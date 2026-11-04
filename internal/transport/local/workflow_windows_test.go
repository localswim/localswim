//go:build windows && amd64

package local

import (
	"context"
	"database/sql"
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func configuredFixture() *A.Workflow {
	w := &A.Workflow{WorkflowID: "10000000-0000-4000-8000-000000000010", Revision: 1,
		ActorRights: A.WorkflowActorRights{Human: []string{"approve", "create", "edit", "read", "read-own-outcome", "review-handoff"}, Automation: []string{"read", "read-own-outcome"}}}
	roles := []string{"initial", "in_progress", "review", "completed"}
	ids := []A.UUID{"10000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002", "10000000-0000-4000-8000-000000000003", "10000000-0000-4000-8000-000000000004"}
	for i, role := range roles {
		w.Lanes = append(w.Lanes, A.WorkflowLane{ID: ids[i], Label: role, Role: role})
		if i > 0 {
			w.Transitions = append(w.Transitions, A.WorkflowTransition{From: ids[i-1], To: ids[i], Actors: []A.Kind{"human"}})
		}
	}
	return w
}

func TestConfiguredNativeInitInspectAndReopen(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(testRoot(t), "configured")
	w := configuredFixture()
	id, err := InitializeBoard(ctx, root, w)
	if err != nil {
		t.Fatal(err)
	}
	got, err := InspectBoardDefinition(ctx, root, &id)
	if err != nil || !reflect.DeepEqual(got.Workflow, w) {
		t.Fatalf("inspect mismatch: %v", err)
	}
	if _, err = InitializeBoard(ctx, root, w); err == nil {
		t.Fatal("occupied initialization accepted")
	}
	r, err := OpenEngine(ctx, EngineOptions{DataDir: root, WorkspaceID: "20000000-0000-4000-8000-000000000001", Origin: "http://127.0.0.1:8991", ExpectedIdentity: &id})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	live, err := r.InspectDefinition(ctx, "browser")
	if err != nil || !reflect.DeepEqual(live.Workflow, w) {
		t.Fatalf("live policy mismatch: %v", err)
	}
	if _, err = InspectBoardDefinition(ctx, root, &id); err == nil {
		t.Fatal("offline inspected owned root")
	}
	lease, err := r.store.Reader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if err = lease.Do(ctx, func(c *sql.Conn) error {
		var count int
		if err := c.QueryRowContext(ctx, "SELECT count(*) FROM grant_right WHERE incarnation=? AND grant_id=?", id.Incarnation, r.id.BotGrant).Scan(&count); err != nil {
			return err
		}
		if count != 2 {
			t.Fatal("seed ignored automation ceiling")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredInvalidDefinitionDoesNotAllocate(t *testing.T) {
	root := filepath.Join(testRoot(t), "invalid")
	w := configuredFixture()
	w.Lanes[1].Label = w.Lanes[0].Label
	if _, err := InitializeBoard(context.Background(), root, w); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("invalid policy allocated root")
	}
}

func TestConfiguredInitRefusesCompetingEmptyPrivateRoot(t *testing.T) {
	root := filepath.Join(testRoot(t), "competing")
	// Deterministically install the competitor immediately before the atomic
	// native creation boundary. An observation followed by adopting OPEN_ALWAYS
	// would initialize this empty root; exclusive creation must refuse it.
	_, err := initializeBoardWithRoot(context.Background(), root, configuredFixture(), func(path string) (*WorkspacePath, error) {
		if e := privateDir(path); e != nil {
			t.Fatal(e)
		}
		return createInitializationRoot(path)
	})
	if !errors.Is(err, ErrInitializationRefused) {
		t.Fatalf("competing root was not refused: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("competing root was changed")
	}
}
