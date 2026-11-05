//go:build windows && amd64

package local

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestAttentionHTTPCurrentReadAndExplicitDisable(t *testing.T) {
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	path := r.boardPath() + "attention"
	before := request(t, r, "GET", r.boardPath()[:len(r.boardPath())-1], nil, info.Token, nil, nil)
	response := request(t, r, "GET", path, nil, info.Token, nil, nil)
	current, err := T.Decode[T.BoardAttention](response.b)
	if err != nil || response.status != 200 || current.AttentionContext.Source != "implicit" || current.Attention.ReadyForReview.Value == nil || *current.Attention.ReadyForReview.Value != 0 || current.Attention.Blocked.State != "not_configured" {
		t.Fatalf("implicit attention response status %d error %v", response.status, err)
	}
	after := request(t, r, "GET", r.boardPath()[:len(r.boardPath())-1], nil, info.Token, nil, nil)
	if !bytes.Equal(before.b, after.b) {
		t.Fatal("attention read changed board")
	}
	w := policyedit.DefaultDefinition(A.UUID(uuid()), 2)
	w.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
	payload := mutationWire(t, r, "", 0, 0, map[string]any{"expectedPolicyVersion": 1, "workflow": w})
	term(t, request(t, r, "PATCH", r.boardPath()+"workflow", payload, info.Token, nil, nil), "committed")
	response = request(t, r, "GET", path, nil, info.Token, nil, nil)
	current, err = T.Decode[T.BoardAttention](response.b)
	if err != nil || current.Revision != 1 || current.AttentionContext.Source != "explicit" || current.Attention.ReadyForReview.State != "not_configured" || current.Attention.ReadyForReview.Value != nil {
		t.Fatalf("explicit attention response %s error %v", response.b, err)
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		if got := request(t, r, method, path, []byte(`{}`), info.Token, nil, nil); got.status != 405 {
			t.Fatal("attention mutation admitted", method, got.status)
		}
	}
	if got := request(t, r, "GET", path+"?extra=1", nil, info.Token, nil, nil); got.status != 400 {
		t.Fatal("attention query admitted")
	}
	if got := request(t, r, "GET", path, json.RawMessage(`{}`), info.Token, nil, nil); got.status != 400 {
		t.Fatal("attention body admitted")
	}
}
