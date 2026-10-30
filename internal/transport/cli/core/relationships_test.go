package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func relationshipBoard(t *testing.T, count int) (T.BoardRead, string) {
	b, root := hierarchyBoard(t, count)
	kinds := []A.LinkKind{"blocking", "informational", "related", "reference", "duplicate"}
	ref := func(id A.UUID) A.CardRef {
		return A.CardRef{BoardID: b.Result.Identity.BoardID, Incarnation: b.Result.Identity.Incarnation, CardID: id}
	}
	for i := 1; i <= count; i++ {
		peer := &b.Result.Cards[i]
		peer.Parent = nil
		l := A.Link{LinkID: A.UUID(fmt.Sprintf("80000000-0000-4000-8000-%012d", i)), CardID: A.UUID(root), Kind: kinds[(i-1)%len(kinds)], Target: ref(peer.CardID), Active: true, CreatedAt: "2026-10-01T00:00:00Z", CreatedIn: b.Result.Identity}
		owner := &b.Result.Cards[0]
		if i%2 == 0 {
			l.CardID, l.Target, owner = peer.CardID, ref(A.UUID(root)), peer
		}
		owner.LinkIDs = append(owner.LinkIDs, l.LinkID)
		b.Result.Links = append(b.Result.Links, l)
	}
	return b, root
}

func TestRelationshipsCompleteJSONAndLocalPages(t *testing.T) {
	for _, count := range []int{0, 20, 21, 101} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			b, root := relationshipBoard(t, count)
			wire, err := T.Readable(b)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			r := Runner{Provider: &provider{conn: connection(t, func(req *http.Request) (int, []byte, error) {
				calls++
				if req.Method != "GET" || req.URL.Path != "/api/v1/boards/"+testBoard || req.URL.RawQuery != "" {
					t.Fatal("relationship read changed HTTP scope")
				}
				return 200, wire, nil
			})}}
			raw := run(t, r, args("card", "relationships", root), nil, 0)
			h, err := A.Decode[RelationshipRead](raw)
			if err != nil || len(h.Result.Relationships) != count || !h.Result.Complete || int(h.Result.OwnedCount) != (count+1)/2 || int(h.Result.IncomingCount) != count/2 || h.Result.Revision != b.Result.Revision {
				t.Fatalf("incomplete projection: %v", err)
			}
			if calls != 1 {
				t.Fatal("extra read")
			}
			a := args("card", "relationships", root)[1:]
			var out, diagnostic bytes.Buffer
			if exit := r.Run(context.Background(), a, nil, &out, &diagnostic); exit != 0 || !strings.Contains(out.String(), fmt.Sprintf("of %d; page 1 of", count)) {
				t.Fatal("readable first page")
			}
			if count > 20 {
				out.Reset()
				if exit := r.Run(context.Background(), append(a, "--page", "2"), nil, &out, &diagnostic); exit != 0 || !strings.Contains(out.String(), "Rows 21-") {
					t.Fatal("second page")
				}
			}
			run(t, r, args("card", "relationships", root, "--page", "1"), nil, 1)
			if exit := r.Run(context.Background(), append(a, "--page", "99"), nil, &out, &diagnostic); exit != 1 {
				t.Fatal("out of range")
			}
		})
	}
}

func TestRelationshipsLabelsOwnershipAndExactUnavailablePeers(t *testing.T) {
	b, root := relationshipBoard(t, 10)
	// Reuse a local UUID in an external exact identity: no local peer label may leak.
	foreign := b.Result.Links[0]
	foreign.LinkID = "80000000-0000-4000-8000-000000000999"
	foreign.Target.BoardID = "90000000-0000-4000-8000-000000000001"
	b.Result.Cards[0].LinkIDs = append(b.Result.Cards[0].LinkIDs, foreign.LinkID)
	sort.Slice(b.Result.Cards[0].LinkIDs, func(i, j int) bool { return b.Result.Cards[0].LinkIDs[i] < b.Result.Cards[0].LinkIDs[j] })
	b.Result.Links = append(b.Result.Links, foreign)
	// Inactive retained evidence never contributes a current incident row.
	inactive := foreign
	inactive.LinkID, inactive.Active = "80000000-0000-4000-8000-000000001000", false
	b.Result.Links = append(b.Result.Links, inactive)
	b.Result.Cards[0].LinkIDs = append(b.Result.Cards[0].LinkIDs, inactive.LinkID)
	b.Result.Cards[1].Title = "Peer\x1b[31m\nforged"
	h, err := relationshipRead(b, root)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"blocking/outgoing": "blocked by", "blocking/incoming": "blocks", "informational/outgoing": "informs", "informational/incoming": "informed by", "related/symmetric": "relates to", "reference/outgoing": "references", "reference/incoming": "referenced by", "duplicate/outgoing": "duplicates", "duplicate/incoming": "duplicated by"}
	for _, row := range h.read.Result.Relationships {
		if row.Label != labels[string(row.Kind)+"/"+row.Direction] {
			t.Fatal("wrong reciprocal label")
		}
		if row.Owned != (row.Owner.CardID == A.UUID(root)) {
			t.Fatal("owner changed")
		}
	}
	last := h.read.Result.Relationships[len(h.read.Result.Relationships)-1]
	if last.LinkID != foreign.LinkID || last.Peer != foreign.Target || len(h.read.Result.Relationships) != 11 {
		t.Fatal("foreign or inactive evidence mishandled")
	}
	text := readableRelationships(h, 1)
	if !strings.Contains(text, "unavailable; peer "+refText(foreign.Target)) || strings.Contains(text, "\x1b") || strings.Contains(text, "\nforged") || !strings.Contains(text, "archived: true") {
		t.Fatal("unsafe or incomplete readable labels")
	}
}

