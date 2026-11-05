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
	"github.com/localswim/localswim/internal/board/policyedit"
	"github.com/localswim/localswim/internal/board/workflow"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

func TestEditedPolicyNativeRepeatedRestorePreservesProjection(t *testing.T) {
	for _, defaultProfile := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "default"}[defaultProfile], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			members := editedPolicyMembers(t, defaultProfile)
			board, err := A.DecodeReadable[A.Board](members["board.json"])
			if err != nil {
				t.Fatal(err)
			}
			history, err := A.DecodeReadable[A.History](members["history.json"])
			if err != nil {
				t.Fatal(err)
			}
			currentPin, historicPin := A.PolicyVersion(3), A.PolicyVersion(2)
			board.Cards[0].WorkflowPolicyVersion = &currentPin
			history.Records[0].State.WorkflowPolicyVersion = &historicPin
			history.Records[0].State.Lane = A.Lane(board.WorkflowPolicies[1].Workflow.Lanes[1].ID)
			members["board.json"], _ = A.Readable(board)
			members["history.json"], _ = A.Readable(history)
			first, err := prepare(ctx, sourcePackage(t, members), testTime, testInc)
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
			store, err := S.Existing(ctx, dir, schema.Identity{BoardID: string(first.board.Identity.BoardID), Incarnation: string(first.board.Identity.Incarnation)})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			revision := mutateRestoredPolicyBoard(t, ctx, store, first)
			projected, err := projectStore(ctx, store, first.board.Identity, revision, testTime)
			closeErr := store.Close()
			if err != nil || closeErr != nil {
				t.Fatal(err, closeErr)
			}
			members = projected.members
			members["manifest.json"], err = A.Readable(projected.manifest)
			if err != nil {
				t.Fatal(err)
			}
			second, err := prepare(ctx, sourcePackage(t, members), testTime, "99999999-9999-9999-9999-999999999998")
			if err != nil {
				t.Fatal(err)
			}
			currentBoard, err := A.DecodeReadable[A.Board](projected.members["board.json"])
			if err != nil {
				t.Fatal(err)
			}
			currentHistory, err := A.DecodeReadable[A.History](projected.members["history.json"])
			if err != nil {
				t.Fatal(err)
			}
			currentActivity, err := A.DecodeReadable[A.Activity](projected.members["activity.json"])
			if err != nil {
				t.Fatal(err)
			}
			currentOutcomes, err := A.DecodeReadable[A.Outcomes](projected.members["outcomes.json"])
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(second.board.WorkflowPolicies, currentBoard.WorkflowPolicies) || !reflect.DeepEqual(second.board.Cards, currentBoard.Cards) || !reflect.DeepEqual(second.board.Approvals, currentBoard.Approvals) || !reflect.DeepEqual(second.history.Records, currentHistory.Records) || !reflect.DeepEqual(second.activity.Events, currentActivity.Events) {
				t.Fatal("second restore changed post-restore policy, card, history, approval or activity")
			}
			if currentBoard.PolicyVersion != 4 || len(currentBoard.Approvals) != 1 || currentBoard.Approvals[0].PolicyVersion != 4 || currentBoard.Cards[0].CurrentApprovalID == nil || currentBoard.Cards[0].WorkflowPolicyVersion == nil || *currentBoard.Cards[0].WorkflowPolicyVersion != 4 {
				t.Fatal("real post-restore policy and card approval activity missing")
			}
			for _, old := range first.history.Records {
				found := false
				for _, current := range second.history.Records {
					if reflect.DeepEqual(old, current) {
						found = true
					}
				}
				if !found {
					t.Fatal("original historical policy pin or retired lane changed")
				}
			}
			found := false
			for _, group := range second.outcomes.SourceArchives {
				if group.Identity == currentOutcomes.Identity {
					want, _ := A.Canonical(currentOutcomes.Operations)
					got, _ := A.Canonical(group.Operations)
					found = bytes.Equal(want, got)
				}
			}
			if !found || len(currentOutcomes.Operations) != 5 {
				t.Fatal("new workflow/card originals or refused retired lane original changed")
			}
			secondDir := hierarchyNativeDir(t)
			if err = second.Construct(ctx, secondDir); err != nil {
				t.Fatal(err)
			}
			if _, err = second.NativeInventory(ctx, secondDir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Uses the real writer and domain services with a newly provisioned synthetic
// human channel. Transport/CLI qualification belongs to the parent journey.
func mutateRestoredPolicyBoard(t *testing.T, ctx context.Context, store *S.Store, first *Prepared) A.Number {
	t.Helper()
	const actor = "80000000-0000-4000-8000-000000000031"
	const grant = "80000000-0000-4000-8000-000000000032"
	const installation = "80000000-0000-4000-8000-000000000033"
	const origin = "https://synthetic.localhost:47443"
	lease, err := store.Writer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		for _, table := range []string{"grant_record", "grant_right", "cli_credential"} {
			var count int
			if err := c.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("restored active authority")
			}
		}
		if _, err := c.ExecContext(ctx, "INSERT INTO actor VALUES(?,?,?,?,?,?)", testInc, actor, "human", "Fresh synthetic reviewer", first.board.Identity.BoardID, testInc); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "INSERT INTO grant_fact VALUES(?,?)", testInc, grant); err != nil {
			return err
		}
		if _, err := c.ExecContext(ctx, "INSERT INTO grant_record VALUES(?,?,?,1)", testInc, grant, actor); err != nil {
			return err
		}
		for _, right := range []string{"approve", "edit", "read", "read-own-outcome", "review-handoff"} {
			if _, err := c.ExecContext(ctx, "INSERT INTO grant_right VALUES(?,?,?)", testInc, grant, right); err != nil {
				return err
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
	revision, cardRevision := uint64(first.board.Revision), uint64(first.board.Cards[0].Revision)
	boardID, cardID := string(first.board.Identity.BoardID), string(first.board.Cards[0].CardID)
	submit := func(command string, input any, want string) {
		t.Helper()
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			t.Fatal(err)
		}
		opID := fmt.Sprintf("%x-%x-%x-%x-%x", nonce[:4], nonce[4:6], nonce[6:8], nonce[8:10], nonce[10:])
		targetID := cardID
		var target any = map[string]any{"cardId": cardID}
		var expectedCard any = cardRevision
		callbacks := workflow.New(g).Callbacks(workflow.Route{BoardID: boardID, Incarnation: string(testInc), CardID: cardID})
		if command == "workflow.edit" {
			targetID = ""
			target = nil
			expectedCard = nil
			callbacks = policyedit.New(g).Callbacks(policyedit.Route{BoardID: boardID, Incarnation: string(testInc)})
		} else if command == "card.edit" {
			callbacks = cards.New(g).Callbacks(cards.Route{BoardID: boardID, Incarnation: string(testInc), CardID: cardID})
		}
		raw, err := json.Marshal(map[string]any{"opId": opID, "incarnation": testInc, "target": target, "expectedRevision": revision, "expectedCardRevision": expectedCard, "input": input})
		if err != nil {
			t.Fatal(err)
		}
		intent, err := contract.Freeze(raw, contract.ResolvedRoute{Command: contract.Command(command), BoardID: boardID, TargetID: targetID}, contract.AuthenticatedActor{ActorID: actor})
		if err != nil {
			t.Fatal(err)
		}
		result := engine.Submit(ctx, intent, callbacks)
		if result.Terminal == nil || result.Terminal.Code != want {
			t.Fatalf("%s expected %s, got terminal=%v unknown=%v", command, want, result.Terminal, result.Unknown)
		}
		if want == "OK" {
			revision++
			if command != "workflow.edit" {
				cardRevision++
			}
		}
	}
	raw, _ := A.Canonical(first.board.Workflow)
	var candidate A.Workflow
	if err := json.Unmarshal(raw, &candidate); err != nil {
		t.Fatal(err)
	}
	candidate.Revision = 4
	candidate.Lanes[0].Label = "Changed after first restore"
	submit("workflow.edit", map[string]any{"expectedPolicyVersion": 3, "workflow": candidate}, "OK")
	submit("card.edit", map[string]any{"laneId": first.board.WorkflowPolicies[1].Workflow.Lanes[1].ID}, "INVALID_INPUT")
	submit("card.edit", map[string]any{"laneId": A.WorkflowLaneForRole(&candidate, "in_progress")}, "OK")
	submit("review-handoff", map[string]any{}, "OK")
	submit("approve", map[string]any{"policyVersion": 4}, "OK")
	return A.Number(revision)
}

