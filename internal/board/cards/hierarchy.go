package cards

import (
	"errors"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/hierarchy"
	"github.com/localswim/localswim/internal/store/writer"
)

func sameParent(a *contract.CardRef, b *A.CardRef) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.BoardID == string(b.BoardID) && a.Incarnation == string(b.Incarnation) && a.CardID == string(b.CardID)
}

// Called only inside the sole writer admission snapshot, after normal admission.
func validateParent(r writer.Read, e envelope, child string) error {
	p := e.Payload.Parent.Value
	if p != nil && (p.BoardID != e.BoardID || p.Incarnation != e.Incarnation) {
		return errors.New("invalid hierarchy")
	}
	parents := map[string]string{}
	rows, err := r.Query(`SELECT c.card_id,coalesce(p.parent_id,'') FROM card c LEFT JOIN card_parent p ON p.incarnation=c.incarnation AND p.child_id=c.card_id WHERE c.incarnation=?`, e.Incarnation)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, parent string
		if err = rows.Scan(&id, &parent); err != nil {
			rows.Close()
			return err
		}
		parents[id] = parent
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if p != nil {
		if _, ok := parents[p.CardID]; !ok {
			return errors.New("invalid hierarchy")
		}
	}
	if child == "" {
		child = "new-card-admission"
	}
	parent := ""
	if p != nil {
		parent = p.CardID
	}
	parents[child] = parent
	return hierarchy.Validate(parents)
}
