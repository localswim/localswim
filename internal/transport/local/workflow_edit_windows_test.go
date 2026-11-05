//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestWorkflowEditHTTPPreviewHistoryAndSemanticCatalog(t *testing.T) {
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	card := catalogCreate(t, r, info.Token)
	path := r.boardPath() + "cards/" + card
	term(t, request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"lane": "In Progress"}), info.Token, nil, nil), "committed")
	term(t, request(t, r, "POST", path+"/review-handoff", mutationWire(t, r, card, 2, 2, map[string]any{}), info.Token, nil, nil), "committed")
	before := catalogSnapshot(t, r, 3, 1)
	candidate := policyedit.DefaultDefinition(A.UUID(uuid()), 2)
	candidate.Lanes[2].Label = "Verification"
	previewRequest := T.WorkflowPreviewRequest{APIVersion: 1, BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation), ExpectedRevision: 3, ExpectedPolicyVersion: 1, Workflow: candidate}
	wire, _ := json.Marshal(previewRequest)
	previewResponse := request(t, r, "POST", r.boardPath()+"workflow/preview", wire, info.Token, nil, nil)
	preview, err := T.Decode[T.WorkflowPreview](previewResponse.b)
	if err != nil || previewResponse.status != 200 || !preview.WouldChange || preview.CurrentWorkflow != nil || !reflect.DeepEqual(preview.Candidate, candidate) || len(preview.Changes.Renamed) != 1 {
		t.Fatalf("preview mismatch status %d error %v", previewResponse.status, err)
	}
	if after := catalogSnapshot(t, r, 3, 1); !reflect.DeepEqual(before, after) {
		t.Fatal("preview changed committed state")
	}
	edit := mutationWire(t, r, "", 3, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate})
	committed := term(t, request(t, r, "PATCH", r.boardPath()+"workflow", edit, info.Token, nil, nil), "committed")
	if committed.Command != "workflow.edit" || committed.CardRevision != nil {
		t.Fatal("workflow edit changed card revision")
	}
	after := catalogSnapshot(t, r, 4, 1)
	if !reflect.DeepEqual(before.LastCardActivity, after.LastCardActivity) {
		t.Fatal("policy edit changed committed-card activity")
	}
	read := request(t, r, "GET", path, nil, info.Token, nil, nil)
	view, err := T.Decode[T.CardRead](read.b)
	if err != nil || view.Result.Card.Revision != 3 || view.Result.Card.Lane != "Ready for Review" || view.Result.Card.WorkflowPolicyVersion != nil || len(view.Result.WorkflowPolicies) != 2 {
		t.Fatalf("policy edit repinned or changed card, error %v", err)
	}
	stale := request(t, r, "POST", r.boardPath()+"workflow/preview", wire, info.Token, nil, nil)
	if stale.status != 409 {
		t.Fatal("stale preview did not refuse")
	}
	// A replay consults the original receipt without replacing it with current policy.
	replayed := request(t, r, "PATCH", r.boardPath()+"workflow", edit, info.Token, nil, nil)
	if !bytes.Equal(replayed.b, mustReadable(t, committed)) {
		t.Fatal("retained workflow terminal changed")
	}
	if _, err = r.InspectDefinition(context.Background(), "cli"); err != nil {
		t.Fatal("edited policy inspection unavailable")
	}
}

