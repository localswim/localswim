// Package export prepares and writes bounded D02 archives from one pinned reader.
package export

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
	"sort"

	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/writer"
)

const (
	MaxArchiveBytes   = 150994944
	MaxMembers        = 16384
	MaxJSONBytes      = 33554432
	MaxJSONTotalBytes = 67108864
	MaxBlobBytes      = 10485760
	MaxBlobTotalBytes = 67108864
)

var ErrLimit = errors.New("archive exceeds supported scope")
var ErrPrecondition = errors.New("export snapshot precondition mismatch")

// Request is an internal trusted-adapter boundary. Verify MUST authenticate the
// caller and require current read+export rights through the supplied reader.
// A nonnil callback is mandatory; the package never supplies a permissive default.
// The callback must not retain the reader or perform output or mutation.
type Request struct {
	Identity   A.Identity
	Revision   A.Number
	ExportedAt A.Timestamp
	Verify     func(context.Context, writer.Read) error
}

type member struct {
	name string
	data []byte
}

// Prepared owns private immutable copies; it holds no database lease or pins.
// Its availability proves package preparation, never delivery or a saved file.
type Prepared struct {
	members []member
	digest  A.Digest
	size    int64
}

func (p *Prepared) ManifestSHA256() A.Digest {
	if p == nil {
		return ""
	}
	return p.digest
}
func (p *Prepared) ByteLength() int64 {
	if p == nil {
		return 0
	}
	return p.size
}

// Prepare authenticates and validates all inventory/bytes in one actual S03
// snapshot. It releases the reader before its pins, including on every failure.
// No archive byte is exposed until projection, validation and cleanup succeed.
func Prepare(ctx context.Context, store *S.Store, req Request) (out *Prepared, err error) {
	if ctx == nil || store == nil || req.Verify == nil {
		return nil, errors.New("export verifier, context and store required")
	}
	if err = A.Validate(req.Identity); err != nil {
		return nil, err
	}
	if err = A.Validate(req.Revision); err != nil {
		return nil, err
	}
	if err = A.Validate(req.ExportedAt); err != nil {
		return nil, err
	}
	x, err := recovery.RegistryFor(store).BeginExportSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		if e := x.Close(); e != nil {
			out = nil
			err = errors.Join(err, e)
		}
	}()
	id, revision := x.Identity()
	if id != req.Identity || revision != req.Revision {
		return nil, ErrPrecondition
	}
	err = x.Read(ctx, func(r writer.Read) error {
		if e := req.Verify(ctx, r); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		facts := x.Facts()
		if len(facts) > MaxMembers-6 {
			return ErrLimit
		}
		b, h, a, o, lineage, e := project(r, id, revision)
		if e != nil {
			return e
		}
		out, e = assemble(ctx, b, h, a, o, lineage, req.ExportedAt, facts, func(f A.ByteFact) ([]byte, error) {
			var n int64
			// Inspect declared and actual sizes before asking the driver to allocate bytes.
			if e := r.Scan("SELECT length(bytes) FROM blob WHERE incarnation=? AND sha256=? AND byte_length=?", []any{string(id.Incarnation), string(f.SHA256), uint64(f.ByteLength)}, &n); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					store.Fence()
				}
				return nil, e
			}
			if n < 0 || n > MaxBlobBytes || A.Number(n) != f.ByteLength {
				store.Fence()
				return nil, errors.New("protected blob length mismatch")
			}
			var raw []byte
			if e := r.Scan("SELECT bytes FROM blob WHERE incarnation=? AND sha256=? AND byte_length=? AND length(bytes)=?", []any{string(id.Incarnation), string(f.SHA256), uint64(f.ByteLength), n}, &raw); e != nil {
				if errors.Is(e, sql.ErrNoRows) {
					store.Fence()
				}
				return nil, e
			}
			if len(raw) != int(n) || digest(raw) != f.SHA256 {
				store.Fence()
				return nil, errors.New("protected blob integrity failure")
			}
			return raw, nil
		})
		return e
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func digest(b []byte) A.Digest { s := sha256.Sum256(b); return A.Digest(hex.EncodeToString(s[:])) }

