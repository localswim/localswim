//go:build windows && amd64

package local

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

func catalogSnapshot(t *testing.T, r *Runtime, revision, ready A.Number) CatalogSnapshot {
	t.Helper()
	var out CatalogSnapshot
	for _, channel := range []string{"browser", "cli"} {
		got, err := r.CatalogSnapshot(context.Background(), channel)
		if err != nil || got.Revision != revision || got.ReadyForReview != ready || got.Coverage == nil {
			t.Fatalf("catalog snapshot %s: revision=%d ready=%d coverage=%t error=%v", channel, got.Revision, got.ReadyForReview, got.Coverage != nil, err)
		}
		if channel == "cli" && !reflect.DeepEqual(out, got) {
			t.Fatal("configured channels disagree on one unchanged board")
		}
		out = got
	}
	return out
}

func catalogCreate(t *testing.T, r *Runtime, token T.SecretToken) string {
	t.Helper()
	created := term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0, map[string]any{"title": "Catalog native journey", "description": "Synthetic"}), token, nil, nil), "committed")
	for _, ref := range created.ResultRefs {
		if ref.Kind == "card" {
			return string(ref.ID)
		}
	}
	t.Fatal("committed create missing card reference")
	return ""
}

// Positive product evidence uses the ordinary local HTTP mutations and the
// existing human-only approve path. SQL is not a mutation fallback.
func TestWorkspaceCatalogSnapshotNativeHTTP(t *testing.T) {
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	cookie := bootstrapTest(t, r)
	empty := catalogSnapshot(t, r, 0, 0)
	if empty.LastCardActivity != nil || len(empty.Coverage.Gaps) != 0 {
		t.Fatal("empty board manufactured activity or coverage gaps")
	}
	card := catalogCreate(t, r, info.Token)
	path := r.boardPath() + "cards/" + card
	created := catalogSnapshot(t, r, 1, 0)
	if created.LastCardActivity == nil {
		t.Fatal("committed create missing activity")
	}
	term(t, request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"lane": "In Progress"}), info.Token, nil, nil), "committed")
	term(t, request(t, r, "POST", path+"/review-handoff", mutationWire(t, r, card, 2, 2, map[string]any{}), info.Token, nil, nil), "committed")
	catalogSnapshot(t, r, 3, 1)
	term(t, request(t, r, "POST", path+"/approve", mutationWire(t, r, card, 3, 3, map[string]any{"policyVersion": 1}), info.Token, nil, nil), "not_committed")
	catalogSnapshot(t, r, 3, 1)
	term(t, request(t, r, "POST", path+"/approve", mutationWire(t, r, card, 3, 3, map[string]any{"policyVersion": 1}), "", cookie, nil), "committed")
	approved := catalogSnapshot(t, r, 4, 0)
	if approved.LastCardActivity == nil || *approved.LastCardActivity < *created.LastCardActivity {
		t.Fatal("committed activity moved backwards")
	}
	read := request(t, r, "GET", path, nil, info.Token, nil, nil)
	view, err := T.Decode[T.CardRead](read.b)
	if err != nil || read.status != 200 || view.Result.Card.Lane != "Completed" || view.Result.Card.Priority != "P3" {
		t.Fatal("projection changed workflow or explicit priority")
	}
	credentials := configuredCredentials(r)
	metadata := configuredMetadataHashes(t, root)
	// File touches are neither card commits nor authority changes.
	future := time.Now().Add(48 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "identity.json"), future, future); err != nil {
		t.Fatal(err)
	}
	touched := catalogSnapshot(t, r, 4, 0)
	if !reflect.DeepEqual(approved, touched) {
		t.Fatal("metadata touch changed catalog facts")
	}
	// Mutating a detached result must not corrupt a later observation.
	*touched.LastCardActivity = "9999-12-31T23:59:59Z"
	floor := A.Timestamp("2099-01-01T00:00:00Z")
	touched.Coverage.HistoryFloor = &floor
	if reflect.DeepEqual(touched, catalogSnapshot(t, r, 4, 0)) {
		t.Fatal("projection exposed retained mutable runtime state")
	}
	if !reflect.DeepEqual(credentials, configuredCredentials(r)) || !reflect.DeepEqual(metadata, configuredMetadataHashes(t, root)) {
		t.Fatal("projection minted credentials or changed member metadata")
	}
}