func mustReadable(t *testing.T, value any) []byte {
	t.Helper()
	b, err := T.Readable(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConfiguredInitRejectsEditedProfilesBeforeAllocation(t *testing.T) {
	for _, w := range []*A.Workflow{func() *A.Workflow { w := configuredFixture(); w.Revision = 2; return w }(), func() *A.Workflow { w := policyedit.DefaultDefinition(A.UUID(uuid()), 2); return &w }()} {
		allocated := false
		_, err := initializeBoardWithRoot(context.Background(), "unused", w, func(string) (*WorkspacePath, error) { allocated = true; return nil, nil })
		if err != ErrInitializationRefused || allocated {
			t.Fatal("edited workflow reached initialization allocation")
		}
	}
}

func TestWorkflowConfiguredReviewCatalog(t *testing.T) {
	root := filepath.Join(testRoot(t), "configured-review")
	w := configuredFixture()
	engine, err := openEngine(t.Context(), EngineOptions{DataDir: root, Port: freePort(t), Workflow: w}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Start(t.Context(), Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cookie := bootstrapTest(t, r)
	created := term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0, map[string]any{"title": "Configured review", "description": "Synthetic"}), "", cookie, nil), "committed")
	var card string
	for _, ref := range created.ResultRefs {
		if ref.Kind == "card" {
			card = string(ref.ID)
		}
	}
	if card == "" {
		t.Fatal("created card reference missing")
	}
	path := r.boardPath() + "cards/" + card
	term(t, request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"laneId": w.Lanes[1].ID}), "", cookie, nil), "committed")
	term(t, request(t, r, "POST", path+"/review-handoff", mutationWire(t, r, card, 2, 2, map[string]any{}), "", cookie, nil), "committed")
	before := catalogSnapshot(t, r, 3, 1)
	w.Revision = 2
	w.Lanes[2].Label = "Independent verification"
	term(t, request(t, r, "PATCH", r.boardPath()+"workflow", mutationWire(t, r, "", 3, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": w}), "", cookie, nil), "committed")
	after := catalogSnapshot(t, r, 4, 1)
	if !reflect.DeepEqual(before.LastCardActivity, after.LastCardActivity) {
		t.Fatal("configured workflow-only edit changed card activity")
	}
}

func TestWorkflowHistoryOpaquePinCollisionStaysUnavailable(t *testing.T) {
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	card := catalogCreate(t, r, info.Token)
	path := r.boardPath() + "cards/" + card
	term(t, request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"description": "Second revision"}), info.Token, nil, nil), "committed")
	if got := request(t, r, "GET", path+"/history", nil, info.Token, nil, nil); got.status != 200 {
		t.Fatal("baseline history unavailable")
	}
	// Model an opaque default1 snapshot accepted by old physical storage but
	// unrepresentable by its full DTO. A newly recognized key cannot repin it.
	lease, err := r.store.Writer(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(t.Context(), func(c *sql.Conn) error {
		var state string
		if err := c.QueryRowContext(t.Context(), "SELECT state FROM card_revision WHERE incarnation=? AND card_id=?", info.Incarnation, card).Scan(&state); err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(state), &fields); err != nil {
			return err
		}
		fields["workflowPolicyVersion"] = json.RawMessage(`1`)
		raw, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		_, err = c.ExecContext(t.Context(), "INSERT INTO card_revision(incarnation,origin_board,origin_incarnation,card_id,card_revision,actor_id,committed_at,state) SELECT incarnation,origin_board,?,card_id,card_revision,actor_id,committed_at,? FROM card_revision WHERE incarnation=? AND card_id=? AND workflow_policy_version IS NULL LIMIT 1", uuid(), string(raw), info.Incarnation, card)
		return err
	})
	closeErr := lease.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("synthetic legacy snapshot injection failed: %v / %v", err, closeErr)
	}
	if got := request(t, r, "GET", path+"/history", nil, info.Token, nil, nil); got.status != 503 {
		t.Fatal("legacy opaque pin became a readable modern state")
	}
	candidate := policyedit.DefaultDefinition(A.UUID(uuid()), 2)
	candidate.Lanes[0].Label = "Queue"
	term(t, request(t, r, "PATCH", r.boardPath()+"workflow", mutationWire(t, r, "", 2, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": candidate}), info.Token, nil, nil), "committed")
	if got := request(t, r, "GET", path+"/history", nil, info.Token, nil, nil); got.status != 503 {
		t.Fatal("policy edit reinterpreted legacy opaque history")
	}
}