func TestEditedDefaultOpaqueHistoryStaysExactAndExportUnavailable(t *testing.T) {
	for _, test := range []struct{ name, raw string }{
		{"no-reserved-key", ` { "legacyValue" : "synthetic" } `},
		{"numeric-reserved-key", ` { "workflowPolicyVersion" : 2, "legacyValue" : "synthetic" } `},
		{"string-reserved-key", ` { "workflowPolicyVersion" : "opaque", "legacyValue" : "synthetic" } `},
		{"null-reserved-key", ` { "workflowPolicyVersion" : null, "legacyValue" : "synthetic" } `},
		{"otherwise-complete-state", ` { "ticket":1, "title":"old", "description":"", "priority":"P3", "lane":"Ready for Work", "archived":false, "currentApprovalId":null, "workflowPolicyVersion":2 } `},
	} {
		t.Run(test.name, func(t *testing.T) { checkEditedDefaultOpaqueHistory(t, test.raw) })
	}
}

func checkEditedDefaultOpaqueHistory(t *testing.T, opaque string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	prepared, err := prepare(ctx, sourcePackage(t, editedPolicyMembers(t, true)), testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	dir := hierarchyNativeDir(t)
	if err = prepared.Construct(ctx, dir); err != nil {
		t.Fatal(err)
	}
	identity := schema.Identity{BoardID: string(prepared.board.Identity.BoardID), Incarnation: string(prepared.board.Identity.Incarnation)}
	store, err := S.Existing(ctx, dir, identity)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.Writer(ctx)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		_, e := c.ExecContext(ctx, "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) VALUES(?,?,?,?,?,?,?,?)", testInc, prepared.board.Identity.BoardID, testInc, prepared.board.Cards[0].CardID, 2, prepared.board.Actors[0].ActorID, testTime, opaque)
		return e
	})
	closeLease, closeStore := lease.Close(), store.Close()
	if err != nil || closeLease != nil || closeStore != nil {
		t.Fatal(err, closeLease, closeStore)
	}
	store, err = S.Existing(ctx, dir, identity)
	if err != nil {
		t.Fatal("legacy opaque history prevented reopen", err)
	}
	defer store.Close()
	reader, err := store.Reader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = reader.Do(ctx, func(c *sql.Conn) error {
		var actual string
		var physicalPin sql.NullInt64
		if e := c.QueryRowContext(ctx, "SELECT state,workflow_policy_version FROM card_revision WHERE incarnation=? AND origin_incarnation=? AND card_revision=2", testInc, testInc).Scan(&actual, &physicalPin); e != nil {
			return e
		}
		if actual != opaque || physicalPin.Valid {
			t.Fatal("opaque history bytes or implicit policy metadata changed")
		}
		return nil
	})
	closeLease = reader.Close()
	if err != nil || closeLease != nil {
		t.Fatal(err, closeLease)
	}
	if projected, err := projectStore(ctx, store, prepared.board.Identity, prepared.board.Revision, testTime); err == nil || projected != nil {
		t.Fatal("opaque history became a lossy export")
	}
}
