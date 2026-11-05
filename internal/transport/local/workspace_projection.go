package local

import (
	"context"
	"database/sql"
	"errors"

	"github.com/localswim/localswim/internal/board/attention"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
)

// CatalogSnapshot is an independently owned observation, never a credential or
// wire authority. No member/runtime storage is shared with its pointer or slice
// fields. All policy context and category counts share the authorized snapshot.
type CatalogSnapshot struct {
	Revision         A.Number
	ReadyForReview   A.Number // Compatibility accessor for the configured review count.
	AttentionContext T.AttentionContext
	Attention        T.CatalogAttention
	LastCardActivity *A.Timestamp
	Coverage         *A.Coverage
}

// CatalogSnapshot resolves the configured human/browser or automation/CLI read
// grant and all values in the same current snapshot. A failed read returns no
// partial observation. Retained committed card-operation metadata supplies the
// timestamp even after history/activity payload pruning, including archived-card
// edits and restored provenance. Grant, retention and expiry work never counts as
// card activity. Future committed timestamps retain their exact ordering value.
func (r *Runtime) CatalogSnapshot(ctx context.Context, channel string) (CatalogSnapshot, error) {
	var out CatalogSnapshot
	err := r.withConfiguredRead(ctx, channel, func(rd writer.Read) error {
		var hf, af, of, last sql.NullString
		if err := rd.Scan(`SELECT revision,history_floor,activity_floor,outcome_floor FROM board WHERE singleton=1 AND board_id=? AND incarnation=?`, []any{r.id.BoardID, r.id.Incarnation}, &out.Revision, &hf, &af, &of); err != nil {
			return err
		}
		current, err := attention.ReadCurrent(rd, r.id.BoardID, r.id.Incarnation)
		if err != nil {
			return err
		}
		out.Revision = current.Revision
		out.AttentionContext, out.Attention = current.AttentionContext, current.Attention
		if current.Attention.ReadyForReview.Value != nil {
			out.ReadyForReview = *current.Attention.ReadyForReview.Value
		}
		if err := rd.Scan(`SELECT max(terminal_at) FROM operation WHERE incarnation=? AND terminal_class='COMMITTED' AND committed_card_revision IS NOT NULL`, []any{r.id.Incarnation}, &last); err != nil {
			return err
		}
		out.LastCardActivity = catalogStamp(last)
		out.Coverage = &A.Coverage{HistoryFloor: catalogStamp(hf), ActivityFloor: catalogStamp(af), OutcomePayloadFloor: catalogStamp(of), Gaps: []A.Gap{}}
		rows, err := rd.Query(`SELECT origin_board,origin_incarnation,class,reason,from_exclusive,through_inclusive,recorded_at,board_revision FROM coverage_gap WHERE incarnation=? ORDER BY origin_board,origin_incarnation,class,from_exclusive,through_inclusive,reason,board_revision,recorded_at`, r.id.Incarnation)
		if err != nil {
			return err
		}
		for rows.Next() {
			var gap A.Gap
			var from sql.NullString
			if err = rows.Scan(&gap.Origin.BoardID, &gap.Origin.Incarnation, &gap.Class, &gap.Reason, &from, &gap.ThroughInclusive, &gap.RecordedAt, &gap.BoardRevision); err != nil {
				break
			}
			gap.FromExclusive = catalogStamp(from)
			out.Coverage.Gaps = append(out.Coverage.Gaps, gap)
		}
		return errors.Join(err, rows.Err(), rows.Close())
	})
	if err != nil {
		return CatalogSnapshot{}, err
	}
	return out, nil
}

func catalogStamp(s sql.NullString) *A.Timestamp {
	if !s.Valid {
		return nil
	}
	t := A.Timestamp(s.String)
	return &t
}
