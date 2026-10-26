package archive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
)

const fixtures = "../../../fixtures/contracts/archive-recovery"

func read(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(fixtures, p))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func fixture[T any](t *testing.T, p string) T {
	t.Helper()
	v, e := DecodeReadable[T](read(t, p))
	if e != nil {
		t.Fatalf("%s: %v", p, e)
	}
	return v
}
func TestAcceptedFixtures(t *testing.T) {
	m := fixture[Manifest](t, "package/manifest.json")
	b := fixture[Board](t, "package/board.json")
	h := fixture[History](t, "package/history.json")
	a := fixture[Activity](t, "package/activity.json")
	o := fixture[Outcomes](t, "package/outcomes.json")
	pins, e := ValidateDocuments(m, b, h, a, o)
	if e != nil {
		t.Fatal(e)
	}
	if len(pins) != 2 {
		t.Fatalf("pins %d", len(pins))
	}
	for _, n := range []string{"preview-clear.json", "preview-retention.json"} {
		fixture[Preview](t, n)
	}
	fixture[PreviewRequest](t, "preview-request.json")
	fixture[Confirmation](t, "confirm-clear.json")
	for _, n := range []string{"restore-receipt.json", "restore-receipt-preparing.json", "restore-receipt-prepared.json", "restore-receipt-failed.json"} {
		fixture[Receipt](t, n)
	}
	fixture[Status](t, "status-confirmed.json")
	fixture[Status](t, "status-unknown.json")
	bo := fixture[Board](t, "prepared-target/board.json")
	ho := fixture[History](t, "prepared-target/history.json")
	ao := fixture[Activity](t, "prepared-target/activity.json")
	oo := fixture[Outcomes](t, "prepared-target/outcomes.json")
	target := m
	target.Source = bo.Identity
	target.Coverage = bo.Coverage
	target.Inventory = fixture[Receipt](t, "restore-receipt.json").TargetInventory.Entries
	if _, e = ValidateDocuments(target, bo, ho, ao, oo); e != nil {
		t.Fatal("prepared", e)
	}
	for _, n := range []string{"negative/semantic/broken-committed-reference.json", "negative/semantic/byte-facts-omitted.json", "negative/semantic/byte-facts-substituted.json", "negative/semantic/byte-facts-conflicting-length.json", "negative/semantic/nonbyte-command-facts.json"} {
		t.Run(n, func(t *testing.T) {
			op, e := Decode[Operation](read(t, n))
			if e == nil {
				e = ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, Rows{b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals, b.Tombstones})
			}
			if e == nil {
				t.Fatal("negative accepted")
			}
		})
	}
}
func TestStrictNegativeFixtures(t *testing.T) {
	for _, n := range []string{"duplicate-key", "invalid-utf8", "unpaired-surrogate", "overflow", "fraction", "negative", "unknown-field", "absent-null", "null-not-input"} {
		t.Run(n, func(t *testing.T) {
			if _, e := Decode[Confirmation](read(t, "negative/"+n+".bin")); e == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, e := Decode[struct {
		ThroughTime Timestamp `json:"throughTime"`
	}](read(t, "negative/bad-time.bin")); e == nil {
		t.Fatal("bad time accepted")
	}
}
func TestCanonicalVectors(t *testing.T) {
	var vectors struct {
		Vectors []struct {
			Path       string          `json:"path"`
			SHA256     Digest          `json:"sha256"`
			ByteLength Number          `json:"byteLength"`
			Value      json.RawMessage `json:"value"`
		} `json:"vectors"`
	}
	if e := json.Unmarshal(read(t, "vectors.json"), &vectors); e != nil {
		t.Fatal(e)
	}
	for _, v := range vectors.Vectors {
		b := read(t, v.Path)
		if hash(b) != v.SHA256 || len(b) != int(v.ByteLength) {
			t.Fatal(v.Path)
		}
		if v.Value != nil {
			x, e := contract.DecodeJSON(v.Value)
			if e != nil {
				t.Fatal(e)
			}
			got, e := Canonical(x)
			if e != nil || !bytes.Equal(got, b) {
				t.Fatal(v.Path, e)
			}
		}
	}
	m := fixture[Manifest](t, "package/manifest.json")
	raw, e := Readable(m)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(append([]byte(hash(raw)), '\n'), read(t, "package/COMPLETE")) {
		t.Fatal("COMPLETE")
	}
	for _, c := range []struct {
		Path string
		Fact ByteFact
	}{{"negative/tampered-blob.bin", ByteFact{"4ac9c84a00cf5c569835430fd5e4cb1a782d70806ee741822d600498b7460671", 10}}, {"negative/truncated-blob.bin", ByteFact{"4ac9c84a00cf5c569835430fd5e4cb1a782d70806ee741822d600498b7460671", 10}}} {
		if ValidateBytes(read(t, c.Path), c.Fact) == nil {
			t.Fatal("raw negative digest")
		}
	}
}

func TestHistorySharedProjection(t *testing.T) {
	type view struct {
		Identity     Identity          `json:"identity"`
		CardID       UUID              `json:"cardId"`
		CardRevision Positive          `json:"cardRevision"`
		Records      []Snapshot        `json:"records"`
		Actors       []Actor           `json:"actors"`
		Comments     []Comment         `json:"comments"`
		Links        []Link            `json:"links"`
		Attachments  []Attachment      `json:"attachments"`
		Approvals    []Approval        `json:"approvals"`
		Tombstones   []EntityTombstone `json:"tombstones"`
	}
	for _, n := range []string{"history-read.json", "negative-history-extra-attachment.bin", "negative-history-missing-attachment.bin"} {
		raw, e := os.ReadFile("../../../fixtures/contracts/transport-envelopes/" + n)
		if e != nil {
			t.Fatal(e)
		}
		var envelope struct {
			Result view `json:"result"`
		}
		if e = json.Unmarshal(raw, &envelope); e != nil {
			t.Fatal(e)
		}
		v := envelope.Result
		e = ValidateHistoryReferences(v.Identity, v.CardID, v.CardRevision, v.Records, Rows{v.Actors, v.Comments, v.Links, v.Attachments, v.Approvals, v.Tombstones})
		if (e == nil) != (n == "history-read.json") {
			t.Fatal(n, e)
		}
	}
}
func TestReferenceOwnershipAndAvailability(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	h := fixture[History](t, "package/history.json")
	o := fixture[Outcomes](t, "package/outcomes.json")
	rows := func() Rows { return Rows{b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals, b.Tombstones} }
	for _, op := range o.Operations {
		if op.TerminalClass == "NOT_COMMITTED" {
			if e := ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, rows()); e != nil {
				t.Fatal("unresolved attempted reference", e)
			}
		}
	}
	op := o.Operations[0]
	old := b.Attachments[0].CardID
	b.Attachments[0].CardID = "00000000-0000-4000-8000-000000000099"
	if e := ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, rows()); e == nil {
		t.Fatal("attachment ownership")
	}
	b.Attachments[0].CardID = old
	saved := b.Attachments
	b.Attachments = []Attachment{}
	if e := ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, rows()); e == nil {
		t.Fatal("full committed metadata missing")
	}
	b.Attachments = saved
	op.ResultRefs[0].Revision = new(Number)
	if e := Validate(op); e == nil {
		t.Fatal("immutable result ref revision")
	}
	r, e := newRegistry(rows())
	if e != nil {
		t.Fatal(e)
	}
	current := map[UUID]Positive{b.Cards[0].CardID: b.Cards[0].Revision}
	if e = r.history(h.Records, current, false); e != nil {
		t.Fatal(e)
	}
	for _, s := range h.Records {
		if s.State != nil && len(s.References.AttachmentIDs) > 0 {
			id := s.References.AttachmentIDs[0]
			owner := r.full["attachment"][id]
			delete(r.full["attachment"], id)
			r.dead["attachment"][id] = owner
			if e = r.history([]Snapshot{s}, current, false); e == nil {
				t.Fatal("full snapshot accepted tombstone")
			}
			s.State = nil
			s.Removal = &Removal{Reason: "expired", RemovedAt: "2026-10-25T00:00:00Z", BoardRevision: 5}
			if e = r.history([]Snapshot{s}, current, false); e != nil {
				t.Fatal("removed snapshot rejected tombstone", e)
			}
		}
	}
}

