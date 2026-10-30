//go:build windows && amd64

package publication

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	E "github.com/localswim/localswim/internal/archive/export"
	V "github.com/localswim/localswim/internal/archive/validate"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

type parityFixture struct {
	n                         *native
	root, data, input, output string
	s                         *S.Store
	g                         *grants.Service
	id                        A.Identity
	actor, maintenance        string
}

func sha(raw []byte) A.Digest { x := sha256.Sum256(raw); return A.Digest(hex.EncodeToString(x[:])) }

func parityGrant(t *testing.T, s *S.Store, id A.Identity, actor string, rights ...string) *grants.Service {
	t.Helper()
	grant := string(uuid(t))
	l, e := s.Writer(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(ctx(t), func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx(t), "INSERT INTO grant_fact VALUES(?,?)", id.Incarnation, grant); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx(t), "INSERT INTO grant_record VALUES(?,?,?,1)", id.Incarnation, grant, actor); e != nil {
			return e
		}
		for _, right := range rights {
			if _, e := c.ExecContext(ctx(t), "INSERT INTO grant_right VALUES(?,?,?)", id.Incarnation, grant, right); e != nil {
				return e
			}
		}
		return nil
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	const installation = "60000000-0000-4000-8000-000000000001"
	const origin = "https://synthetic.localhost:47443"
	g, e := grants.New(installation, origin, func(context.Context, writer.Read, time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: string(id.BoardID), Incarnation: string(id.Incarnation), ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	return g
}

func newParityFixture(t *testing.T, expiry bool) *parityFixture {
	t.Helper()
	if schema.Version != 6 || schema.BaseSchemaVersion != 5 {
		t.Fatal("current physical6/base5 provider required before consumer qualification")
	}
	n, root := nativeRoot(t)
	f := &parityFixture{n: n, root: root, data: filepath.Join(root, "source"), input: filepath.Join(root, "input"), output: filepath.Join(root, "output"), id: A.Identity{BoardID: uuid(t), Incarnation: uuid(t)}, actor: string(uuid(t)), maintenance: string(uuid(t))}
	for _, p := range []string{f.data, f.input, f.output} {
		if e := n.mkdir(p); e != nil {
			t.Fatal(e)
		}
	}
	s, e := S.Create(ctx(t), f.data, schema.Identity{BoardID: string(f.id.BoardID), Incarnation: string(f.id.Incarnation)})
	if e != nil {
		t.Fatal(e)
	}
	f.s = s
	t.Cleanup(func() { _ = s.Close() })
	l, e := s.Writer(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(ctx(t), func(c *sql.Conn) error {
		run := func(q string, args ...any) error { _, e := c.ExecContext(ctx(t), q, args...); return e }
		if e := run("BEGIN IMMEDIATE"); e != nil {
			return e
		}
		for _, a := range []struct{ id, kind, label string }{{f.actor, "human", "Synthetic parity human"}, {f.maintenance, "automation", "Synthetic parity expiry"}} {
			if e := run("INSERT INTO actor VALUES(?,?,?,?,?,?)", f.id.Incarnation, a.id, a.kind, a.label, f.id.BoardID, f.id.Incarnation); e != nil {
				return e
			}
		}
		days := 90
		if expiry {
			days = 1
		}
		if e := run("UPDATE board SET revision=2,next_ticket=2,history_days=?", days); e != nil {
			return e
		}
		card := uuid(t)
		if e := run("INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'card',?,?,?,?)", f.id.Incarnation, card, card, f.id.BoardID, f.id.Incarnation); e != nil {
			return e
		}
		if e := run("INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,1,?)", f.id.Incarnation, card); e != nil {
			return e
		}
		if e := run("INSERT INTO card(incarnation,card_id,revision,title,description) VALUES(?,?,2,'Synthetic current','Preserved current text')", f.id.Incarnation, card); e != nil {
			return e
		}
		state, e := A.Canonical(A.SnapshotState{Ticket: 1, Title: "Synthetic earlier", Description: "Old synthetic history", Priority: "P3", Lane: "Ready for Work"})
		if e != nil {
			return e
		}
		old := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Second).Format("2006-01-02T15:04:05Z")
		if e := run("INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,1,?,?,?)", f.id.Incarnation, f.id.BoardID, f.id.Incarnation, card, f.actor, old, string(state)); e != nil {
			return e
		}
		if e := schema.Validate(ctx(t), c, schema.Identity{BoardID: string(f.id.BoardID), Incarnation: string(f.id.Incarnation)}); e != nil {
			return e
		}
		return run("COMMIT")
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	f.g = parityGrant(t, s, f.id, f.actor, "read", "export", "read-own-outcome", "history-admin")
	return f
}

func parityExport(t *testing.T, s *S.Store, g *grants.Service, id A.Identity, actor string) []byte {
	t.Helper()
	var revision A.Number
	l, e := s.Reader(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(ctx(t), func(c *sql.Conn) error {
		return c.QueryRowContext(ctx(t), "SELECT revision FROM board WHERE singleton=1").Scan(&revision)
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	p, e := E.Prepare(ctx(t), s, E.Request{Identity: id, Revision: revision, ExportedAt: stamp(time.Now), Verify: func(c context.Context, r writer.Read) error {
		return g.Require(c, r, string(id.BoardID), string(id.Incarnation), actor, "read", "export")
	}})
	if e != nil {
		t.Fatal(e)
	}
	var raw bytes.Buffer
	if e = p.Write(ctx(t), &raw); e != nil {
		t.Fatal(e)
	}
	return raw.Bytes()
}

func nativeDocuments(t *testing.T, dir string, r A.Receipt) E.OfflineDocuments {
	t.Helper()
	var d E.OfflineDocuments
	e := S.InspectExisting(ctx(t), dir, schema.Identity{BoardID: string(r.Destination.BoardID), Incarnation: string(r.Destination.Incarnation)}, func(read S.InspectionRead) error {
		var e error
		d, e = E.ProjectOffline(read, r.Destination, r.SourceManifest.Revision)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	return d
}

func TestNativePhysical4CompleteZeroRevisionGapPackageRestoresWithoutMutation(t *testing.T) {
	n, root := nativeRoot(t)
	input, output := filepath.Join(root, "input"), filepath.Join(root, "output")
	for _, p := range []string{input, output} {
		if e := n.mkdir(p); e != nil {
			t.Fatal(e)
		}
	}
	id := A.Identity{BoardID: uuid(t), Incarnation: uuid(t)}
	through := A.Timestamp("2026-09-01T00:00:00Z")
	gap := A.Gap{Origin: id, Class: "activity", Reason: "expired", ThroughInclusive: through, RecordedAt: "2026-10-01T00:00:00Z", BoardRevision: 0}
	coverage := A.Coverage{ActivityFloor: &through, Gaps: []A.Gap{gap}}
	retention := A.Retention{HistoryDays: 90, ActivityDays: 365, OutcomeDays: 180}
	b := A.Board{SchemaVersion: 1, Identity: id, Revision: 0, NextTicket: 1, PolicyVersion: 1, Retention: retention, RetentionVersion: 1, Coverage: coverage, Actors: []A.Actor{}, Cards: []A.Card{}, Comments: []A.Comment{}, Links: []A.Link{}, Attachments: []A.Attachment{}, Approvals: []A.Approval{}, Tombstones: []A.EntityTombstone{}}
	h := A.History{SchemaVersion: 1, Identity: id, Revision: 0, Records: []A.Snapshot{}}
	a := A.Activity{SchemaVersion: 1, Identity: id, Revision: 0, Events: []A.Event{}}
	o := A.Outcomes{SchemaVersion: 1, Identity: id, Revision: 0, Operations: []A.Operation{}, SourceArchives: []A.SourceArchive{}}
	m := A.Manifest{FormatVersion: 1, SchemaVersion: 1, HashAlgorithm: "sha256", Source: id, Revision: 0, ExportedAt: stamp(time.Now), Lineage: []A.Lineage{}, Retention: retention, RetentionVersion: 1, Coverage: coverage, Inventory: []A.Entry{}}
	members := map[string][]byte{}
	for _, v := range []struct {
		name  string
		value any
	}{{"board.json", b}, {"history.json", h}, {"activity.json", a}, {"outcomes.json", o}} {
		raw, e := A.Readable(v.value)
		if e != nil {
			t.Fatal(e)
		}
		members[v.name] = raw
		m.Inventory = append(m.Inventory, A.Entry{Path: v.name, ByteLength: A.Number(len(raw)), SHA256: sha(raw)})
	}
	sort.Slice(m.Inventory, func(i, j int) bool { return m.Inventory[i].Path < m.Inventory[j].Path })
	if _, e := A.ValidateDocuments(m, b, h, a, o); e != nil {
		t.Fatal("complete D02 gap0 fixture refused", e)
	}
	members["manifest.json"], _ = A.Readable(m)
	members["COMPLETE"] = []byte(string(sha(members["manifest.json"])) + "\n")
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	for _, name := range []string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json", "COMPLETE"} {
		data := members[name]
		w, e := z.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, ReaderVersion: 20, CreatorVersion: 20, CRC32: crc32.ChecksumIEEE(data), CompressedSize64: uint64(len(data)), UncompressedSize64: uint64(len(data))})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := V.Read(ctx(t), bytes.NewReader(raw.Bytes())); e != nil {
		t.Fatal("complete A03 gap0 package refused", e)
	}
	source := filepath.Join(input, "zero.zip")
	if e := n.write(source, raw.Bytes()); e != nil {
		t.Fatal(e)
	}
	q := Request{SourceArchive: source, Destination: filepath.Join(output, "zero"), RequestID: uuid(t)}
	if got := Restore(ctx(t), q); got.Exit != 0 {
		t.Fatal("revision0 gap restore", got.Exit)
	}
	if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 0 {
		t.Fatal("revision0 gap status", got.Exit)
	}
	r, e := n.receipt(q.Destination)
	if e != nil {
		t.Fatal(e)
	}
	d := nativeDocuments(t, filepath.Join(q.Destination, "data"), r)
	if d.Board.Revision != 0 || len(d.Outcomes.Operations) != 0 || len(d.Board.Coverage.Gaps) != 2 {
		t.Fatal("revision0 provenance changed")
	}
	old, newGap := false, false
	for _, g := range d.Board.Coverage.Gaps {
		if reflect.DeepEqual(g, gap) {
			old = true
		}
		if g.Origin == id && g.Class == "activity" && g.Reason == "expired" && g.BoardRevision == 0 && g.ThroughInclusive == through && g.RecordedAt == r.CreatedAt {
			newGap = true
		}
	}
	if !old || !newGap {
		t.Fatal("zero-revision complete gap tuple lost")
	}
	after, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(raw.Bytes(), after) {
		t.Fatal("zero-revision restore mutated source")
	}
}

func TestNativePhysical4ExpiryAndRetentionExportRestorePreserveBothGapTimes(t *testing.T) {
	for _, method := range []string{"expiry", "retention.set"} {
		t.Run(method, func(t *testing.T) {
			f := newParityFixture(t, method == "expiry")
			service := recovery.New(f.s, f.g)
			route := recovery.Route{BoardID: string(f.id.BoardID), Incarnation: string(f.id.Incarnation)}
			if method == "expiry" {
				result, e := service.Expire(ctx(t), route, func(context.Context, writer.Read) (recovery.MaintenancePrincipal, error) {
					return recovery.MaintenancePrincipal{ActorID: f.maintenance, Label: "Synthetic parity expiry"}, nil
				})
				if e != nil || result.Unknown || result.Revision == nil || *result.Revision != 3 {
					t.Fatal("actual native expiry", e)
				}
			} else {
				input, _ := json.Marshal(A.Retention{HistoryDays: 1, ActivityDays: 365, OutcomeDays: 180})
				preview, e := service.Preview(ctx(t), route, f.actor, A.PreviewRequest{Incarnation: f.id.Incarnation, ExpectedRevision: 2, Command: "retention.set", Input: input})
				if e != nil {
					t.Fatal(e)
				}
				wire, _ := json.Marshal(A.Confirmation{OpID: uuid(t), Incarnation: f.id.Incarnation, ExpectedRevision: 2, Command: "retention.set", Input: input, PreviewHash: preview.PreviewHash})
				intent, e := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "retention.set", BoardID: string(f.id.BoardID)}, contract.AuthenticatedActor{ActorID: f.actor})
				if e != nil {
					t.Fatal(e)
				}
				result := service.Confirm(ctx(t), route, intent)
				if result.Terminal == nil || result.Terminal.Outcome != "committed" {
					t.Fatal("actual native retention confirmation failed")
				}
			}
			raw := parityExport(t, f.s, f.g, f.id, f.actor)
			source, e := V.Read(ctx(t), bytes.NewReader(raw))
			if e != nil {
				t.Fatal(e)
			}
			m, e := source.Manifest()
			if e != nil {
				t.Fatal(e)
			}
			braw, _ := source.Member("board.json")
			b, e := A.DecodeReadable[A.Board](braw)
			if e != nil {
				t.Fatal(e)
			}
			if b.Coverage.HistoryFloor == nil {
				t.Fatal("native pruning omitted floor")
			}
			var original *A.Gap
			for _, g := range b.Coverage.Gaps {
				if g.Origin == m.Source && g.Class == "history" && g.Reason == "expired" && g.ThroughInclusive == *b.Coverage.HistoryFloor && g.BoardRevision == m.Revision {
					copy := g
					original = &copy
				}
			}
			if original == nil {
				t.Fatal("native source did not exercise narrower-key collision")
			}
			if e = f.s.Close(); e != nil {
				t.Fatal(e)
			}
			databaseBefore, e := os.ReadFile(filepath.Join(f.data, "board.sqlite"))
			if e != nil {
				t.Fatal(e)
			}
			sourcePath := filepath.Join(f.input, "source.zip")
			if e = f.n.write(sourcePath, raw); e != nil {
				t.Fatal(e)
			}
			// Use the real wall clock at a later second; no recovery clock/provider is
			// mocked and no installed/native human authority is activated.
			recorded, _ := time.Parse("2006-01-02T15:04:05Z", string(original.RecordedAt))
			if wait := time.Until(recorded.Add(time.Second)); wait > 0 {
				time.Sleep(wait + 10*time.Millisecond)
			}
			q := Request{SourceArchive: sourcePath, Destination: filepath.Join(f.output, "restored"), RequestID: uuid(t), ProtectedRoots: []string{f.data}}
			if got := Restore(ctx(t), q); got.Exit != 0 {
				t.Fatal("later-second physical4 restore", got.Exit)
			}
			if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 0 {
				t.Fatal("physical4 status", got.Exit)
			}
			r, e := f.n.receipt(q.Destination)
			if e != nil {
				t.Fatal(e)
			}
			if r.CreatedAt <= original.RecordedAt {
				t.Fatal("restore did not occur in a later second")
			}
			d := nativeDocuments(t, filepath.Join(q.Destination, "data"), r)
			oldFound, newFound := false, false
			for _, g := range d.Board.Coverage.Gaps {
				if reflect.DeepEqual(g, *original) {
					oldFound = true
				}
				if g.Origin == original.Origin && g.Class == original.Class && g.Reason == original.Reason && g.FromExclusive == nil && g.ThroughInclusive == original.ThroughInclusive && g.BoardRevision == original.BoardRevision && g.RecordedAt == r.CreatedAt {
					newFound = true
				}
			}
			if !oldFound || !newFound {
				t.Fatal("physical4 lost source or restore gap recording time")
			}
			if d.Board.Coverage.HistoryFloor != nil || d.Board.Coverage.ActivityFloor != nil || d.Board.Coverage.OutcomePayloadFloor != nil || len(d.Outcomes.Operations) != 0 {
				t.Fatal("destination current-origin state adopted")
			}
			after, _ := os.ReadFile(sourcePath)
			databaseAfter, _ := os.ReadFile(filepath.Join(f.data, "board.sqlite"))
			if !bytes.Equal(raw, after) || !bytes.Equal(databaseBefore, databaseAfter) {
				t.Fatal("restore changed source archive or database")
			}
		})
	}
}

// Re-encode a synthetic package after a documentary group/lineage change. Native
// roundtrip export below still uses the actual authenticated A02 service.
func parityGroupZIP(t *testing.T) ([]byte, A.Identity, A.Identity) {
	t.Helper()
	p, e := V.Read(ctx(t), bytes.NewReader(fixtureZIP(t)))
	if e != nil {
		t.Fatal(e)
	}
	m, e := p.Manifest()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := p.Member("outcomes.json")
	o, e := A.DecodeReadable[A.Outcomes](raw)
	if e != nil {
		t.Fatal(e)
	}
	empty := A.Identity{BoardID: m.Source.BoardID, Incarnation: uuid(t)}
	lineageOnly := A.Identity{BoardID: m.Source.BoardID, Incarnation: uuid(t)}
	o.SourceArchives = append(o.SourceArchives, A.SourceArchive{Identity: empty, Operations: []A.Operation{}})
	sort.Slice(o.SourceArchives, func(i, j int) bool {
		return string(o.SourceArchives[i].Identity.BoardID)+string(o.SourceArchives[i].Identity.Incarnation) < string(o.SourceArchives[j].Identity.BoardID)+string(o.SourceArchives[j].Identity.Incarnation)
	})
	m.Lineage = append(m.Lineage, A.Lineage{Identity: lineageOnly, ManifestSHA256: A.Digest("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), Revision: 0})
	sort.Slice(m.Lineage, func(i, j int) bool {
		return string(m.Lineage[i].Identity.BoardID)+string(m.Lineage[i].Identity.Incarnation) < string(m.Lineage[j].Identity.BoardID)+string(m.Lineage[j].Identity.Incarnation)
	})
	members := map[string][]byte{}
	for _, name := range p.MemberNames() {
		members[name], _ = p.Member(name)
	}
	members["outcomes.json"], _ = A.Readable(o)
	for i := range m.Inventory {
		entry := &m.Inventory[i]
		entry.ByteLength = A.Number(len(members[entry.Path]))
		entry.SHA256 = sha(members[entry.Path])
	}
	members["manifest.json"], _ = A.Readable(m)
	members["COMPLETE"] = []byte(string(sha(members["manifest.json"])) + "\n")
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range p.MemberNames() {
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
	if _, e = V.Read(ctx(t), bytes.NewReader(out.Bytes())); e != nil {
		t.Fatal(e)
	}
	return out.Bytes(), empty, lineageOnly
}

func TestNativePhysical4EmptyGroupsLineageIndependenceAndNestedRestore(t *testing.T) {
	if schema.Version != 6 || schema.BaseSchemaVersion != 5 {
		t.Fatal("current physical6/base5 provider required")
	}
	n, root := nativeRoot(t)
	input, output := filepath.Join(root, "input"), filepath.Join(root, "output")
	for _, p := range []string{input, output} {
		if e := n.mkdir(p); e != nil {
			t.Fatal(e)
		}
	}
	raw, empty, lineageOnly := parityGroupZIP(t)
	source := filepath.Join(input, "one.zip")
	if e := n.write(source, raw); e != nil {
		t.Fatal(e)
	}
	one := Request{SourceArchive: source, Destination: filepath.Join(output, "one"), RequestID: uuid(t)}
	if got := Restore(ctx(t), one); got.Exit != 0 {
		t.Fatal("first group restore", got.Exit)
	}
	r, e := n.receipt(one.Destination)
	if e != nil {
		t.Fatal(e)
	}
	first := nativeDocuments(t, filepath.Join(one.Destination, "data"), r)
	check := func(d E.OfflineDocuments, absentGroup A.Identity) {
		t.Helper()
		emptyFound := false
		for _, g := range d.Outcomes.SourceArchives {
			if g.Identity == empty {
				emptyFound = true
				if len(g.Operations) != 0 {
					t.Fatal("empty declared group changed")
				}
			}
			if g.Identity == lineageOnly || g.Identity == absentGroup {
				t.Fatal("lineage invented an operation group")
			}
		}
		if !emptyFound {
			t.Fatal("declared empty group lost")
		}
		lineageFound := false
		for _, l := range d.Lineage {
			if l.Identity == lineageOnly {
				lineageFound = true
			}
			if l.Identity == empty {
				t.Fatal("empty group invented lineage")
			}
		}
		if !lineageFound {
			t.Fatal("lineage-only identity lost")
		}
		if len(d.Outcomes.Operations) != 0 {
			t.Fatal("source operations adopted into current ledger")
		}
	}
	check(first, A.Identity{})
	// Explicit synthetic fixture activation supplies only test read/export rights
	// for the next authenticated export; it is not a real bootstrap operation.
	s, e := S.Existing(ctx(t), filepath.Join(one.Destination, "data"), schema.Identity{BoardID: string(r.Destination.BoardID), Incarnation: string(r.Destination.Incarnation)})
	if e != nil {
		t.Fatal(e)
	}
	actor := string(first.Board.Actors[0].ActorID)
	g := parityGrant(t, s, r.Destination, actor, "read", "export")
	next := parityExport(t, s, g, r.Destination, actor)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if got := Status(ctx(t), one.Destination, one.RequestID); got.Exit != 4 {
		t.Fatal("activated source still qualified as prepared offline state")
	}
	nextPath := filepath.Join(input, "two.zip")
	if e = n.write(nextPath, next); e != nil {
		t.Fatal(e)
	}
	two := Request{SourceArchive: nextPath, Destination: filepath.Join(output, "two"), RequestID: uuid(t), ProtectedRoots: []string{one.Destination}}
	if got := Restore(ctx(t), two); got.Exit != 0 {
		t.Fatal("nested group restore", got.Exit)
	}
	secondReceipt, e := n.receipt(two.Destination)
	if e != nil {
		t.Fatal(e)
	}
	second := nativeDocuments(t, filepath.Join(two.Destination, "data"), secondReceipt)
	check(second, r.Destination)
	if !reflect.DeepEqual(first.Outcomes.SourceArchives, second.Outcomes.SourceArchives) {
		t.Fatal("nested restore changed separate source groups")
	}
	found := false
	for _, l := range second.Lineage {
		if l.Identity == r.Destination {
			found = true
		}
	}
	if !found {
		t.Fatal("nested restore omitted immediate source lineage")
	}
	if got := Status(ctx(t), two.Destination, two.RequestID); got.Exit != 0 {
		t.Fatal("nested native status", got.Exit)
	}
}
