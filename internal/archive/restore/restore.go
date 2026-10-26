// Package restore prepares an inert, separate destination from a fully validated
// offline archive. It neither activates boards nor supplies authentication.
package restore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

var ErrInvalid = errors.New("restore validation failed")
var ErrScope = errors.New("restore scope unsupported")

// Prepared is opaque and owns independent source and transformed bytes. The
// destination has no active grant, credential, session or current operation.
type Prepared struct {
	source    *V.Package
	manifest  A.Manifest
	board     A.Board
	history   A.History
	activity  A.Activity
	outcomes  A.Outcomes
	lineage   []A.Lineage
	created   A.Timestamp
	documents map[string][]byte
	inventory A.TargetInventory
}

func hash(b []byte) A.Digest          { h := sha256.Sum256(b); return A.Digest(hex.EncodeToString(h[:])) }
func identityKey(i A.Identity) string { return string(i.BoardID) + "/" + string(i.Incarnation) }
func gapKey(g A.Gap) string {
	f := ""
	if g.FromExclusive != nil {
		f = string(*g.FromExclusive)
	}
	return fmt.Sprintf("%s/%s/%s/%s/%s/%016d/%s", identityKey(g.Origin), g.Class, f, g.ThroughInclusive, g.Reason, g.BoardRevision, g.RecordedAt)
}

// Prepare can consume only A03's opaque validated package. All source bytes are
// validated before this call can allocate a destination identity; no destination
// filesystem is touched here. Entropy failure never falls back to a reused ID.
func Prepare(ctx context.Context, source *V.Package, created A.Timestamp) (*Prepared, error) {
	if ctx == nil || ctx.Err() != nil || source == nil || A.Validate(created) != nil {
		return nil, ErrInvalid
	}
	m, e := source.Manifest()
	if e != nil {
		return nil, e
	}
	var id A.UUID
	for attempts := 0; attempts < 16; attempts++ {
		var raw [16]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return nil, ErrInvalid
		}
		id = A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]))
		fresh := id != m.Source.Incarnation && A.Validate(id) == nil
		for _, l := range m.Lineage {
			if l.Identity.Incarnation == id {
				fresh = false
			}
		}
		if fresh {
			break
		}
		id = ""
	}
	if id == "" {
		return nil, ErrInvalid
	}
	return prepare(ctx, source, created, id)
}

func decode[T any](p *V.Package, name string) (T, error) {
	b, e := p.Member(name)
	if e != nil {
		var z T
		return z, e
	}
	return A.DecodeReadable[T](b)
}

