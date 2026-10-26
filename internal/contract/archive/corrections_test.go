package archive

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

const correctionID UUID = "00000000-0000-4000-8000-000000000099"
const correctionTime Timestamp = "2026-10-25T00:00:00Z"

func emptyRows() Rows {
	return Rows{[]Actor{}, []Comment{}, []Link{}, []Attachment{}, []Approval{}, []EntityTombstone{}}
}
func requireError(t *testing.T, err error, contains string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("want %q rejection, got %v", contains, err)
	}
}

func TestExactHistoryRejectsUnusedCardTombstone(t *testing.T) {
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
	raw, e := os.ReadFile("../../../fixtures/contracts/transport-envelopes/history-read.json")
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
	tomb := EntityTombstone{Kind: "card", ID: correctionID, CardID: correctionID, CreatedIn: v.Identity, RemovedAt: correctionTime, Reason: "expired"}
	for _, nonempty := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "retained"}[nonempty], func(t *testing.T) {
			rows := emptyRows()
			records := []Snapshot{}
			if nonempty {
				rows = Rows{v.Actors, v.Comments, v.Links, v.Attachments, v.Approvals, v.Tombstones}
				records = v.Records
			}
			if e = ValidateHistoryReferences(v.Identity, v.CardID, v.CardRevision, records, rows); e != nil {
				t.Fatal("baseline", e)
			}
			rows.Tombstones = append(rows.Tombstones, tomb)
			requireError(t, ValidateHistoryReferences(v.Identity, v.CardID, v.CardRevision, records, rows), "extra history tombstone")
		})
	}
}

func TestCompactResultOwnershipSurvivesCompaction(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	outcomes := fixture[Outcomes](t, "package/outcomes.json")
	owner := b.Cards[0].CardID
	other := b.Cards[0]
	other.CardID = correctionID
	other.Ticket = 2
	cards := []Card{b.Cards[0], other}
	for _, item := range []struct {
		command Command
		kind    EntityKind
	}{{"comment.add", "comment"}, {"link.add", "link"}, {"link.remove", "link"}, {"approve", "approval"}, {"attachment.add", "attachment"}} {
		t.Run(string(item.command), func(t *testing.T) {
			for _, removed := range []bool{false, true} {
				if removed && item.kind == "approval" {
					continue
				}
				t.Run(map[bool]string{false: "full-row", true: "tombstone"}[removed], func(t *testing.T) {
					op := outcomes.Operations[0]
					op.Command = item.command
					op.Full = nil
					op.Removal = &Removal{Reason: "expired", RemovedAt: correctionTime, BoardRevision: 5}
					op.ByteFacts = []ByteFact{}
					if item.kind == "attachment" {
						op.ByteFacts = outcomes.Operations[0].ByteFacts
					}
					rev := Number(*op.CommittedCardRevision)
					op.ResultRefs = []FactRef{{Kind: "card", ID: owner, Revision: &rev}, {Kind: FactKind(item.kind), ID: correctionID}}
					sort.Slice(op.ResultRefs, func(i, j int) bool { a, _ := rowkey(op.ResultRefs[i]); b, _ := rowkey(op.ResultRefs[j]); return a < b })
					rowsFor := func(rowOwner UUID) Rows {
						rows := emptyRows()
						rows.Actors = b.Actors
						if removed {
							var actor *UUID
							if item.kind == "comment" {
								actor = &b.Actors[0].ActorID
							}
							when := correctionTime
							rows.Tombstones = []EntityTombstone{{Kind: item.kind, ID: correctionID, CardID: rowOwner, CreatedIn: b.Identity, ActorID: actor, CreatedAt: &when, RemovedAt: correctionTime, Reason: "expired"}}
							return rows
						}
						switch item.kind {
						case "comment":
							row := b.Comments[0]
							row.CommentID = correctionID
							row.CardID = rowOwner
							rows.Comments = []Comment{row}
						case "link":
							rows.Links = []Link{{LinkID: correctionID, CardID: rowOwner, Kind: "informational", Target: CardRef{b.Identity.BoardID, b.Identity.Incarnation, owner}, Active: false, CreatedAt: correctionTime, CreatedIn: b.Identity}}
						case "approval":
							rows.Approvals = []Approval{{ApprovalID: correctionID, CardID: rowOwner, ActorID: b.Actors[0].ActorID, ReviewedCardRevision: 1, ReviewedBoardRevision: 1, PolicyVersion: 1, CreatedAt: correctionTime, CreatedIn: b.Identity}}
						case "attachment":
							fact := op.ByteFacts[0]
							rows.Attachments = []Attachment{{AttachmentID: correctionID, CardID: rowOwner, Name: "synthetic", MediaLabel: "application/octet-stream", SHA256: fact.SHA256, ByteLength: fact.ByteLength, CreatedAt: correctionTime, CreatedIn: b.Identity}}
						}
						return rows
					}
					if e := ValidateOperationReferences(op, b.Identity.BoardID, cards, rowsFor(owner)); e != nil {
						t.Fatal("legitimate compact reference", e)
					}
					requireError(t, ValidateOperationReferences(op, b.Identity.BoardID, cards, rowsFor(other.CardID)), "reference ownership")
				})
			}
		})
	}
}

