package grants

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/store/writer"
	_ "modernc.org/sqlite"
)

const currentInstallation = "00000000-0000-4000-8000-000000000001"
const currentBoard = "00000000-0000-4000-8000-000000000002"
const currentIncarnation = "00000000-0000-4000-8000-000000000003"
const currentActor = "00000000-0000-4000-8000-000000000004"
const currentGrant = "00000000-0000-4000-8000-000000000005"
const currentOrigin = "https://synthetic.localhost:47443"

// This portable snapshot contains only the read projections used by current;
// native store creation and credential persistence have separate integration tests.
type currentSnapshot struct{ db *sql.DB }

func (r currentSnapshot) Scan(q string, args []any, dest ...any) error {
	return r.db.QueryRow(q, args...).Scan(dest...)
}

func (r currentSnapshot) Query(q string, args ...any) (*sql.Rows, error) {
	return r.db.Query(q, args...)
}

func currentFixture(t *testing.T) currentSnapshot {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, q := range []string{
		"CREATE TABLE board(singleton INTEGER,board_id TEXT,incarnation TEXT)",
		"CREATE TABLE actor(incarnation TEXT,actor_id TEXT,kind TEXT)",
		"CREATE TABLE grant_record(incarnation TEXT,grant_id TEXT,actor_id TEXT,enabled INTEGER)",
		"CREATE TABLE grant_right(incarnation TEXT,grant_id TEXT,right_name TEXT)",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO board VALUES(1,?,?)", []any{currentBoard, currentIncarnation}},
		{"INSERT INTO actor VALUES(?,?,'human')", []any{currentIncarnation, currentActor}},
		{"INSERT INTO grant_record VALUES(?,?,?,1)", []any{currentIncarnation, currentGrant, currentActor}},
		{"INSERT INTO grant_right VALUES(?,?,'read-own-outcome')", []any{currentIncarnation, currentGrant}},
	} {
		if _, err := db.Exec(stmt.query, stmt.args...); err != nil {
			t.Fatal(err)
		}
	}
	return currentSnapshot{db}
}

func currentBrowser(at time.Time) Credential {
	return Credential{
		InstallationID: currentInstallation, Origin: currentOrigin,
		BoardID: currentBoard, Incarnation: currentIncarnation, ActorID: currentActor,
		GrantID: currentGrant, Mode: "browser", IssuedAt: at.Add(-time.Hour),
		ExpiresAt: at.Add(time.Hour), IdleExpiresAt: at.Add(30 * time.Minute),
	}
}

