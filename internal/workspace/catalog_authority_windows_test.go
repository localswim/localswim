//go:build windows && amd64

package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

// catalog checks Done only after it has joined the host's pending jobs. This
// context makes that request boundary deterministic without sleeping or IO.
type catalogJoinedContext struct {
	context.Context
	once   sync.Once
	joined chan struct{}
}

func (c *catalogJoinedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.joined) })
	return nil
}
func joinedPoll(r *Runtime) (<-chan T.WorkspaceCatalog, <-chan struct{}) {
	ctx := &catalogJoinedContext{Context: context.Background(), joined: make(chan struct{})}
	out := make(chan T.WorkspaceCatalog, 1)
	go func() { out <- r.catalog(ctx, "cli") }()
	return out, ctx.joined
}
func waitJoined(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("poll did not join host job")
	}
}

func TestCatalogLateJoinRevalidatesChangedNativeAuthority(t *testing.T) {
	for _, change := range []string{"identity", "revocation"} {
		t.Run(change, func(t *testing.T) {
			base := testBase(t)
			r := testStart(t, filepath.Join(base, "w"), testPort(t))
			fixture := createFixture(t, r, filepath.Join(base, "p"))
			ready, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(release) })
			var calls atomic.Int32
			var originalDeadline time.Time
			r.catalogProbe = func(ctx context.Context, engine *L.Runtime, channel string) (L.CatalogSnapshot, error) {
				n := calls.Add(1)
				deadline, _ := ctx.Deadline()
				if n == 1 {
					originalDeadline = deadline
				} else if n == 2 && deadline != originalDeadline {
					t.Error("late join reset the job attempt budget")
				}
				result, err := engine.CatalogSnapshot(ctx, channel)
				if n == 1 {
					if err != nil {
						t.Error(err)
					}
					close(ready)
					<-release
				}
				return result, err
			}
			first, _ := joinedPoll(r)
			waitJoined(t, ready)
			// Deliberate negative technical fixtures, not product mutation or
			// grant-management APIs: change native identity/current read admission
			// after an actual authorized SQL snapshot but before its host return.
			var restore func()
			if change == "identity" {
				path := filepath.Join(string(fixture.DataDir), "identity.json")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var data map[string]any
				if err = json.Unmarshal(original, &data); err != nil {
					t.Fatal(err)
				}
				data["incarnation"] = uuid.NewString()
				replaced, err := json.Marshal(data)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, replaced, 0600); err != nil {
					t.Fatal(err)
				}
				restore = func() {
					if err := os.WriteFile(path, original, 0600); err != nil {
						t.Error(err)
					}
				}
			} else {
				db, err := sql.Open("sqlite", filepath.Join(string(fixture.DataDir), "board", "board.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				statement := "UPDATE grant_record SET enabled=? WHERE actor_id IN (SELECT actor_id FROM actor WHERE kind='automation')"
				result, err := db.Exec(statement, 0)
				if err != nil {
					t.Fatal(err)
				}
				if n, _ := result.RowsAffected(); n != 1 {
					t.Fatal("revocation fixture changed unexpected grants")
				}
				restore = func() {
					if _, err := db.Exec(statement, 1); err != nil {
						t.Error(err)
					}
				}
			}
			defer restore()
			r.mu.Lock()
			engine := r.states["harbor/main"].engine
			r.mu.Unlock()
			if engine.ConfiguredReadAllowed(context.Background(), "cli") {
				t.Fatal("current changed authority was not denied")
			}
			late, joined := joinedPoll(r)
			waitJoined(t, joined)
			once.Do(func() { close(release) })
			for _, poll := range []<-chan T.WorkspaceCatalog{first, late} {
				select {
				case out := <-poll:
					row := out.Entries[0]
					if row.Freshness != "never" || row.Identity != nil || row.BoardRevision != nil || row.Attention.ReadyForReview.Value != nil || row.Activity.Coverage != nil {
						t.Fatal("late join inherited pre-change authority facts")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("late authority response exceeded budget")
				}
			}
			if calls.Load() != 2 {
				t.Fatal("late join did not re-read within original job")
			}
			restore()
			out := r.catalog(context.Background(), "cli")
			if calls.Load() != 3 || out.Entries[0].Freshness != "fresh" {
				t.Fatal("post-completion poll did not authorize a new snapshot")
			}
		})
	}
}

func TestCatalogPendingJoinSharesOneAuthorizedSnapshot(t *testing.T) {
	base := testBase(t)
	r := testStart(t, filepath.Join(base, "w"), testPort(t))
	createFixture(t, r, filepath.Join(base, "p"))
	var calls atomic.Int32
	r.catalogProbe = func(ctx context.Context, engine *L.Runtime, channel string) (L.CatalogSnapshot, error) {
		calls.Add(1)
		return engine.CatalogSnapshot(ctx, channel)
	}
	// Occupy admission slots before either poll joins: both callers are in the
	// captured generation before the one real snapshot is admitted.
	for i := 0; i < 4; i++ {
		r.slots <- struct{}{}
	}
	first, joined := joinedPoll(r)
	waitJoined(t, joined)
	second, joined := joinedPoll(r)
	waitJoined(t, joined)
	r.mu.Lock()
	<-r.slots
	r.pumpCatalogLocked()
	r.mu.Unlock()
	for i := 0; i < 3; i++ {
		<-r.slots
	}
	for _, poll := range []<-chan T.WorkspaceCatalog{first, second} {
		select {
		case out := <-poll:
			if out.Entries[0].Freshness != "fresh" {
				t.Fatal("pending joins did not share current snapshot")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("pending joins did not complete")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("pending joins duplicated the authorized snapshot")
	}
}
