//go:build windows && amd64

package restore

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/workflow"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// Controlled native service proof: the verifier supplies a fresh synthetic
// principal. This does not stand in for ordinary browser/CLI qualification.
func TestConfiguredNativeRestoreMoveApproveAndSecondRestoreConserveAllRecords(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	source := sourcePackage(t, configuredMembers(t))
	first, err := prepare(ctx, source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	dir := hierarchyNativeDir(t)
	if err = first.Construct(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err = first.NativeInventory(ctx, dir); err != nil {
		t.Fatal(err)
	}
	store, err := S.Existing(ctx, dir, schema.Identity{BoardID: string(first.board.Identity.BoardID), Incarnation: string(testInc)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	const actor = "80000000-0000-4000-8000-000000000011"
	const grant = "80000000-0000-4000-8000-000000000012"
	const installation = "80000000-0000-4000-8000-000000000013"
	const origin = "https://synthetic.localhost:47443"
	lease, err := store.Writer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		// Archived identity and attribution are not live authority.
		for _, table := range []string{"grant_record", "grant_right", "cli_credential"} {
			var n int
			if e := c.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil {
				return e
			} else if n != 0 {
				return fmt.Errorf("restored authority in %s", table)
			}
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO actor VALUES(?,?,?,?,?,?)", testInc, actor, "human", "Fresh synthetic reviewer", first.board.Identity.BoardID, testInc); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO grant_fact VALUES(?,?)", testInc, grant); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO grant_record VALUES(?,?,?,1)", testInc, grant, actor); e != nil {
			return e
		}
		for _, right := range []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"} {
			if _, e := c.ExecContext(ctx, "INSERT INTO grant_right VALUES(?,?,?)", testInc, grant, right); e != nil {
				return e
			}
		}
		return nil
	})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	g, err := grants.New(installation, origin, func(_ context.Context, _ writer.Read, now time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: string(first.board.Identity.BoardID), Incarnation: string(testInc), ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	engine := writer.New(store)
	boardID := string(first.board.Identity.BoardID)
	cardID := string(first.board.Cards[0].CardID)
	revision, cardRevision := uint64(first.board.Revision), uint64(first.board.Cards[0].Revision)
	var refusedIntent []byte
	var refusedCanonical []byte
	submit := func(command string, input map[string]any, want string) writer.Result {
		t.Helper()
		var nonce [16]byte
		if _, e := rand.Read(nonce[:]); e != nil {
			t.Fatal(e)
		}
		op := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:])
		raw, e := json.Marshal(map[string]any{"opId": op, "incarnation": testInc, "target": map[string]any{"cardId": cardID}, "expectedRevision": revision, "expectedCardRevision": cardRevision, "input": input})
		if e != nil {
			t.Fatal(e)
		}
		i, e := contract.Freeze(raw, contract.ResolvedRoute{Command: contract.Command(command), BoardID: boardID, TargetID: cardID}, contract.AuthenticatedActor{ActorID: actor})
		if e != nil {
			t.Fatal(e)
		}
		callbacks := workflow.New(g).Callbacks(workflow.Route{BoardID: boardID, Incarnation: string(testInc), CardID: cardID})
		if command == "card.edit" {
			callbacks = cards.New(g).Callbacks(cards.Route{BoardID: boardID, Incarnation: string(testInc), CardID: cardID})
		}
		r := engine.Submit(ctx, i, callbacks)
		if r.Terminal == nil || string(r.Terminal.Outcome) != want {
			t.Fatalf("%s: expected %s terminal, got %+v", command, want, r)
		}
		if want == "committed" {
			revision++
			cardRevision++
		} else {
			refusedCanonical = i.CanonicalBytes()
		}
		return r
	}
	refusal := submit("card.edit", map[string]any{"laneId": "70000000-0000-4000-8000-000000000099"}, "not_committed")
	if refusal.Terminal.Code != "INVALID_INPUT" {
		t.Fatal("foreign lane refusal", refusal.Terminal.Code)
	}
	submit("card.edit", map[string]any{"laneId": first.board.Workflow.Lanes[1].ID}, "committed")
	submit("card.edit", map[string]any{"laneId": first.board.Workflow.Lanes[2].ID}, "committed")
	submit("review-handoff", map[string]any{}, "committed")
	submit("approve", map[string]any{"policyVersion": 1}, "committed")
	at := A.Timestamp(time.Now().UTC().Format(time.RFC3339))
	projected, err := projectStore(ctx, store, first.board.Identity, A.Number(revision), at)
	if err != nil {
		t.Fatal(err)
	}
	newBoard, err := A.DecodeReadable[A.Board](projected.members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	newHistory, err := A.DecodeReadable[A.History](projected.members["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	newOutcomes, err := A.DecodeReadable[A.Outcomes](projected.members["outcomes.json"])
	if err != nil {
		t.Fatal(err)
	}
	if newBoard.Cards[0].Lane != A.Lane(first.board.Workflow.Lanes[4].ID) || newBoard.Cards[0].CurrentApprovalID == nil || len(newBoard.Approvals) != 1 {
		t.Fatal("configured approval missing")
	}
	for _, op := range newOutcomes.Operations {
		if op.Code == "INVALID_INPUT" && op.Full != nil {
			refusedIntent = append([]byte{}, op.Full.Intent...)
		}
	}
	decoded, decodeErr := contract.DecodeJSON(refusedIntent)
	if decodeErr != nil {
		t.Fatal("attempted foreign lane original invalid", decodeErr)
	}
	canonical, canonicalErr := contract.Canonical(decoded)
	if canonicalErr != nil || !bytes.Equal(canonical, refusedCanonical) || decoded.(map[string]any)["payload"].(map[string]any)["laneId"] != "70000000-0000-4000-8000-000000000099" {
		t.Fatal("attempted foreign lane original missing")
	}
	for _, old := range first.history.Records {
		found := false
		for _, current := range newHistory.Records {
			if current.Origin == old.Origin && current.CardID == old.CardID && current.CardRevision == old.CardRevision {
				found = reflect.DeepEqual(current, old)
			}
		}
		if !found {
			t.Fatal("source historical record changed")
		}
	}
	members := map[string][]byte{}
	for name, raw := range projected.members {
		members[name] = raw
	}
	members["manifest.json"], err = A.Readable(projected.manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepare(ctx, sourcePackage(t, members), at, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second.board.Workflow, first.board.Workflow) || !reflect.DeepEqual(second.board.Cards, newBoard.Cards) || !reflect.DeepEqual(second.board.Approvals, newBoard.Approvals) || !reflect.DeepEqual(second.history.Records, newHistory.Records) {
		t.Fatal("second restore changed configured domain facts")
	}
	found := false
	for _, group := range second.outcomes.SourceArchives {
		if group.Identity == first.board.Identity {
			found = reflect.DeepEqual(group.Operations, newOutcomes.Operations)
		}
	}
	if !found {
		t.Fatal("second restore changed current originals including refused lane")
	}
	secondDir := hierarchyNativeDir(t)
	if err = second.Construct(ctx, secondDir); err != nil {
		t.Fatal(err)
	}
	if _, err = second.NativeInventory(ctx, secondDir); err != nil {
		t.Fatal(err)
	}
}