func TestCurrentValidatesTimeAfterVerifier(t *testing.T) {
	for _, scenario := range []string{"concurrent-idle-refresh", "idle-expires", "absolute-expires"} {
		t.Run(scenario, func(t *testing.T) {
			r := currentFixture(t)
			at := time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)
			var ticks atomic.Int64
			ticks.Store(at.UnixNano())
			c := currentBrowser(at)
			if scenario == "idle-expires" {
				c.IdleExpiresAt = at.Add(time.Millisecond)
			}
			if scenario == "absolute-expires" {
				c.ExpiresAt = at.Add(time.Millisecond)
			}
			started, release := make(chan time.Time, 1), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			s, err := New(currentInstallation, currentOrigin, func(ctx context.Context, _ writer.Read, verifyAt time.Time) (Credential, error) {
				started <- verifyAt
				select {
				case <-release:
					// The barrier orders the simulated refresh before the copy.
					return c, nil
				case <-ctx.Done():
					return Credential{}, ctx.Err()
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			s.now = func() time.Time { return time.Unix(0, ticks.Load()).UTC() }
			done := make(chan error, 1)
			go func() {
				_, err := s.current(ctx, r, currentBoard, currentIncarnation, currentActor)
				done <- err
			}()
			select {
			case verifyAt := <-started:
				if !verifyAt.Equal(at) {
					t.Fatalf("verifier clock = %v, want initial clock %v", verifyAt, at)
				}
			case <-ctx.Done():
				t.Fatal("verifier did not reach barrier")
			}
			// Reach expiry exactly: validity is exclusive at both deadlines.
			validatedAt := at.Add(time.Millisecond)
			ticks.Store(validatedAt.UnixNano())
			if scenario == "concurrent-idle-refresh" {
				c.IdleExpiresAt = validatedAt.Add(30 * time.Minute)
			}
			unblock()
			select {
			case got := <-done:
				if scenario == "concurrent-idle-refresh" {
					if got != nil {
						t.Fatalf("valid refreshed credential rejected: %v", got)
					}
				} else if !errors.Is(got, ErrUnauthenticated) {
					t.Fatalf("credential expired during verification: got %v, want unauthenticated", got)
				}
			case <-ctx.Done():
				t.Fatal("admission did not finish after releasing verifier")
			}
		})
	}
}

func TestCurrentTimeAndScopeBounds(t *testing.T) {
	at := time.Date(2026, 11, 7, 13, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		change func(*Credential)
		want   error
	}{
		{"browser-boundary", func(c *Credential) { c.ExpiresAt = c.IssuedAt.Add(8 * time.Hour) }, nil},
		{"cli-boundary", func(c *Credential) {
			c.Mode = "cli"
			c.IdleExpiresAt = time.Time{}
			c.ExpiresAt = c.IssuedAt.Add(30 * 24 * time.Hour)
		}, nil},
		{"idle-over-limit", func(c *Credential) { c.IdleExpiresAt = at.Add(30*time.Minute + time.Nanosecond) }, ErrUnauthenticated},
		{"absolute-over-limit", func(c *Credential) { c.ExpiresAt = c.IssuedAt.Add(8*time.Hour + time.Nanosecond) }, ErrUnauthenticated},
		{"issued-future", func(c *Credential) { c.IssuedAt = at.Add(time.Nanosecond) }, ErrUnauthenticated},
		{"issued-zero", func(c *Credential) { c.IssuedAt = time.Time{} }, ErrUnauthenticated},
		{"absolute-expired", func(c *Credential) { c.ExpiresAt = at }, ErrUnauthenticated},
		{"idle-expired", func(c *Credential) { c.IdleExpiresAt = at }, ErrUnauthenticated},
		{"idle-zero", func(c *Credential) { c.IdleExpiresAt = time.Time{} }, ErrUnauthenticated},
		{"wrong-installation", func(c *Credential) { c.InstallationID = currentActor }, ErrUnauthenticated},
		{"wrong-origin", func(c *Credential) { c.Origin = "https://foreign.invalid" }, ErrUnauthenticated},
		{"cli-idle", func(c *Credential) { c.Mode = "cli" }, ErrUnauthenticated},
		{"cli-over-limit", func(c *Credential) {
			c.Mode = "cli"
			c.IdleExpiresAt = time.Time{}
			c.ExpiresAt = c.IssuedAt.Add(30*24*time.Hour + time.Nanosecond)
		}, ErrUnauthenticated},
		{"wrong-actor", func(c *Credential) { c.ActorID = currentGrant }, ErrDenied},
		{"wrong-board", func(c *Credential) { c.BoardID = currentActor }, ErrDenied},
		{"wrong-incarnation", func(c *Credential) { c.Incarnation = currentActor }, ErrDenied},
		{"wrong-grant", func(c *Credential) { c.GrantID = currentActor }, ErrDenied},
		{"invalid-grant", func(c *Credential) { c.GrantID = "invalid" }, ErrDenied},
		{"unknown-mode", func(c *Credential) { c.Mode = "unknown" }, ErrDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := currentFixture(t)
			c := currentBrowser(at)
			tc.change(&c)
			s, err := New(currentInstallation, currentOrigin, func(context.Context, writer.Read, time.Time) (Credential, error) { return c, nil })
			if err != nil {
				t.Fatal(err)
			}
			s.now = func() time.Time { return at }
			_, got := s.current(t.Context(), r, currentBoard, currentIncarnation, currentActor)
			if !errors.Is(got, tc.want) {
				t.Fatalf("current = %v, want %v", got, tc.want)
			}
		})
	}
}
