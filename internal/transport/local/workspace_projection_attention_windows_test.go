//go:build windows && amd64

package local

import (
	"context"
	"fmt"
	"github.com/localswim/localswim/internal/board/policyedit"
	A "github.com/localswim/localswim/internal/contract/archive"
	"sync"
	"testing"
)

func TestCatalogAttentionConcurrentPolicySnapshot(t *testing.T) {
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	failures := make(chan error, 2)
	var readers sync.WaitGroup
	for i := 0; i < 2; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				got, err := r.CatalogSnapshot(context.Background(), "cli")
				if err != nil {
					failures <- err
					return
				}
				c := got.AttentionContext
				expected := "known"
				if c.PolicyVersion%2 == 0 {
					expected = "not_configured"
				}
				if uint64(c.PolicyVersion) != uint64(got.Revision)+1 || string(got.Attention.ReadyForReview.State) != expected || got.Attention.Blocked.State != "not_configured" || got.Attention.NeedsTerry.State != "not_configured" || got.LastCardActivity != nil {
					failures <- fmt.Errorf("mixed revision, category or activity observation")
					return
				}
				if expected == "known" && (got.Attention.ReadyForReview.Value == nil || *got.Attention.ReadyForReview.Value != 0 || c.Source != "implicit") {
					failures <- fmt.Errorf("lost known zero")
					return
				}
				if expected == "not_configured" && (got.Attention.ReadyForReview.Value != nil || c.Source != "explicit") {
					failures <- fmt.Errorf("disabled mapping mismatch")
					return
				}
			}
		}()
	}
	candidate := policyedit.DefaultDefinition(A.UUID(uuid()), 2)
	for version := 2; version <= 9; version++ {
		candidate.Revision = A.PolicyVersion(version)
		candidate.Attention = nil
		if version%2 == 0 {
			candidate.Attention = &A.AttentionAssignments{Blocked: []A.Lane{}, NeedsTerry: []A.Lane{}, ReadyForReview: []A.Lane{}}
		}
		wire := mutationWire(t, r, "", version-2, 0, map[string]any{"expectedPolicyVersion": version - 1, "workflow": candidate})
		term(t, request(t, r, "PATCH", r.boardPath()+"workflow", wire, info.Token, nil, nil), "committed")
	}
	close(done)
	readers.Wait()
	close(failures)
	for failure := range failures {
		t.Error(failure)
	}
}
