// Package noticeinterval defines the durable, private boundary between the
// authorized activity source and the fixed-target notice worker.
package noticeinterval

import A "github.com/localswim/localswim/internal/contract/archive"

// Cut identifies a position and its coverage within one physical lineage.
// An empty Floor represents the absence of a selected activity floor.
type Cut struct {
	Event    A.Number    `json:"event"`
	Revision A.Number    `json:"revision"`
	At       A.Timestamp `json:"at"`
	Floor    A.Timestamp `json:"floor"`
	Gap      A.Number    `json:"gap"`
}

func (c Cut) Valid() bool {
	return A.Validate(c.Event) == nil && A.Validate(c.Revision) == nil &&
		A.Validate(c.At) == nil && (c.Floor == "" || A.Validate(c.Floor) == nil) &&
		A.Validate(c.Gap) == nil
}

// Changed reports an authoritative observation signal, excluding sampling time.
func (c Cut) Changed(prior Cut) bool {
	return c.Event != prior.Event || c.Floor != prior.Floor || c.Gap != prior.Gap
}

// Follows checks monotone descriptor fields; wall time may regress.
func (c Cut) Follows(prior Cut) bool {
	return c.Valid() && prior.Valid() && c.Event >= prior.Event &&
		c.Revision >= prior.Revision && c.Gap >= prior.Gap && c.Floor >= prior.Floor
}

// Evidence is an aggregate of one pinned interval, never a collection of
// activity rows. Nil Count is used when the interval cannot be verified.
type Evidence struct {
	Upper *Cut
	Class string
	Count *A.Number
}
