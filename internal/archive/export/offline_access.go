package export

import (
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

// OfflineDocuments contains logical rows projected from one caller-owned reader.
// It is neither an authenticated export nor a validated publication anchor.
type OfflineDocuments struct {
	Board    A.Board
	History  A.History
	Activity A.Activity
	Outcomes A.Outcomes
	Lineage  []A.Lineage
}

// ProjectOffline reuses the bounded logical projection for native restore/status.
// The trusted caller MUST hold exclusive offline filesystem ownership and one
// actual reader transaction for this call and its complete BLOB/inventory check.
// No reader, Rows or SQL connection may escape that transaction. This function
// performs only SELECTs; it supplies no permission, pin, archive output or proof
// of source-to-destination transformation. It must never replace Prepare's live
// current read/export verifier in an ordinary HTTP or CLI export operation.
func ProjectOffline(r writer.Read, id A.Identity, revision A.Number) (OfflineDocuments, error) {
	var out OfflineDocuments
	if r == nil {
		return out, errors.New("offline reader required")
	}
	if err := A.Validate(id); err != nil {
		return out, err
	}
	if err := A.Validate(revision); err != nil {
		return out, err
	}
	var actual A.Identity
	var current A.Number
	if err := r.Scan("SELECT board_id,incarnation,revision FROM board WHERE singleton=1", nil, &actual.BoardID, &actual.Incarnation, &current); err != nil {
		return out, err
	}
	if actual != id || current != revision {
		return out, ErrPrecondition
	}
	board, history, activity, outcomes, lineage, err := project(r, id, revision)
	if err != nil {
		return out, err
	}
	return OfflineDocuments{Board: board, History: history, Activity: activity, Outcomes: outcomes, Lineage: lineage}, nil
}
