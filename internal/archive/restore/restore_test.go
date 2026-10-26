package restore

import (
	"archive/zip"
	"bytes"
	"context"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

const testTime A.Timestamp = "2026-10-26T12:00:00Z"
const testInc A.UUID = "99999999-9999-9999-9999-999999999999"

func fixture(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("..", "..", "..", "fixtures", "contracts", "archive-recovery", "package")
	members := map[string][]byte{}
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			name, _ := filepath.Rel(root, path)
			members[filepath.ToSlash(name)] = raw
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return members
}
func sourcePackage(t *testing.T, members map[string][]byte) *V.Package {
	t.Helper()
	m, e := A.DecodeReadable[A.Manifest](members["manifest.json"])
	if e != nil {
		t.Fatal(e)
	}
	m.Inventory = []A.Entry{}
	names := []string{}
	for name, raw := range members {
		if name != "manifest.json" && name != "COMPLETE" {
			m.Inventory = append(m.Inventory, A.Entry{Path: name, ByteLength: A.Number(len(raw)), SHA256: hash(raw)})
			if len(name) > 6 && name[:6] == "blobs/" {
				names = append(names, name)
			}
		}
	}
	sort.Slice(m.Inventory, func(i, j int) bool { return m.Inventory[i].Path < m.Inventory[j].Path })
	sort.Strings(names)
	members["manifest.json"], e = A.Readable(m)
	if e != nil {
		t.Fatal(e)
	}
	members["COMPLETE"] = []byte(string(hash(members["manifest.json"])) + "\n")
	names = append([]string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json"}, names...)
	names = append(names, "COMPLETE")
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	for _, name := range names {
		data := members[name]
		w, e := z.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, ReaderVersion: 20, CreatorVersion: 20, CRC32: crc32.ChecksumIEEE(data), CompressedSize64: uint64(len(data)), UncompressedSize64: uint64(len(data))})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	p, e := V.Read(context.Background(), &raw)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestTransformPreservesProvenanceAndIsolatesLedger(t *testing.T) {
	source := sourcePackage(t, fixture(t))
	before, _ := source.Member("outcomes.json")
	p, e := prepare(context.Background(), source, testTime, testInc)
	if e != nil {
		t.Fatal(e)
	}
	original, _ := decode[A.Board](source, "board.json")
	history, _ := decode[A.History](source, "history.json")
	outcomes, _ := decode[A.Outcomes](source, "outcomes.json")
	if p.board.Identity.Incarnation != testInc || p.board.Identity.BoardID != original.Identity.BoardID || p.board.Revision != original.Revision || !reflect.DeepEqual(p.board.Cards, original.Cards) || !reflect.DeepEqual(p.history.Records, history.Records) {
		t.Fatal("logical IDs, revisions or historical data changed")
	}
	if len(p.outcomes.Operations) != 0 || len(p.outcomes.SourceArchives) != len(outcomes.SourceArchives)+1 {
		t.Fatal("source ledger was adopted")
	}
	for _, g := range p.outcomes.SourceArchives {
		if g.Identity == outcomes.Identity && !reflect.DeepEqual(g.Operations, outcomes.Operations) {
			t.Fatal("source operation intent/fingerprint changed")
		}
	}
	after, _ := source.Member("outcomes.json")
	if !bytes.Equal(before, after) {
		t.Fatal("source changed")
	}
	r, e := p.Receipt("88888888-8888-8888-8888-888888888888")
	if e != nil || r.TargetInventory != nil || r.Phase != "preparing" {
		t.Fatal("early receipt anchor", e)
	}
	if _, e = prepare(context.Background(), source, testTime, sourceManifest(t, source).Source.Incarnation); e == nil {
		t.Fatal("source incarnation reused")
	}
	for _, l := range p.manifest.Lineage {
		if _, e = prepare(context.Background(), source, testTime, l.Identity.Incarnation); e == nil {
			t.Fatal("lineage incarnation reused")
		}
	}
}
func sourceManifest(t *testing.T, p *V.Package) A.Manifest {
	t.Helper()
	m, e := p.Manifest()
	if e != nil {
		t.Fatal(e)
	}
	return m
}

func TestFloorsBecomeSourceGapsAndEmptyCurrentLedger(t *testing.T) {
	ms := fixture(t)
	b, e := A.DecodeReadable[A.Board](ms["board.json"])
	if e != nil {
		t.Fatal(e)
	}
	m, e := A.DecodeReadable[A.Manifest](ms["manifest.json"])
	if e != nil {
		t.Fatal(e)
	}
	floor := A.Timestamp("2026-08-01T00:00:00Z")
	b.Coverage.HistoryFloor = &floor
	b.Coverage.ActivityFloor = &floor
	b.Coverage.OutcomePayloadFloor = &floor
	m.Coverage = b.Coverage
	ms["board.json"], _ = A.Readable(b)
	ms["manifest.json"], _ = A.Readable(m)
	o, e := A.DecodeReadable[A.Outcomes](ms["outcomes.json"])
	if e != nil {
		t.Fatal(e)
	}
	o.Operations = []A.Operation{}
	ms["outcomes.json"], _ = A.Readable(o)
	p, e := prepare(context.Background(), sourcePackage(t, ms), testTime, testInc)
	if e != nil {
		t.Fatal(e)
	}
	if p.board.Coverage.HistoryFloor != nil || p.board.Coverage.ActivityFloor != nil || p.board.Coverage.OutcomePayloadFloor != nil || len(p.board.Coverage.Gaps) != len(b.Coverage.Gaps)+3 {
		t.Fatal("floor transform")
	}
	if len(p.outcomes.Operations) != 0 || len(p.outcomes.SourceArchives) != len(o.SourceArchives) {
		t.Fatal("empty ledger manufactured an unrepresentable group")
	}
	for _, g := range p.board.Coverage.Gaps {
		if g.ThroughInclusive == floor && (g.Origin != m.Source || g.RecordedAt != testTime || g.BoardRevision != m.Revision || g.Reason != "expired") {
			t.Fatal("gap origin metadata")
		}
	}
}

func TestDeclaredEmptyGroupsAndDistinctCompleteGapsArePreserved(t *testing.T) {
	t.Run("empty inherited operation group", func(t *testing.T) {
		ms := fixture(t)
		o, e := A.DecodeReadable[A.Outcomes](ms["outcomes.json"])
		if e != nil {
			t.Fatal(e)
		}
		o.SourceArchives = append(o.SourceArchives, A.SourceArchive{Identity: A.Identity{BoardID: o.Identity.BoardID, Incarnation: "77777777-7777-7777-7777-777777777777"}, Operations: []A.Operation{}})
		sort.Slice(o.SourceArchives, func(i, j int) bool {
			return identityKey(o.SourceArchives[i].Identity) < identityKey(o.SourceArchives[j].Identity)
		})
		ms["outcomes.json"], _ = A.Readable(o)
		p, e := prepare(context.Background(), sourcePackage(t, ms), testTime, testInc)
		if e != nil || p == nil || len(p.outcomes.SourceArchives) != len(o.SourceArchives)+1 {
			t.Fatal("empty source group not preserved", e)
		}
		found := false
		for _, g := range p.outcomes.SourceArchives {
			if g.Identity.Incarnation == "77777777-7777-7777-7777-777777777777" {
				found = true
				if len(g.Operations) != 0 {
					t.Fatal("empty group gained operations")
				}
			}
		}
		if !found {
			t.Fatal("empty inherited group omitted")
		}
	})
	t.Run("native gap key collision", func(t *testing.T) {
		ms := fixture(t)
		b, e := A.DecodeReadable[A.Board](ms["board.json"])
		if e != nil {
			t.Fatal(e)
		}
		m, e := A.DecodeReadable[A.Manifest](ms["manifest.json"])
		if e != nil {
			t.Fatal(e)
		}
		g := b.Coverage.Gaps[0]
		g.RecordedAt = "2026-10-02T00:00:00Z"
		b.Coverage.Gaps = append(b.Coverage.Gaps, g)
		sort.Slice(b.Coverage.Gaps, func(i, j int) bool { return gapKey(b.Coverage.Gaps[i]) < gapKey(b.Coverage.Gaps[j]) })
		m.Coverage = b.Coverage
		ms["board.json"], _ = A.Readable(b)
		ms["manifest.json"], _ = A.Readable(m)
		p, e := prepare(context.Background(), sourcePackage(t, ms), testTime, testInc)
		if e != nil || p == nil || len(p.board.Coverage.Gaps) != len(b.Coverage.Gaps) {
			t.Fatal("distinct complete gap evidence not retained", e)
		}
		if !reflect.DeepEqual(p.board.Coverage.Gaps, b.Coverage.Gaps) {
			t.Fatal("gap evidence changed")
		}
	})
	t.Run("distinct fromExclusive", func(t *testing.T) {
		ms := fixture(t)
		b, _ := A.DecodeReadable[A.Board](ms["board.json"])
		m, _ := A.DecodeReadable[A.Manifest](ms["manifest.json"])
		b.Coverage.Gaps[0].Reason = "expired"
		g := b.Coverage.Gaps[0]
		from := A.Timestamp("2026-08-01T00:00:00Z")
		g.FromExclusive = &from
		b.Coverage.Gaps = append(b.Coverage.Gaps, g)
		sort.Slice(b.Coverage.Gaps, func(i, j int) bool { return gapKey(b.Coverage.Gaps[i]) < gapKey(b.Coverage.Gaps[j]) })
		m.Coverage = b.Coverage
		ms["board.json"], _ = A.Readable(b)
		ms["manifest.json"], _ = A.Readable(m)
		p, e := prepare(context.Background(), sourcePackage(t, ms), testTime, testInc)
		if e != nil || !reflect.DeepEqual(p.board.Coverage.Gaps, b.Coverage.Gaps) {
			t.Fatal("distinct fromExclusive evidence not preserved", e)
		}
	})
}

func TestFloorConversionDeduplicatesOnlyAnIdenticalCompleteGap(t *testing.T) {
	ms := fixture(t)
	b, _ := A.DecodeReadable[A.Board](ms["board.json"])
	m, _ := A.DecodeReadable[A.Manifest](ms["manifest.json"])
	g := b.Coverage.Gaps[0]
	g.Origin = m.Source
	g.Class = "outcome_payload"
	g.Reason = "expired"
	g.FromExclusive = nil
	g.BoardRevision = m.Revision
	g.RecordedAt = testTime
	b.Coverage.Gaps = []A.Gap{g}
	b.Coverage.OutcomePayloadFloor = &g.ThroughInclusive
	m.Coverage = b.Coverage
	ms["board.json"], _ = A.Readable(b)
	ms["manifest.json"], _ = A.Readable(m)
	p, e := prepare(context.Background(), sourcePackage(t, ms), testTime, testInc)
	if e != nil || len(p.board.Coverage.Gaps) != 1 || !reflect.DeepEqual(p.board.Coverage.Gaps[0], g) {
		t.Fatal("identical complete gap was not deduplicated", e)
	}
	later := A.Timestamp("2026-10-26T12:00:01Z")
	p, e = prepare(context.Background(), sourcePackage(t, ms), later, testInc)
	if e != nil || len(p.board.Coverage.Gaps) != 2 {
		t.Fatal("different recording time was deduplicated", e)
	}
}

func TestInternalCardRefsRemapExternalAndHistoricalRefsStaySourceQualified(t *testing.T) {
	ms := fixture(t)
	b, e := A.DecodeReadable[A.Board](ms["board.json"])
	if e != nil {
		t.Fatal(e)
	}
	peer := b.Cards[0]
	peer.CardID = "66666666-6666-6666-6666-666666666666"
	peer.Ticket = 2
	peer.Revision = 1
	peer.Title = "Synthetic peer"
	peer.CommentIDs = []A.UUID{}
	peer.LinkIDs = []A.UUID{}
	peer.AttachmentIDs = []A.UUID{}
	peer.CurrentApprovalID = nil
	b.Cards = append(b.Cards, peer)
	b.NextTicket = 3
	internal := A.Link{LinkID: "55555555-5555-5555-5555-555555555555", CardID: b.Cards[0].CardID, Kind: "informational", Target: A.CardRef{BoardID: b.Identity.BoardID, Incarnation: b.Identity.Incarnation, CardID: peer.CardID}, Active: true, CreatedAt: testTime, CreatedIn: b.Identity}
	external := internal
	external.LinkID = "77777777-7777-7777-7777-777777777777"
	external.Target.Incarnation = "44444444-4444-4444-4444-444444444444"
	b.Links = []A.Link{internal, external}
	b.Cards[0].LinkIDs = []A.UUID{internal.LinkID, external.LinkID}
	ms["board.json"], _ = A.Readable(b)
	p, e := prepare(context.Background(), sourcePackage(t, ms), testTime, testInc)
	if e != nil {
		t.Fatal(e)
	}
	if p.board.Links[0].Target.Incarnation != testInc || p.board.Links[0].CreatedIn != internal.CreatedIn || p.board.Links[1].Target != external.Target {
		t.Fatal("internal/external provenance transform")
	}
	actual := &projection{manifest: A.Manifest{Source: p.board.Identity, Revision: p.manifest.Revision, Lineage: p.lineage, Inventory: p.inventory.Entries}, members: map[string][]byte{}}
	for name, raw := range p.documents {
		actual.members[name] = bytes.Clone(raw)
	}
	for _, entry := range p.inventory.Entries {
		if len(entry.Path) > 6 && entry.Path[:6] == "blobs/" {
			actual.members[entry.Path], _ = p.source.Member(entry.Path)
		}
	}
	if _, e = p.VerifyProjection(actual); e != nil {
		t.Fatal(e)
	}
	tampered := p.board
	tampered.NextTicket++
	actual.members["board.json"], _ = A.Readable(tampered)
	if _, e = p.VerifyProjection(actual); e == nil {
		t.Fatal("equal manifest inventory accepted incorrect transform")
	}
}
