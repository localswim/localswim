package local

import (
	"context"
	"errors"
	"github.com/localswim/localswim/internal/board/policy"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

var ErrInitializationRefused = errors.New("board initialization refused before allocation")

// InitializeBoard explicitly initializes a new configured native board. It does
// not listen, register workspace membership, replace or adopt an occupied root.
func InitializeBoard(ctx context.Context, dataDir string, workflow *A.Workflow) (BoardIdentity, error) {
	return initializeBoardWithRoot(ctx, dataDir, workflow, createInitializationRoot)
}

func initializeBoardWithRoot(ctx context.Context, dataDir string, workflow *A.Workflow, createRoot func(string) (*WorkspacePath, error)) (id BoardIdentity, err error) {
	if ctx == nil || ctx.Err() != nil || workflow == nil || A.ValidateInitialWorkflow(workflow) != nil {
		return BoardIdentity{}, ErrInitializationRefused
	}
	guard, err := createRoot(dataDir)
	if guard != nil {
		// Keep the creator's full chain pinned through engine initialization and
		// close. The normal open path can only reopen this owned directory.
		defer func() {
			if e := guard.Close(); e != nil {
				err = errors.Join(err, workspaceRetain(e, guard.Close))
			}
		}()
	}
	if err != nil {
		return BoardIdentity{}, err
	}
	if guard == nil {
		return BoardIdentity{}, errLocal
	}
	r, err := openEngine(ctx, EngineOptions{DataDir: dataDir, Workflow: workflow}, nil)
	if err != nil {
		return BoardIdentity{}, err
	}
	id = r.Identity()
	if err = r.Close(); err != nil {
		return id, workspaceRetain(err, r.Close)
	}
	return id, nil
}

// BoardDefinition reports a fully validated native identity and its complete
// current definition and retained policy context. Nil Workflow identifies default1.
type BoardDefinition struct {
	Identity         BoardIdentity      `json:"identity"`
	Workflow         *A.Workflow        `json:"workflow,omitempty"`
	WorkflowPolicies []A.WorkflowPolicy `json:"workflowPolicies,omitempty"`
}

// InspectDefinition reads an already owned engine under current channel read
// authority. It never reacquires exclusion, activates or restarts that engine.
func (r *Runtime) InspectDefinition(ctx context.Context, channel string) (BoardDefinition, error) {
	out := BoardDefinition{Identity: r.Identity()}
	err := r.withConfiguredRead(ctx, channel, func(rd writer.Read) error {
		var err error
		out.Workflow, err = policy.Load(rd, r.id.Incarnation)
		if err != nil {
			return err
		}
		if out.Workflow != nil {
			if err = policy.ValidateCurrent(rd, r.id.Incarnation, out.Workflow); err != nil {
				return err
			}
		}
		out.WorkflowPolicies, err = policy.LoadHistory(rd, r.id.Incarnation)
		if len(out.WorkflowPolicies) == 1 {
			out.WorkflowPolicies = nil
		}
		return err
	})
	if err != nil {
		return BoardDefinition{}, err
	}
	return out, nil
}