func TestWorkspaceCatalogSnapshotCurrentAuthorityAndFailure(t *testing.T) {
	r, root := configuredReadEngine(t)
	catalogSnapshot(t, r, 0, 0)
	deny := func(channel string) {
		t.Helper()
		got, err := r.CatalogSnapshot(context.Background(), channel)
		if err == nil || !reflect.DeepEqual(got, CatalogSnapshot{}) {
			t.Fatal("failed authority disclosed partial catalog data")
		}
	}
	for _, selected := range []struct{ channel, grant string }{{"browser", r.id.HumanGrant}, {"cli", r.id.BotGrant}} {
		configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, selected.grant)
		deny(selected.channel)
		configuredGrantSQL(t, r, "INSERT INTO grant_right(incarnation,grant_id,right_name) VALUES(?,?,'read')", r.id.Incarnation, selected.grant)
		configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, selected.grant)
		deny(selected.channel)
		configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, selected.grant)
	}
	for _, channel := range []string{"", "human", "automation", "native-owner"} {
		deny(channel)
	}
	for _, ctx := range []context.Context{nil, func() context.Context { c, cancel := context.WithCancel(context.Background()); cancel(); return c }()} {
		if got, err := r.CatalogSnapshot(ctx, "cli"); err == nil || !reflect.DeepEqual(got, CatalogSnapshot{}) {
			t.Fatal("invalid context disclosed catalog data")
		}
	}
	path := filepath.Join(root, "identity.json")
	original, err := readPrivate(path)
	if err != nil {
		t.Fatal(err)
	}
	replaced := r.id
	replaced.Incarnation = uuid()
	body, _ := json.Marshal(replaced)
	if err := publish(path, body, true); err != nil {
		t.Fatal(err)
	}
	deny("browser")
	deny("cli")
	if err := publish(path, original, true); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	deny("cli")
}

func TestWorkspaceCatalogSnapshotCommittedAuthorityBoundary(t *testing.T) {
	r, _ := configuredReadEngine(t)
	lease, err := r.store.Writer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(context.Background(), func(c *sql.Conn) error {
		if _, err := c.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
			return err
		}
		if _, err := c.ExecContext(context.Background(), "UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.HumanGrant); err != nil {
			return err
		}
		if _, err := c.ExecContext(context.Background(), "UPDATE board SET revision=1 WHERE singleton=1"); err != nil {
			return err
		}
		// The reader sees the old committed grant and old revision together,
		// never the writer's partially changed state.
		catalogSnapshot(t, r, 0, 0)
		_, err := c.ExecContext(context.Background(), "COMMIT")
		return err
	})
	if err = errors.Join(err, lease.Close()); err != nil {
		t.Fatal(err)
	}
	if got, err := r.CatalogSnapshot(context.Background(), "browser"); !errors.Is(err, grants.ErrDenied) || !reflect.DeepEqual(got, CatalogSnapshot{}) {
		t.Fatal("committed revoke disclosed new board revision")
	}
	got, err := r.CatalogSnapshot(context.Background(), "cli")
	if err != nil || got.Revision != 1 {
		t.Fatal("other configured grant did not see committed revision")
	}
	configuredGrantSQL(t, r, "UPDATE grant_record SET enabled=1 WHERE incarnation=? AND grant_id=?", r.id.Incarnation, r.id.HumanGrant)
	catalogSnapshot(t, r, 1, 0)
}

