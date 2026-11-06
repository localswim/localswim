package boardapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
)

const noticeExpectedLimit = 1000

// ErrNoticeForeign reports an exact scope mismatch or unproved continuity.
var ErrNoticeForeign = errors.New("notice source continuity unproved")

// ObserveNoticeCut reads a baseline or wake signal without scanning events.
// The caller owns the pinned store and binds any retained cut to its physical
// lineage before use. Authorize must check current ordinary read authority in
// the supplied snapshot, including the selected credential and durable grant.
func ObserveNoticeCut(ctx context.Context, store *S.Store, identity A.Identity, authorize func(context.Context, writer.Read) error) (noticeinterval.Cut, error) {
	var cut noticeinterval.Cut
	_, err := readNoticeSource(ctx, store, identity, authorize, func(r writer.Read) error {
		var err error
		cut, err = noticeCut(r, identity, time.Now())
		return err
	})
	if err != nil {
		return noticeinterval.Cut{}, err
	}
	return cut, nil
}

// CaptureNoticeInterval classifies one admitted observation in the same pinned
// snapshot as its upper cut. Its lower cut must belong to the pinned physical
// lineage. Source failure after admission can retain a validated upper cut, but
// only after successful lease cleanup. Authority and scope failures expose no cut.
func CaptureNoticeInterval(ctx context.Context, store *S.Store, identity A.Identity, automation A.UUID, lower noticeinterval.Cut, authorize func(context.Context, writer.Read) error) (noticeinterval.Evidence, error) {
	if A.Validate(automation) != nil {
		return noticeinterval.Evidence{}, ErrUnavailable
	}
	var evidence noticeinterval.Evidence
	admitted, err := readNoticeSource(ctx, store, identity, authorize, func(r writer.Read) error {
		var err error
		evidence, err = captureNoticeInterval(r, identity, automation, lower, time.Now())
		return err
	})
	if err != nil {
		if admitted && errors.Is(err, ErrUnavailable) {
			return noticeinterval.Evidence{Upper: evidence.Upper, Class: "unavailable"}, nil
		}
		return noticeinterval.Evidence{}, err
	}
	return evidence, nil
}

func readNoticeSource(ctx context.Context, store *S.Store, identity A.Identity, authorize func(context.Context, writer.Read) error, read func(writer.Read) error) (bool, error) {
	if A.Validate(identity) != nil {
		return false, ErrNoticeForeign
	}
	if ctx == nil || store == nil || authorize == nil {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lease, err := store.Reader(ctx)
	if err != nil {
		return false, ErrUnavailable
	}
	admitted := false
	err = lease.Do(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "BEGIN"); err != nil {
			return err
		}
		rd := readAccess{ctx, c}
		// BEGIN is deferred. Pin the SQLite snapshot before current admission,
		// including any credential verification that uses only memory.
		var actual A.Identity
		if err := rd.Scan("SELECT board_id,incarnation FROM board WHERE singleton=1", nil, &actual.BoardID, &actual.Incarnation); err != nil {
			return err
		}
		if A.Validate(actual) != nil {
			return ErrUnavailable
		}
		if actual != identity {
			return ErrNoticeForeign
		}
		authority := &noticeAdmissionRead{activityAdmissionRead{readAccess: rd}}
		authorityErr := authorize(ctx, authority)
		if authority.err != nil {
			return authority.err
		}
		if authorityErr != nil {
			return authorityErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		admitted = true
		return read(rd)
	})
	closeErr := lease.Close()
	// A later failure cannot make a definite refusal eligible for publication.
	switch {
	case errors.Is(err, ErrNoticeForeign):
		return false, ErrNoticeForeign
	case errors.Is(err, grants.ErrDenied):
		return false, grants.ErrDenied
	case errors.Is(err, grants.ErrUnauthenticated):
		return false, grants.ErrUnauthenticated
	case closeErr != nil:
		return false, ErrUnavailable
	case err != nil || ctx.Err() != nil:
		return admitted, ErrUnavailable
	default:
		return admitted, nil
	}
}