func prepare(ctx context.Context, source *V.Package, created A.Timestamp, incarnation A.UUID) (*Prepared, error) {
	m, e := source.Manifest()
	if e != nil {
		return nil, e
	}
	if A.Validate(incarnation) != nil || incarnation == m.Source.Incarnation {
		return nil, ErrInvalid
	}
	for _, l := range m.Lineage {
		if l.Identity.Incarnation == incarnation {
			return nil, ErrInvalid
		}
	}
	b, e := decode[A.Board](source, "board.json")
	if e != nil {
		return nil, e
	}
	h, e := decode[A.History](source, "history.json")
	if e != nil {
		return nil, e
	}
	a, e := decode[A.Activity](source, "activity.json")
	if e != nil {
		return nil, e
	}
	o, e := decode[A.Outcomes](source, "outcomes.json")
	if e != nil {
		return nil, e
	}
	destination := A.Identity{BoardID: m.Source.BoardID, Incarnation: incarnation}
	b.Identity = destination
	h.Identity = destination
	a.Identity = destination
	o.Identity = destination
	for i := range b.Links {
		t := &b.Links[i].Target
		if t.BoardID == m.Source.BoardID && t.Incarnation == m.Source.Incarnation {
			t.Incarnation = incarnation
		}
	}
	floors := []struct {
		class A.GapClass
		value *A.Timestamp
	}{{"history", b.Coverage.HistoryFloor}, {"activity", b.Coverage.ActivityFloor}, {"outcome_payload", b.Coverage.OutcomePayloadFloor}}
	for _, f := range floors {
		if f.value != nil {
			g := A.Gap{Origin: m.Source, Class: f.class, Reason: "expired", ThroughInclusive: *f.value, RecordedAt: created, BoardRevision: m.Revision}
			duplicate := false
			for _, old := range b.Coverage.Gaps {
				if gapKey(old) == gapKey(g) {
					duplicate = true
				}
			}
			if !duplicate {
				b.Coverage.Gaps = append(b.Coverage.Gaps, g)
			}
		}
	}
	b.Coverage.HistoryFloor = nil
	b.Coverage.ActivityFloor = nil
	b.Coverage.OutcomePayloadFloor = nil
	sort.Slice(b.Coverage.Gaps, func(i, j int) bool { return gapKey(b.Coverage.Gaps[i]) < gapKey(b.Coverage.Gaps[j]) })
	if len(o.Operations) > 0 {
		o.SourceArchives = append(o.SourceArchives, A.SourceArchive{Identity: m.Source, Operations: o.Operations})
	}
	o.Operations = []A.Operation{}
	sort.Slice(o.SourceArchives, func(i, j int) bool {
		return identityKey(o.SourceArchives[i].Identity) < identityKey(o.SourceArchives[j].Identity)
	})
	lineage := append([]A.Lineage{}, m.Lineage...)
	lineage = append(lineage, A.Lineage{Identity: m.Source, ManifestSHA256: source.ManifestSHA256(), Revision: m.Revision})
	sort.Slice(lineage, func(i, j int) bool {
		x, y := lineage[i], lineage[j]
		return fmt.Sprintf("%s/%s/%016d", identityKey(x.Identity), x.ManifestSHA256, x.Revision) < fmt.Sprintf("%s/%s/%016d", identityKey(y.Identity), y.ManifestSHA256, y.Revision)
	})
	p := &Prepared{source: source, manifest: m, board: b, history: h, activity: a, outcomes: o, lineage: lineage, created: created, documents: map[string][]byte{}}
	entries := []A.Entry{}
	jsonTotal := 0
	for _, v := range []struct {
		name  string
		value any
	}{{"board.json", b}, {"history.json", h}, {"activity.json", a}, {"outcomes.json", o}} {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		raw, e := A.Readable(v.value)
		if e != nil {
			return nil, e
		}
		if len(raw) > V.MaxJSONBytes || jsonTotal > V.MaxJSONTotalBytes-len(raw) {
			return nil, ErrScope
		}
		jsonTotal += len(raw)
		p.documents[v.name] = raw
		entries = append(entries, A.Entry{Path: v.name, ByteLength: A.Number(len(raw)), SHA256: hash(raw)})
	}
	for _, entry := range m.Inventory {
		if len(entry.Path) > 6 && entry.Path[:6] == "blobs/" {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	canonical, e := A.Canonical(entries)
	if e != nil {
		return nil, e
	}
	p.inventory = A.TargetInventory{SchemaVersion: 1, Entries: entries, EntryCount: A.Number(len(entries)), SHA256: hash(canonical)}
	// A separate logical document validation checks the transformed graph and
	// exact protected union before anything can be constructed or anchored.
	targetManifest := m
	targetManifest.Source = destination
	targetManifest.Coverage = b.Coverage
	targetManifest.Lineage = lineage
	targetManifest.Inventory = entries
	manifestRaw, e := A.Readable(targetManifest)
	if e != nil {
		return nil, e
	}
	if len(manifestRaw) > V.MaxJSONBytes || jsonTotal > V.MaxJSONTotalBytes-len(manifestRaw) {
		return nil, ErrScope
	}
	if _, e = A.ValidateDocuments(targetManifest, b, h, a, o); e != nil {
		return nil, e
	}
	if e = A.Validate(p.inventory); e != nil {
		return nil, e
	}
	return p, nil
}

func (p *Prepared) Identity() A.Identity {
	if p == nil {
		return A.Identity{}
	}
	return p.board.Identity
}
func (p *Prepared) Receipt(request A.UUID) (A.Receipt, error) {
	if p == nil || A.Validate(request) != nil {
		return A.Receipt{}, ErrInvalid
	}
	r := A.Receipt{ReceiptVersion: 1, RequestID: request, SourceManifest: p.manifest, SourceManifestSHA256: p.source.ManifestSHA256(), Destination: p.board.Identity, Phase: "preparing", Result: "incomplete", CreatedAt: p.created, UpdatedAt: p.created}
	b, e := A.Readable(r)
	if e != nil {
		return A.Receipt{}, e
	}
	return A.DecodeReadable[A.Receipt](b)
}

// VerifyProjection compares independently reprojected native bytes with the
// complete expected transform, including lineage. It returns a fresh anchor only
// after every logical document and raw BLOB agrees. Equal inventory alone cannot
// establish a correct source-to-destination transform.
func (p *Prepared) VerifyProjection(actual interface {
	Manifest() (A.Manifest, error)
	Member(string) ([]byte, error)
}) (A.TargetInventory, error) {
	if p == nil || actual == nil {
		return A.TargetInventory{}, ErrInvalid
	}
	m, e := actual.Manifest()
	if e != nil {
		return A.TargetInventory{}, e
	}
	if m.Source != p.board.Identity || m.Revision != p.manifest.Revision {
		return A.TargetInventory{}, ErrInvalid
	}
	x, _ := A.Canonical(m.Lineage)
	y, _ := A.Canonical(p.lineage)
	if !bytes.Equal(x, y) {
		return A.TargetInventory{}, ErrInvalid
	}
	for name, want := range p.documents {
		got, e := actual.Member(name)
		if e != nil || !bytes.Equal(want, got) {
			return A.TargetInventory{}, ErrInvalid
		}
	}
	x, _ = A.Canonical(m.Inventory)
	y, _ = A.Canonical(p.inventory.Entries)
	if !bytes.Equal(x, y) {
		return A.TargetInventory{}, ErrInvalid
	}
	for _, entry := range m.Inventory {
		if len(entry.Path) > 6 && entry.Path[:6] == "blobs/" {
			got, e := actual.Member(entry.Path)
			want, we := p.source.Member(entry.Path)
			if e != nil || we != nil || !bytes.Equal(got, want) {
				return A.TargetInventory{}, ErrInvalid
			}
		}
	}
	b, e := A.Readable(p.inventory)
	if e != nil {
		return A.TargetInventory{}, e
	}
	return A.DecodeReadable[A.TargetInventory](b)
}