// This is a labeled technical archived/provenance fixture, not a product archive
// operation or customer journey. The exact native schema/consistency validator
// accepts the fixture before the projection is read. No archive API is added.
func TestWorkspaceCatalogSnapshotTechnicalArchivedFixture(t *testing.T) {
	r, root := startTest(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	card := catalogCreate(t, r, info.Token)
	path := r.boardPath() + "cards/" + card
	term(t, request(t, r, "PATCH", path, mutationWire(t, r, card, 1, 1, map[string]any{"lane": "In Progress"}), info.Token, nil, nil), "committed")
	term(t, request(t, r, "POST", path+"/review-handoff", mutationWire(t, r, card, 2, 2, map[string]any{}), info.Token, nil, nil), "committed")
	catalogSnapshot(t, r, 3, 1)
	configuredGrantSQL(t, r, "UPDATE card SET archived=1 WHERE incarnation=? AND card_id=?", r.id.Incarnation, card)
	// Add compact historical provenance with a future timestamp. Restored history
	// can predate this incarnation; its timestamp is not rewritten to observation time.
	lease, err := r.store.Writer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = lease.Do(context.Background(), func(c *sql.Conn) error {
		_, err := c.ExecContext(context.Background(), `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,target_kind,target_id,expected_revision,expected_card_revision,terminal_class,code,terminal_at,committed_revision,committed_card_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'card.edit','card',?,1,1,'COMMITTED','OK','2099-01-01T00:00:00Z',2,2,'2099-01-02T00:00:00Z','cleared',3)`, r.id.Incarnation, uuid(), uuid(), uuid(), r.id.BotID, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", card)
		if err != nil {
			return err
		}
		// A later non-card commit is not card activity. Activity payloads and
		// historical snapshot payloads may be pruned while provenance remains.
		_, err = c.ExecContext(context.Background(), `INSERT INTO operation(incarnation,origin_board,origin_incarnation,op_id,current_ledger,actor_id,fingerprint,command,expected_revision,terminal_class,code,terminal_at,committed_revision,removed_at,removal_reason,removal_revision) VALUES(?,?,?,?,0,?,?,'retention.set',2,'COMMITTED','OK','2099-02-01T00:00:00Z',3,'2099-02-02T00:00:00Z','cleared',3)`, r.id.Incarnation, uuid(), uuid(), uuid(), r.id.HumanID, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		if err != nil {
			return err
		}
		for _, query := range []string{
			`DELETE FROM activity`,
			`UPDATE card_revision SET state=NULL,removed_at='2098-01-02T00:00:00Z',removal_reason='cleared',removal_revision=3 WHERE state IS NOT NULL`,
			`UPDATE board SET history_floor='2098-01-01T00:00:00Z',activity_floor='2098-01-01T00:00:00Z' WHERE singleton=1`,
		} {
			if _, err = c.ExecContext(context.Background(), query); err != nil {
				return err
			}
		}
		_, err = c.ExecContext(context.Background(), `INSERT INTO coverage_gap(incarnation,origin_board,origin_incarnation,class,reason,through_inclusive,recorded_at,board_revision) VALUES(?,?,?,'history','cleared','2098-01-01T00:00:00Z','2098-01-02T00:00:00Z',3)`, r.id.Incarnation, r.id.BoardID, r.id.Incarnation)
		return err
	})
	if err = errors.Join(err, lease.Close()); err != nil {
		t.Fatal(err)
	}
	reader, err := r.store.Reader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = reader.Do(context.Background(), func(c *sql.Conn) error {
		return schema.Validate(context.Background(), c, schema.Identity{BoardID: r.id.BoardID, Incarnation: r.id.Incarnation})
	})
	if err = errors.Join(err, reader.Close()); err != nil {
		t.Fatalf("technical fixture validation: %v", err)
	}
	got := catalogSnapshot(t, r, 3, 0)
	if got.LastCardActivity == nil || *got.LastCardActivity != "2099-01-01T00:00:00Z" {
		t.Fatal("archived compact commit excluded or future timestamp normalized")
	}
	if got.Coverage.HistoryFloor == nil || *got.Coverage.HistoryFloor != "2098-01-01T00:00:00Z" || got.Coverage.ActivityFloor == nil || len(got.Coverage.Gaps) != 1 || got.Coverage.Gaps[0].Class != "history" {
		t.Fatal("cleared/pruned history coverage hidden")
	}
}

func TestWorkspaceConfiguredReadSnapshotDrainAndBudget(t *testing.T) {
	r, root := configuredReadEngine(t)
	entered, release := make(chan struct{}), make(chan struct{})
	drained := make(chan error, 1)
	go func() {
		drained <- r.withConfiguredRead(context.Background(), "browser", func(rd writer.Read) error {
			close(entered)
			<-release
			var revision int
			return rd.Scan("SELECT revision FROM board WHERE singleton=1", nil, &revision)
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("read did not enter")
	}
	closed := make(chan error, 1)
	go func() { closed <- r.Close() }()
	deadline := time.Now().Add(time.Second)
	for {
		r.reqMu.Lock()
		closing := r.closing
		r.reqMu.Unlock()
		if closing {
			break
		}
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("Close did not gate admission")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := r.CatalogSnapshot(context.Background(), "cli"); !errors.Is(err, grants.ErrDenied) {
		close(release)
		t.Fatal("late projection admitted")
	}
	select {
	case <-closed:
		close(release)
		t.Fatal("Close released admitted read")
	default:
	}
	if other, err := OpenEngine(context.Background(), EngineOptions{DataDir: root, WorkspaceID: r.workspaceID, Origin: r.Origin()}); err == nil {
		_ = other.Close()
		close(release)
		t.Fatal("ownership released during read")
	}
	close(release)
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}

	r2, _ := configuredReadEngine(t)
	a, err := r2.store.Reader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := r2.store.Reader(context.Background())
	if err != nil {
		_ = a.Close()
		t.Fatal(err)
	}
	start := time.Now()
	got, err := r2.CatalogSnapshot(context.Background(), "cli")
	elapsed := time.Since(start)
	if closeErr := errors.Join(a.Close(), b.Close()); closeErr != nil {
		t.Fatal(closeErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(got, CatalogSnapshot{}) || elapsed < 800*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("reader budget: elapsed=%v error=%v", elapsed, err)
	}
	catalogSnapshot(t, r2, 0, 0)
}