func refreshProjectionInventory(t *testing.T, m Manifest, b Board, h History, a Activity, o Outcomes) Manifest {
	t.Helper()
	m.Inventory = append([]Entry{}, m.Inventory...)
	for path, doc := range map[string]any{"board.json": b, "history.json": h, "activity.json": a, "outcomes.json": o} {
		raw, e := Readable(doc)
		if e != nil {
			t.Fatal(e)
		}
		for i := range m.Inventory {
			if m.Inventory[i].Path == path {
				m.Inventory[i].ByteLength = Number(len(raw))
				m.Inventory[i].SHA256 = hash(raw)
			}
		}
	}
	return m
}
func TestCurrentInternalLinkRequiresFullTarget(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	h := fixture[History](t, "package/history.json")
	a := fixture[Activity](t, "package/activity.json")
	o := fixture[Outcomes](t, "package/outcomes.json")
	m := fixture[Manifest](t, "package/manifest.json")
	b.Links = []Link{{LinkID: correctionID, CardID: b.Cards[0].CardID, Kind: "blocking", Target: CardRef{b.Identity.BoardID, b.Identity.Incarnation, correctionID}, Active: true, CreatedAt: correctionTime, CreatedIn: b.Identity}}
	b.Tombstones = []EntityTombstone{{Kind: "card", ID: correctionID, CardID: correctionID, CreatedIn: b.Identity, RemovedAt: correctionTime, Reason: "expired"}}
	b.Cards[0].LinkIDs = []UUID{correctionID}
	for _, active := range []bool{true, false} {
		b.Links[0].Active = active
		mm := refreshProjectionInventory(t, m, b, h, a, o)
		_, e := ValidateDocuments(mm, b, h, a, o)
		requireError(t, e, "unresolved card reference")
	}
	// A retained historical-only link may retain an allowed card tombstone. Its
	// presence outside current card references must not become a new live target.
	b.Cards[0].LinkIDs = []UUID{}
	b.Links[0].Active = false
	mm := refreshProjectionInventory(t, m, b, h, a, o)
	if _, e := ValidateDocuments(mm, b, h, a, o); e != nil {
		t.Fatal("historical-only link", e)
	}
	// A current link to a complete internal card remains structurally valid.
	b.Tombstones = []EntityTombstone{}
	target := b.Cards[0]
	target.CardID = correctionID
	target.Ticket = 2
	target.CommentIDs = []UUID{}
	target.AttachmentIDs = []UUID{}
	target.LinkIDs = []UUID{}
	b.Cards = append(b.Cards, target)
	b.NextTicket = 3
	b.Cards[0].LinkIDs = []UUID{correctionID}
	b.Links[0].Active = true
	mm = refreshProjectionInventory(t, m, b, h, a, o)
	if _, e := ValidateDocuments(mm, b, h, a, o); e != nil {
		t.Fatal("complete current target", e)
	}
}

