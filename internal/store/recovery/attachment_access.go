package recovery

import (
	"errors"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
)

// ErrByteQuota distinguishes a valid protected union exceeding its cap from
// inconsistent persisted pin facts or failed snapshot reads.
var ErrByteQuota = errors.New("protected byte quota exceeded")

// CheckQuotaWith includes pending attachment facts without mutating the store.
// The caller must acquire this guard after writer admission and retain it until
// S02 finishes COMMIT cleanup and readback. Existing protective and export facts
// use the same accounting as CheckQuota; duplicate digests count only once.
func (g *PinGuard) CheckQuotaWith(r writer.Read, pending ...A.ByteFact) error {
	f, e := g.Facts()
	if e != nil {
		return e
	}
	if r == nil {
		return errors.New("pin snapshot required")
	}
	p, e := domainPins(r, nil)
	if e != nil {
		return e
	}
	for _, v := range append(f, pending...) {
		if e = A.Validate(v); e != nil {
			return e
		}
		if e = addFact(p, v); e != nil {
			return e
		}
	}
	if _, e = factSum(factList(p)); e != nil {
		return ErrByteQuota
	}
	return nil
}