func TestRelationshipsSortAndStrictGrammar(t *testing.T) {
	b, root := relationshipBoard(t, 3)
	// Ticket order differs from UUID and transport row order.
	b.Result.Cards[1].Ticket, b.Result.Cards[3].Ticket = b.Result.Cards[3].Ticket, b.Result.Cards[1].Ticket
	sort.Slice(b.Result.Cards, func(i, j int) bool { return b.Result.Cards[i].Ticket < b.Result.Cards[j].Ticket })
	h, err := relationshipRead(b, root)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range h.read.Result.Relationships {
		if int(h.peers[row.Peer].Ticket) != i+2 {
			t.Fatal("not sorted by numeric peer ticket")
		}
	}
	for _, tail := range [][]string{{"--page", "0"}, {"--page", "01"}, {"--page", "-1"}, {"--page", "2", "--page", "3"}, {"--cursor", "opaque"}, {"--input", "-"}} {
		if _, code, _ := parse(append(args("card", "relationships", root)[1:], tail...)); code == "" {
			t.Fatal("invalid relationship read flag accepted")
		}
	}
	if _, code, _ := parse(args("card", "relationships", "1")); code == "" {
		t.Fatal("ticket selector accepted")
	}
	if _, err := relationshipRead(b, "90000000-0000-4000-8000-000000000001"); err == nil {
		t.Fatal("missing selected card accepted")
	}
}

func TestRelationshipsReplacementAliasPreservesEditEnvelope(t *testing.T) {
	_, old := fixtureRecord(t, "card-edit")
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(old, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["input"] = json.RawMessage(`{"relationships":[]}`)
	wire, _ := json.Marshal(envelope)
	q, code, exit := parse(args("card", "relationships-replace", "00000000-0000-4000-8000-00000000000b", "--input", "-"))
	if code != "" || exit != 0 || q.command != "card.edit" || q.method != "PATCH" || !strings.HasSuffix(q.path, "/cards/00000000-0000-4000-8000-00000000000b") {
		t.Fatal("alias route")
	}
	if !relationshipsAlias(wire) {
		t.Fatal("clear refused")
	}
	for _, input := range []string{`{}`, `{"relationships":null}`, `{"relationships":[],"title":"oops"}`, `{"parent":null}`} {
		envelope["input"] = json.RawMessage(input)
		bad, _ := json.Marshal(envelope)
		p := &provider{}
		run(t, Runner{Provider: p}, args("card", "relationships-replace", q.id, "--input", "-"), bad, 2)
		if p.opened != 0 {
			t.Fatal("invalid alias reached provider")
		}
	}
}

func TestRelationshipsReplacementImmutableDispatchAndUnknownNoRetry(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			original, old := fixtureRecord(t, "card-edit")
			var envelope map[string]json.RawMessage
			_ = json.Unmarshal(old, &envelope)
			envelope["input"] = json.RawMessage(`{"relationships":[]}`)
			wire, _ := json.Marshal(envelope)
			store := &memoryEvidence{}
			calls := 0
			c := connection(t, func(req *http.Request) (int, []byte, error) {
				calls++
				if req.Method != "PATCH" || !strings.HasSuffix(req.URL.Path, "/cards/"+original.Route().TargetID) || store.saved != 1 || store.loaded != 1 || store.record.Identity().Command != "card.edit" {
					t.Fatal("alias changed command or skipped private preparation")
				}
				body, _ := io.ReadAll(req.Body)
				frozen, err := T.FreezeMutation(body, original.Route(), contract.AuthenticatedActor{ActorID: testActor})
				if err != nil || frozen.Fingerprint() != store.record.Fingerprint() {
					t.Fatal("sent a different original")
				}
				if unknown {
					return 0, nil, errors.New("synthetic lost response")
				}
				terminal, err := T.Decode[T.Terminal](fixture(t, "command-card-edit.json"))
				if err != nil {
					t.Fatal(err)
				}
				terminal.Fingerprint = A.Digest(store.record.Fingerprint())
				response, err := T.Readable(terminal)
				if err != nil {
					t.Fatal(err)
				}
				return 200, response, nil
			})
			want := 0
			if unknown {
				want = 4
			}
			raw := run(t, Runner{Provider: &provider{conn: c}, Evidence: store}, args("card", "relationships-replace", original.Route().TargetID, "--input", "-"), wire, want)
			if calls != 1 {
				t.Fatal("alias retried")
			}
			if !unknown {
				terminal, err := T.Decode[T.Terminal](raw)
				if err != nil || len(terminal.ResultRefs) != 1 || terminal.ResultRefs[0].Kind != "card" {
					t.Fatal("replacement widened original result")
				}
			}
		})
	}
}