type noticeAdmissionRead struct{ activityAdmissionRead }

func (r *noticeAdmissionRead) Query(q string, args ...any) (*sql.Rows, error) {
	rows, err := r.readAccess.Query(q, args...)
	if err != nil {
		r.err = err
	}
	return rows, err
}

const noticeOrigin = `incarnation=? AND origin_board=? AND origin_incarnation=?`

func noticeCut(r writer.Read, identity A.Identity, now time.Time) (noticeinterval.Cut, error) {
	cut := noticeinterval.Cut{At: A.Timestamp(now.UTC().Format(time.RFC3339))}
	var next A.Positive
	var floor sql.NullString
	if err := r.Scan(`SELECT revision,next_event,activity_floor FROM board WHERE singleton=1 AND board_id=? AND incarnation=?`,
		[]any{identity.BoardID, identity.Incarnation}, &cut.Revision, &next, &floor); err != nil {
		return cut, err
	}
	if A.Validate(next) != nil || (floor.Valid && A.Validate(A.Timestamp(floor.String)) != nil) {
		return cut, ErrUnavailable
	}
	cut.Event = A.Number(next - 1)
	cut.Floor = A.Timestamp(floor.String)
	var minimum A.Positive
	if err := r.Scan(`SELECT coalesce(max(gap_id),0),coalesce(min(gap_id),1) FROM coverage_gap WHERE `+noticeOrigin+` AND class='activity'`,
		[]any{identity.Incarnation, identity.BoardID, identity.Incarnation}, &cut.Gap, &minimum); err != nil {
		return cut, err
	}
	if !cut.Valid() || A.Validate(minimum) != nil {
		return cut, ErrUnavailable
	}
	return cut, nil
}

func captureNoticeInterval(r writer.Read, identity A.Identity, automation A.UUID, lower noticeinterval.Cut, now time.Time) (noticeinterval.Evidence, error) {
	if !lower.Valid() {
		return noticeinterval.Evidence{}, ErrUnavailable
	}
	upper, err := noticeCut(r, identity, now)
	if err != nil {
		return noticeinterval.Evidence{}, err
	}
	if !upper.Follows(lower) {
		return noticeinterval.Evidence{}, ErrNoticeForeign
	}
	evidence := noticeinterval.Evidence{Upper: &upper}
	expected := upper.Event - lower.Event
	if expected > noticeExpectedLimit {
		return evidence, ErrUnavailable
	}
	facts := noticeFacts{low: min(lower.At, upper.At), high: max(lower.At, upper.At), last: lower.Event}
	if expected != 0 {
		if err := facts.readEvents(r, identity, automation, upper); err != nil {
			return evidence, err
		}
	}
	gapAfter := A.Number(0)
	if expected == 0 {
		gapAfter = lower.Gap
	}
	gapped, err := facts.hasGap(r, identity, gapAfter, upper.Gap)
	if err != nil {
		return evidence, err
	}
	evidence.Class = facts.coverage(lower, upper, gapped)
	if evidence.Class == "" {
		evidence.Class = facts.actors.class()
		evidence.Count = &expected
	}
	return evidence, nil
}

type noticeFacts struct {
	low, high A.Timestamp
	last      A.Number
	missing   bool
	actors    noticeActors
}

func (f *noticeFacts) readEvents(r writer.Read, identity A.Identity, automation A.UUID, upper noticeinterval.Cut) error {
	const query = `SELECT ` + activityEventProjection + ` FROM activity a WHERE
		a.incarnation=? AND a.origin_board=? AND a.origin_incarnation=? AND a.event_id>? AND a.event_id<=?
		ORDER BY a.event_id ASC LIMIT ?`
	for {
		pageSize := 0
		err := searchRows(r, query, []any{identity.Incarnation, identity.BoardID, identity.Incarnation, f.last, upper.Event, T.ActivityEventLimit}, func(rows *sql.Rows) error {
			event, err := scanActivityEvent(rows)
			if err != nil {
				return err
			}
			id := A.Number(event.EventID)
			if event.Origin != identity || id <= f.last || id > upper.Event || event.BoardRevision > upper.Revision {
				return ErrUnavailable
			}
			f.missing = f.missing || id != f.last+1
			f.last = id
			f.low, f.high = min(f.low, event.At), max(f.high, event.At)
			f.actors.add(event, automation)
			pageSize++
			return nil
		})
		if err != nil {
			return err
		}
		if pageSize < T.ActivityEventLimit {
			f.missing = f.missing || f.last != upper.Event
			return nil
		}
	}
}

