package admin

import (
	"bytes"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

var errPreview = errors.New("PREVIEW_UNAVAILABLE_OR_MISMATCH")

// matchPreview requires the ORIGINAL immutable preview. It never refreshes a
// stale preview, substitutes a revision, or constructs a new confirmation.
// Server admission still independently recomputes the inventory and authority.
func matchPreview(preview, confirmation []byte, board, incarnation, actor string, now time.Time) (A.Preview, A.Confirmation, error) {
	p, err := A.Decode[A.Preview](preview)
	if err != nil {
		return A.Preview{}, A.Confirmation{}, errPreview
	}
	c, err := A.Decode[A.Confirmation](confirmation)
	if err != nil {
		return A.Preview{}, A.Confirmation{}, errPreview
	}
	expires, err := time.Parse("2006-01-02T15:04:05Z", string(p.ExpiresAt))
	issued, other := time.Parse("2006-01-02T15:04:05Z", string(p.PreviewedAt))
	if err != nil || other != nil || now.Before(issued) || !now.Before(expires) || string(p.BoardID) != board || string(p.Incarnation) != incarnation || string(p.ActorID) != actor || c.Incarnation != p.Incarnation || c.ExpectedRevision != p.ExpectedRevision || c.Command != p.Command || c.PreviewHash != p.PreviewHash {
		return A.Preview{}, A.Confirmation{}, errPreview
	}
	left, e := contract.DecodeJSON(p.Input)
	right, f := contract.DecodeJSON(c.Input)
	if e != nil || f != nil {
		return A.Preview{}, A.Confirmation{}, errPreview
	}
	x, e := contract.Canonical(left)
	y, f := contract.Canonical(right)
	if e != nil || f != nil || !bytes.Equal(x, y) {
		return A.Preview{}, A.Confirmation{}, errPreview
	}
	return p, c, nil
}