func TestPreviewRemovalOnlyDomainSets(t *testing.T) {
	zero := ByteFact{SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", ByteLength: 0}
	one := ByteFact{SHA256: "4ac9c84a00cf5c569835430fd5e4cb1a782d70806ee741822d600498b7460671", ByteLength: 10}
	x := PreviewBytes{DomainBefore: []ByteFact{}, DomainAfter: []ByteFact{zero}, ExportPinned: []ByteFact{}, Reclaimable: []ByteFact{}}
	requireError(t, Validate(x), "removal-only subset")
	// Concurrent exports may protect a digest outside this domain snapshot.
	x.DomainAfter = []ByteFact{}
	x.ExportPinned = []ByteFact{zero}
	if e := Validate(x); e != nil {
		t.Fatal("independent export pins", e)
	}
	x.DomainBefore = []ByteFact{one, zero}
	x.DomainBeforeBytes = 10
	x.DomainAfter = []ByteFact{zero}
	x.Reclaimable = []ByteFact{one}
	x.ReclaimableBytes = 10
	if e := Validate(x); e != nil {
		t.Fatal("removal with retained pin", e)
	}
	x.DomainAfter = []ByteFact{one, zero}
	x.DomainAfterBytes = 10
	x.Reclaimable = []ByteFact{}
	x.ReclaimableBytes = 0
	if e := Validate(x); e != nil {
		t.Fatal("unchanged domain", e)
	}
	x.DomainAfter[0].ByteLength = 9
	x.DomainAfterBytes = 9
	requireError(t, Validate(x), "BYTE_LENGTH_CONFLICT")
}

func TestEncoderArrayScalarValidation(t *testing.T) {
	invalid := string([]byte{0xff})
	bad := []any{[1]string{invalid}, map[string]any{"value": [1]string{invalid}}, [1]any{map[string]any{"nested": [1]string{invalid}}}, [1]float64{1}}
	for i, v := range bad {
		for name, encode := range map[string]func(any) ([]byte, error){"C": Canonical, "R": Readable} {
			if _, e := encode(v); e == nil {
				t.Fatalf("%s accepted invalid array case %d", name, i)
			}
		}
	}
	input := [2]string{"雪😀", "\r\n"}
	c, e := Canonical(input)
	if e != nil || string(c) != `["雪😀","\u000d\u000a"]` {
		t.Fatalf("valid array C: %s %v", c, e)
	}
	r, e := Readable(map[string]any{"value": input})
	if e != nil || !strings.Contains(string(r), "雪😀") {
		t.Fatal("valid nested array", e)
	}
}

func TestOperationSeamValidatesSuppliedScope(t *testing.T) {
	b := fixture[Board](t, "package/board.json")
	o := fixture[Outcomes](t, "package/outcomes.json")
	op := o.Operations[0]
	rows := func() Rows { return Rows{b.Actors, b.Comments, b.Links, b.Attachments, b.Approvals, b.Tombstones} }
	if e := ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, rows()); e != nil {
		t.Fatal("baseline", e)
	}
	t.Run("board", func(t *testing.T) {
		if e := ValidateOperationReferences(op, "invalid", b.Cards, rows()); e == nil {
			t.Fatal("malformed board")
		}
	})
	t.Run("valid-foreign-board", func(t *testing.T) {
		requireError(t, ValidateOperationReferences(op, correctionID, b.Cards, rows()), "operation board scope")
	})
	t.Run("card", func(t *testing.T) {
		if e := ValidateOperationReferences(op, b.Identity.BoardID, []Card{{CardID: b.Cards[0].CardID}}, rows()); e == nil {
			t.Fatal("malformed card")
		}
	})
	t.Run("foreign-row", func(t *testing.T) {
		r := rows()
		r.Attachments = append([]Attachment{}, r.Attachments...)
		r.Attachments[0].CreatedIn.BoardID = correctionID
		requireError(t, ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, r), "attachment provenance board")
	})
	t.Run("foreign-card", func(t *testing.T) {
		cards := append([]Card{}, b.Cards...)
		cards[0].CreatedIn.BoardID = correctionID
		requireError(t, ValidateOperationReferences(op, b.Identity.BoardID, cards, rows()), "card provenance board")
	})
	t.Run("collision", func(t *testing.T) {
		r := rows()
		r.Tombstones = []EntityTombstone{{Kind: "card", ID: b.Cards[0].CardID, CardID: b.Cards[0].CardID, CreatedIn: b.Identity, RemovedAt: correctionTime, Reason: "expired"}}
		requireError(t, ValidateOperationReferences(op, b.Identity.BoardID, b.Cards, r), "card tombstone collision")
	})
	t.Run("actor", func(t *testing.T) {
		copy := op
		copy.ActorID = correctionID
		copy.Full = nil
		copy.Removal = &Removal{Reason: "expired", RemovedAt: correctionTime, BoardRevision: 5}
		requireError(t, ValidateOperationReferences(copy, b.Identity.BoardID, b.Cards, rows()), "operation actor")
	})
	t.Run("source-incarnation", func(t *testing.T) {
		source := o.SourceArchives[0].Operations[0]
		if source.Origin.Incarnation == b.Identity.Incarnation {
			t.Fatal("fixture needs historical incarnation")
		}
		if e := ValidateOperationReferences(source, b.Identity.BoardID, b.Cards, rows()); e != nil {
			t.Fatal("legitimate historical source", e)
		}
	})
}
