package cards

import (
	"errors"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// SnapshotState is the existing archive representation of a prior card state.
type SnapshotState = A.SnapshotState

// LoadSnapshot captures values before changing the card or clearing approval.
// The caller owns the transaction and must not retain the supplied read seam.
func LoadSnapshot(r writer.Read, incarnation, cardID string) (SnapshotState, error) {
	if r == nil || !schema.ValidUUID(incarnation) || !schema.ValidUUID(cardID) {
		return SnapshotState{}, errors.New("invalid snapshot scope")
	}
	return loadSnapshot(r, envelope{Incarnation: incarnation}, cardID)
}

// WriteSnapshot preserves the existing provenance, prior references and pins.
// Advance the current card revision first, then call this with the captured
// prior state and revision BEFORE changing any current reference collections.
// The caller owns the same transaction and must roll back on any error.
func WriteSnapshot(w writer.Write, incarnation, cardID string, priorRevision A.Positive, state SnapshotState) error {
	if w == nil || !schema.ValidUUID(incarnation) || !schema.ValidUUID(cardID) || priorRevision == 0 || uint64(priorRevision) > contract.MaxInteger {
		return errors.New("invalid snapshot scope")
	}
	if err := A.Validate(state); err != nil {
		return err
	}
	return writeSnapshot(w, envelope{Incarnation: incarnation}, cardID, priorRevision, state)
}