func (f noticeFacts) hasGap(r writer.Read, identity A.Identity, after, through A.Number) (bool, error) {
	if after == through {
		return false, nil
	}
	count := 0
	// Invalid bounds cannot establish that a candidate is outside the envelope.
	// Match the storage timestamp constraints before applying lexical coverage.
	err := searchRows(r, `SELECT DISTINCT origin_board,origin_incarnation,from_exclusive,through_inclusive,reason
		FROM coverage_gap WHERE `+noticeOrigin+` AND class='activity' AND gap_id>? AND gap_id<=?
		AND ((through_inclusive COLLATE BINARY >= ? AND
		      (from_exclusive IS NULL OR (from_exclusive COLLATE BINARY < ? AND from_exclusive <> through_inclusive)))
		     OR through_inclusive IS NULL OR length(through_inclusive)<>20
		     OR through_inclusive<'0001-01-01T00:00:00Z' OR through_inclusive>'9999-12-31T23:59:59Z'
		     OR strftime('%Y-%m-%dT%H:%M:%SZ',through_inclusive,'+0 seconds') IS NOT through_inclusive
		     OR (from_exclusive IS NOT NULL AND (length(from_exclusive)<>20
		         OR from_exclusive<'0001-01-01T00:00:00Z' OR from_exclusive>through_inclusive
		         OR strftime('%Y-%m-%dT%H:%M:%SZ',from_exclusive,'+0 seconds') IS NOT from_exclusive))) LIMIT ?`,
		[]any{identity.Incarnation, identity.BoardID, identity.Incarnation, after, through, f.low, f.high, T.ActivityGapLimit + 1}, func(rows *sql.Rows) error {
			gap, err := scanActivityGap(rows)
			if err != nil {
				return err
			}
			count++
			if gap.Origin != identity || count > T.ActivityGapLimit {
				return ErrUnavailable
			}
			return nil
		})
	return count != 0, err
}

func (f noticeFacts) coverage(lower, upper noticeinterval.Cut, gap bool) string {
	if upper.Event != lower.Event {
		if f.missing && upper.Floor >= f.high {
			return "expired"
		}
		if f.missing || upper.Floor >= f.low || gap {
			return "gapped"
		}
		return ""
	}
	// A newly advanced floor contributes (oldFloor, newFloor]. Empty old
	// floor represents an unbounded lower endpoint.
	if upper.Floor > lower.Floor && upper.Floor >= f.low && lower.Floor < f.high {
		if lower.Floor < f.low && upper.Floor >= f.high {
			return "expired"
		}
		return "gapped"
	}
	if gap {
		return "gapped"
	}
	return ""
}

type noticeActors uint8

const (
	noticeHuman noticeActors = 1 << iota
	noticeAutomation
	noticeOther
	noticeUnknown
)

func (a *noticeActors) add(event T.ActivityEvent, automation A.UUID) {
	switch event.ActorKind {
	case "human":
		*a |= noticeHuman
	case "automation":
		if *event.ActorID == automation {
			*a |= noticeAutomation
		} else {
			*a |= noticeOther
		}
	default:
		*a |= noticeUnknown
	}
}

func (a noticeActors) class() string {
	if a&noticeUnknown != 0 {
		return "unknown-actor"
	}
	switch a {
	case 0:
		return "observed-empty"
	case noticeHuman:
		return "human"
	case noticeAutomation:
		return "all-automation"
	case noticeOther:
		return "other-actor"
	default:
		return "mixed"
	}
}
