// Package validate checks complete offline D02 packages without filesystem IO.
// Successful validation establishes internal consistency, never trusted origin.
package validate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const (
	MaxArchiveBytes   = 150994944
	MaxMembers        = 16384
	MaxJSONBytes      = 33554432
	MaxJSONTotalBytes = 67108864
	MaxBlobBytes      = 10485760
	MaxBlobTotalBytes = 67108864
)

var ErrInvalid = errors.New("invalid archive package")
var ErrLimit = errors.New("archive exceeds supported scope")

type member struct {
	name string
	data []byte
}

// Package is an opaque validated snapshot. Its zero value is unusable. It holds
// only private bytes, no source reader, destination writer, path or authority.
// Copies returned by its methods may be altered without changing this snapshot.
type Package struct {
	members []member
	digest  A.Digest
	size    int64
}

func (p *Package) ManifestSHA256() A.Digest {
	if p == nil {
		return ""
	}
	return p.digest
}
func (p *Package) ByteLength() int64 {
	if p == nil {
		return 0
	}
	return p.size
}

// MemberNames returns physical order, including manifest.json and COMPLETE.
func (p *Package) MemberNames() []string {
	if p == nil || p.digest == "" {
		return nil
	}
	names := make([]string, len(p.members))
	for i, m := range p.members {
		names[i] = m.name
	}
	return names
}

// Member returns a fresh copy, suitable for a separately authorized restore
// consumer. There is no streaming callback that could retain private buffers.
func (p *Package) Member(name string) ([]byte, error) {
	if p == nil || p.digest == "" {
		return nil, ErrInvalid
	}
	for _, m := range p.members {
		if m.name == name {
			return bytes.Clone(m.data), nil
		}
	}
	return nil, ErrInvalid
}

// Manifest returns a freshly decoded value, including independent nested slices.
func (p *Package) Manifest() (A.Manifest, error) {
	if p == nil || p.digest == "" {
		return A.Manifest{}, ErrInvalid
	}
	return A.DecodeReadable[A.Manifest](p.members[0].data)
}

// Read consumes through EOF, bounds actual total bytes including ZIP overhead,
// and validates the entire package before returning any usable handle. The caller
// retains responsibility for source stability and offline ownership. Read performs
// no destination writes or publication and preserves the source unchanged.
func Read(ctx context.Context, r io.Reader) (*Package, error) {
	if ctx == nil || r == nil {
		return nil, ErrInvalid
	}
	raw, err := readBounded(ctx, r)
	if err != nil {
		return nil, err
	}
	ms, err := parseZIP(ctx, raw)
	if err != nil {
		return nil, err
	}
	if err = documents(ctx, ms); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return &Package{members: ms, digest: digest(ms[0].data), size: int64(len(raw))}, nil
}

func readBounded(ctx context.Context, r io.Reader) ([]byte, error) {
	var out []byte
	empty := 0
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		next := 32768
		if left := MaxArchiveBytes - len(out) + 1; left < next {
			next = left
		}
		// Never lend a retained package buffer to a source reader, even if the
		// reader keeps and later changes its Read argument.
		scratch := make([]byte, next)
		n, e := r.Read(scratch)
		if n < 0 || n > len(scratch) {
			return nil, ErrInvalid
		}
		if n > MaxArchiveBytes-len(out) {
			return nil, ErrLimit
		}
		out = append(out, scratch[:n]...)
		if e == io.EOF {
			return out, nil
		}
		if e != nil {
			return nil, e
		}
		if n == 0 {
			empty++
			if empty >= 100 {
				return nil, io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
}

func digest(b []byte) A.Digest {
	s := sha256.Sum256(b)
	return A.Digest(hex.EncodeToString(s[:]))
}

func documents(ctx context.Context, ms []member) error {
	m, e := A.DecodeReadable[A.Manifest](ms[0].data)
	if e != nil {
		return ErrInvalid
	}
	if !bytes.Equal(ms[len(ms)-1].data, []byte(string(digest(ms[0].data))+"\n")) {
		return ErrInvalid
	}
	if len(m.Inventory) != len(ms)-2 {
		return ErrInvalid
	}
	actual := make(map[string][]byte, len(ms)-2)
	for _, v := range ms[1 : len(ms)-1] {
		actual[v.name] = v.data
	}
	for _, v := range m.Inventory {
		if e := ctx.Err(); e != nil {
			return e
		}
		b, ok := actual[v.Path]
		if !ok || uint64(len(b)) != uint64(v.ByteLength) || digest(b) != v.SHA256 {
			return ErrInvalid
		}
		delete(actual, v.Path)
	}
	if len(actual) != 0 {
		return ErrInvalid
	}
	b, e := A.DecodeReadable[A.Board](ms[1].data)
	if e != nil {
		return ErrInvalid
	}
	h, e := A.DecodeReadable[A.History](ms[2].data)
	if e != nil {
		return ErrInvalid
	}
	a, e := A.DecodeReadable[A.Activity](ms[3].data)
	if e != nil {
		return ErrInvalid
	}
	o, e := A.DecodeReadable[A.Outcomes](ms[4].data)
	if e != nil {
		return ErrInvalid
	}
	_, e = A.ValidateDocuments(m, b, h, a, o)
	if e != nil {
		return ErrInvalid
	}
	return nil
}