func TestGeneratedArtifacts(t *testing.T) {
	a, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	b, e := GeneratedFiles()
	if e != nil {
		t.Fatal(e)
	}
	for p, want := range a {
		if !bytes.HasSuffix(want, []byte("\n")) || bytes.HasSuffix(want, []byte("\n\n")) {
			t.Fatal("generated output requires exactly one final LF", p)
		}
		if !bytes.Equal(want, b[p]) {
			t.Fatal("nondeterministic", p)
		}
		got, e := os.ReadFile("../../../" + p)
		if e != nil {
			t.Fatal("run the aggregate contractgen before tests:", p, e)
		}
		if !bytes.Equal(got, want) {
			t.Fatal("generated drift", p)
		}
	}
}
func TestScalarAndFieldRules(t *testing.T) {
	for _, b := range []string{`{"boardId":"00000000-0000-0000-0000-000000000000","incarnation":"00000000-0000-4000-8000-000000000001"}`, `{"boardId":"00000000-0000-4000-8000-00000000000A","incarnation":"00000000-0000-4000-8000-000000000001"}`, `{"boardId":"00000000-0000-4000-8000-00000000000a"}`, `{"boardId":"00000000-0000-4000-8000-00000000000a","incarnation":null}`} {
		if _, e := Decode[Identity]([]byte(b)); e == nil {
			t.Fatal("bad identity")
		}
	}
	for _, s := range []string{`9007199254740992`, `1e0`, `-0`, `1.0`, `01`, `null`, `true`} {
		if _, e := Decode[Number]([]byte(s)); e == nil {
			t.Fatal(s)
		}
	}
	for _, s := range []string{`"0000-01-01T00:00:00Z"`, `"2025-02-29T00:00:00Z"`, `"2026-01-01T00:00:60Z"`, `"2026-01-01T00:00:00.0Z"`} {
		if _, e := Decode[Timestamp]([]byte(s)); e == nil {
			t.Fatal(s)
		}
	}
	if _, e := Decode[Timestamp]([]byte(`"0001-01-01T00:00:00Z"`)); e != nil {
		t.Fatal(e)
	}
	if _, e := Decode[Number]([]byte(`9007199254740991`)); e != nil {
		t.Fatal(e)
	}
	x := map[string]any{"z": "é😀\u2028<>&/\r\n", "a": json.Number("0")}
	c, e := Canonical(x)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(c, []byte(`\u000d\u000a`)) || bytes.Contains(c, []byte(`\u003c`)) {
		t.Fatal(string(c))
	}
	bad := Actor{Label: string([]byte{0xff})}
	if e = Validate(bad); e == nil {
		t.Fatal("repaired Unicode")
	}
	p := fixture[Preview](t, "preview-clear.json")
	p.PreviewedAt = "2026-10-02T00:00:01Z"
	if Validate(p) == nil {
		t.Fatal("preview time drift")
	}
	b := fixture[Board](t, "package/board.json")
	b.Cards[0].CommentIDs = nil
	if Validate(b) == nil {
		t.Fatal("nil slice")
	}
	if _, e := Decode[Identity]([]byte(strings.Repeat("[", 33) + strings.Repeat("]", 33))); e == nil {
		t.Fatal("depth")
	}
}
