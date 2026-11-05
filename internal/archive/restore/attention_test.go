package restore

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func attentionPolicyMembers(t *testing.T, defaultProfile bool) map[string][]byte {
	t.Helper()
	members := editedPolicyMembers(t, defaultProfile)
	board, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	for i := range board.WorkflowPolicies {
		w := board.WorkflowPolicies[i].Workflow
		if w == nil {
			continue
		}
		w.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
		if i != 1 {
			w.Attention.ReadyForReview = []A.Lane{A.WorkflowLaneForRole(w, "review")}
		}
	}
	board.Workflow = board.WorkflowPolicies[len(board.WorkflowPolicies)-1].Workflow
	members["board.json"], err = A.Readable(board)
	if err != nil {
		t.Fatal(err)
	}
	return members
}

func TestAttentionArchivePreservesEveryPolicy(t *testing.T) {
	for _, defaultProfile := range []bool{false, true} {
		members := attentionPolicyMembers(t, defaultProfile)
		original, err := A.DecodeReadable[A.Board](members["board.json"])
		if err != nil {
			t.Fatal(err)
		}
		result, err := prepare(context.Background(), sourcePackage(t, members), testTime, testInc)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.board.WorkflowPolicies, original.WorkflowPolicies) || !reflect.DeepEqual(result.board.Workflow, original.Workflow) {
			t.Fatal("attention policy changed during restore")
		}
		got, _ := A.Canonical(result.board.WorkflowPolicies)
		want, _ := A.Canonical(original.WorkflowPolicies)
		if !bytes.Equal(got, want) {
			t.Fatal("policy bytes changed")
		}
	}
}

func TestAttentionArchiveRejectsInvalidRetainedMapping(t *testing.T) {
	for _, mode := range []string{"foreign", "wrong-role", "overlap", "missing-key", "null"} {
		t.Run(mode, func(t *testing.T) {
			members := attentionPolicyMembers(t, false)
			board, err := A.DecodeReadable[A.Board](members["board.json"])
			if err != nil {
				t.Fatal(err)
			}
			w := board.WorkflowPolicies[0].Workflow
			switch mode {
			case "foreign":
				w.Attention.ReadyForReview = []A.Lane{"ffffffff-ffff-4fff-8fff-ffffffffffff"}
			case "wrong-role":
				w.Attention.Blocked = []A.Lane{A.WorkflowLaneForRole(w, "initial")}
			case "overlap":
				w.Attention.Blocked = []A.Lane{A.WorkflowLaneForRole(w, "review")}
			case "missing-key":
				w.Attention.Blocked = nil
			case "null":
				w.Attention.NeedsTerry = nil
			}
			members["board.json"], _ = A.Readable(board)
			if result, err := V.Read(context.Background(), bytes.NewReader(sourceArchiveBytes(t, members))); err == nil || result != nil {
				t.Fatal("invalid retained mapping accepted")
			}
		})
	}
}
