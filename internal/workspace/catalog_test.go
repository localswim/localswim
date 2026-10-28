package workspace

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

func TestCatalogCoalescesSlowMemberAndCompletesFairQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runtime{ctx: ctx, cancel: cancel, slots: make(chan struct{}, 4), states: map[string]*activation{}}
	slow := &L.Runtime{}
	for i := 0; i < 12; i++ {
		m := member{ProjectKey: "p", BoardKey: T.WorkspaceKey(fmt.Sprintf("b%d", i)), Label: T.WorkspaceLabel(fmt.Sprintf("Board %d", i))}
		r.config.Members = append(r.config.Members, m)
		engine := &L.Runtime{}
		if i == 0 {
			engine = slow
		}
		r.states[memberKey(m.ProjectKey, m.BoardKey)] = &activation{engine: engine, availability: "ready"}
	}
	release := make(chan struct{})
	var slowCalls, active, maximum atomic.Int32
	r.catalogProbe = func(ctx context.Context, engine *L.Runtime, channel string) (L.CatalogSnapshot, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		if engine == slow {
			slowCalls.Add(1)
			<-release
			return L.CatalogSnapshot{}, ctx.Err()
		}
		time.Sleep(10 * time.Millisecond)
		return L.CatalogSnapshot{Revision: 1, Coverage: &A.Coverage{Gaps: []A.Gap{}}}, nil
	}
	var wg sync.WaitGroup
	outs := make([]T.WorkspaceCatalog, 2)
	start := time.Now()
	for i := range outs {
		wg.Add(1)
		go func() { defer wg.Done(); outs[i] = r.catalog(context.Background(), "cli") }()
	}
	wg.Wait()
	if time.Since(start) > 2500*time.Millisecond {
		t.Error("response budget exceeded")
	}
	if slowCalls.Load() != 1 || maximum.Load() > 4 {
		t.Error("duplicate probe or outstanding limit")
	}
	for _, out := range outs {
		healthy := 0
		for _, entry := range out.Entries {
			if entry.BoardKey == "b0" {
				if entry.Freshness != "never" || entry.Identity != nil || entry.BoardRevision != nil {
					t.Error("unfinished authority leaked cached facts")
				}
			} else if entry.Freshness == "fresh" {
				healthy++
			}
		}
		if healthy != 11 {
			t.Errorf("pending fairness lost healthy members: %d", healthy)
		}
	}
	close(release)
	cancel()
	r.jobs.Wait()
}

func TestCatalogOtherOrderingAndFutureAge(t *testing.T) {
	future := A.Timestamp("2099-01-01T00:00:00Z")
	older := A.Timestamp("2026-01-01T00:00:00Z")
	zero, three := A.Number(0), A.Number(3)
	entry := func(label string, at *A.Timestamp, count *A.Number) T.WorkspaceCatalogEntry {
		return T.WorkspaceCatalogEntry{Label: T.WorkspaceLabel(label), Freshness: "fresh", Activity: T.CatalogActivity{State: "known", LastCommittedAt: at}, Attention: T.CatalogAttention{ReadyForReview: T.CatalogCount{State: "known", Value: count}}}
	}
	a, b := entry("Zulu", &future, &zero), entry("Alpha", &older, &three)
	if !catalogLess(a, b) {
		t.Fatal("future committed activity lost ordering")
	}
	b.Activity.LastCommittedAt = &future
	if !catalogLess(b, a) {
		t.Fatal("known attention tie ordering")
	}
	a.Activity.LastCommittedAt = nil
	b.Activity.LastCommittedAt = nil
	if !catalogLess(b, a) {
		t.Fatal("unknown activity must use label, not score")
	}
	a.Label = "same"
	b.Label = "SAME"
	a.ProjectKey = "a"
	b.ProjectKey = "b"
	if !catalogLess(a, b) {
		t.Fatal("exact key deterministic tie")
	}
	if elapsed(time.Now(), time.Now().Add(time.Hour)) != 0 {
		t.Fatal("future elapsed display not clamped")
	}
}