func assemble(ctx context.Context, b A.Board, h A.History, a A.Activity, o A.Outcomes, lineage []A.Lineage, at A.Timestamp, facts []A.ByteFact, blob func(A.ByteFact) ([]byte, error)) (*Prepared, error) {
	if len(facts) > MaxMembers-6 {
		return nil, ErrLimit
	}
	if e := A.Validate(facts); e != nil {
		return nil, e
	}
	m := A.Manifest{FormatVersion: 1, SchemaVersion: 1, HashAlgorithm: "sha256", Source: b.Identity, Revision: b.Revision, ExportedAt: at, Lineage: lineage, Retention: b.Retention, RetentionVersion: b.RetentionVersion, Coverage: b.Coverage, Inventory: []A.Entry{}}
	p := &Prepared{members: make([]member, 0, len(facts)+6)}
	jsonTotal := 0
	for _, item := range []struct {
		name string
		doc  any
	}{{"board.json", b}, {"history.json", h}, {"activity.json", a}, {"outcomes.json", o}} {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		n, e := readableLength(item.doc)
		if e != nil {
			return nil, e
		}
		if e = jsonBudget(&jsonTotal, n); e != nil {
			return nil, e
		}
		if e := A.Validate(item.doc); e != nil {
			return nil, e
		}
		raw, e := A.Readable(item.doc)
		if e != nil {
			return nil, e
		}
		if len(raw) != n {
			return nil, errors.New("archive JSON size mismatch")
		}
		p.members = append(p.members, member{item.name, raw})
		m.Inventory = append(m.Inventory, A.Entry{Path: item.name, ByteLength: A.Number(len(raw)), SHA256: digest(raw)})
	}
	var blobTotal uint64
	for _, f := range facts {
		if uint64(f.ByteLength) > MaxBlobBytes || blobTotal > MaxBlobTotalBytes-uint64(f.ByteLength) {
			return nil, ErrLimit
		}
		blobTotal += uint64(f.ByteLength)
		m.Inventory = append(m.Inventory, A.Entry{Path: "blobs/sha256-" + string(f.SHA256), ByteLength: f.ByteLength, SHA256: f.SHA256})
	}
	sort.Slice(m.Inventory, func(i, j int) bool { return m.Inventory[i].Path < m.Inventory[j].Path })
	n, e := readableLength(m)
	if e != nil {
		return nil, e
	}
	if e = jsonBudget(&jsonTotal, n); e != nil {
		return nil, e
	}
	pins, e := A.ValidateDocuments(m, b, h, a, o)
	if e != nil {
		return nil, e
	}
	if len(pins) != len(facts) {
		return nil, errors.New("snapshot pin inventory mismatch")
	}
	for i := range pins {
		if pins[i] != facts[i] {
			return nil, errors.New("snapshot pin inventory mismatch")
		}
	}
	raw, e := A.Readable(m)
	if e != nil {
		return nil, e
	}
	if len(raw) != n {
		return nil, errors.New("archive JSON size mismatch")
	}
	p.digest = digest(raw)
	p.members = append([]member{{"manifest.json", raw}}, p.members...)
	// Compute exact classic stored-ZIP size before loading BLOBs or writing output.
	lengths := make([]struct {
		name string
		n    int64
	}, 0, len(facts)+6)
	for _, v := range p.members {
		lengths = append(lengths, struct {
			name string
			n    int64
		}{v.name, int64(len(v.data))})
	}
	for _, f := range facts {
		lengths = append(lengths, struct {
			name string
			n    int64
		}{"blobs/sha256-" + string(f.SHA256), int64(f.ByteLength)})
	}
	lengths = append(lengths, struct {
		name string
		n    int64
	}{"COMPLETE", 65})
	p.size = 22
	for _, v := range lengths {
		if e = addZIPSize(&p.size, v.name, v.n); e != nil {
			return nil, e
		}
	}
	for _, f := range facts {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		data, e := blob(f)
		if e != nil {
			return nil, e
		}
		if uint64(len(data)) != uint64(f.ByteLength) || digest(data) != f.SHA256 {
			return nil, errors.New("protected blob integrity failure")
		}
		p.members = append(p.members, member{"blobs/sha256-" + string(f.SHA256), data})
	}
	p.members = append(p.members, member{"COMPLETE", []byte(string(p.digest) + "\n")})
	return p, nil
}

func jsonBudget(total *int, n int) error {
	if n < 0 || n > MaxJSONBytes || *total > MaxJSONTotalBytes-n {
		return ErrLimit
	}
	*total += n
	return nil
}
func addZIPSize(total *int64, name string, n int64) error {
	overhead := int64(76 + 2*len(name))
	if n < 0 || n > MaxArchiveBytes || *total > MaxArchiveBytes-n-overhead {
		return ErrLimit
	}
	*total += n + overhead
	return nil
}

type output struct {
	ctx context.Context
	w   io.Writer
	n   int64
}

func (w *output) Write(p []byte) (int, error) {
	if e := w.ctx.Err(); e != nil {
		return 0, e
	}
	if int64(len(p)) > MaxArchiveBytes-w.n {
		return 0, ErrLimit
	}
	// External Writers may retain or alter their argument. Never lend them any
	// prepared payload, ZIP buffer, or scratch reused by another call.
	total := 0
	for len(p) > 0 {
		if e := w.ctx.Err(); e != nil {
			return total, e
		}
		next := len(p)
		if next > 32768 {
			next = 32768
		}
		scratch := append([]byte(nil), p[:next]...)
		n, e := w.w.Write(scratch)
		if n < 0 || n > next {
			return total, errors.New("invalid output write count")
		}
		total += n
		w.n += int64(n)
		if e != nil {
			return total, e
		}
		if n != next {
			return total, io.ErrShortWrite
		}
		p = p[next:]
	}
	return total, nil
}

// Write emits COMPLETE last and finalizes ZIP only after every write succeeds.
// Any error leaves delivery unconfirmed; callers must never publish a partial file
// or claim a verified browser save from this method's success alone.
func (p *Prepared) Write(ctx context.Context, w io.Writer) error {
	if ctx == nil || w == nil || p == nil || len(p.members) < 6 {
		return errors.New("prepared archive and output required")
	}
	out := &output{ctx: ctx, w: w}
	z := zip.NewWriter(out)
	for _, v := range p.members {
		if e := ctx.Err(); e != nil {
			return e
		}
		h := &zip.FileHeader{Name: v.name, Method: zip.Store, CRC32: crc32.ChecksumIEEE(v.data), CompressedSize64: uint64(len(v.data)), UncompressedSize64: uint64(len(v.data)), CreatorVersion: 20, ReaderVersion: 20}
		entry, e := z.CreateRaw(h)
		if e != nil {
			return e
		}
		if _, e = entry.Write(v.data); e != nil {
			return e
		}
	}
	if e := z.Close(); e != nil {
		return e
	}
	if out.n != p.size {
		return errors.New("archive output size mismatch")
	}
	return nil
}
