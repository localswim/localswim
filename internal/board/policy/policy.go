// Package policy loads immutable configured workflow policy in the caller's snapshot.
package policy

import (
	"database/sql"
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
)

// Reader is the narrow snapshot interface shared by domain and native readers.
type Reader interface {
	Scan(string, []any, ...any) error
}

// Load returns nil only for the exact legacy profile. Corrupt or unavailable
// configured policy fails closed; no default definition is synthesized.
func Load(r Reader, incarnation string) (*A.Workflow, error) {
	var id string
	var revision uint64
	var raw string
	err := r.Scan("SELECT workflow_id,revision,definition FROM workflow_definition WHERE incarnation=?", []any{incarnation}, &id, &revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	definition, err := A.DecodeWorkflow([]byte(raw))
	if err != nil {
		return nil, err
	}
	if err = A.ValidateWorkflow(definition); err != nil {
		return nil, err
	}
	if string(definition.WorkflowID) != id || uint64(definition.Revision) != revision {
		return nil, errors.New("workflow identity mismatch")
	}
	return definition, nil
}

// AllowsRights applies the ordinary-right ceiling without changing native
// administration's separate authority boundary.
func AllowsRights(w *A.Workflow, kind string, rights ...string) bool {
	for _, right := range rights {
		switch right {
		case "attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff", "approve":
			if !A.WorkflowAllowsRight(w, A.Kind(kind), right) {
				return false
			}
		}
	}
	return true
}
