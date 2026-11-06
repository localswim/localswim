package boardapi

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

type activityRequest struct {
	incarnation   A.UUID
	from, through A.Timestamp
	cursor        string
}

func parseActivity(raw string, now time.Time) (activityRequest, error) {
	var request activityRequest
	if len(raw) > 4096 || strings.HasPrefix(raw, "&") || strings.HasSuffix(raw, "&") || strings.Contains(raw, "&&") {
		return request, ErrUnavailable
	}
	params, err := url.ParseQuery(raw)
	if err != nil {
		return request, err
	}
	for name, values := range params {
		if len(values) != 1 || values[0] == "" {
			return request, ErrUnavailable
		}
		switch name {
		case "incarnation":
			request.incarnation = A.UUID(values[0])
		case "from":
			request.from = A.Timestamp(values[0])
		case "through":
			request.through = A.Timestamp(values[0])
		case "cursor":
			request.cursor = values[0]
		default:
			return request, ErrUnavailable
		}
	}
	if !schema.ValidUUID(string(request.incarnation)) {
		return request, ErrUnavailable
	}
	if request.cursor != "" {
		if request.from != "" || request.through != "" || !T.ValidActivityCursor(request.cursor) {
			return request, ErrUnavailable
		}
		return request, nil
	}
	now = now.UTC().Truncate(time.Second)
	window := T.ActivityWindowSeconds * time.Second
	if request.from == "" && request.through == "" {
		request.from = A.Timestamp(now.Add(-window).Format(time.RFC3339))
		request.through = A.Timestamp(now.Format(time.RFC3339))
		return request, nil
	}
	from, errFrom := time.Parse(time.RFC3339, string(request.from))
	through, errThrough := time.Parse(time.RFC3339, string(request.through))
	if errFrom != nil || errThrough != nil || from.Year() < 1 || through.Year() < 1 ||
		from.UTC().Format(time.RFC3339) != string(request.from) || through.UTC().Format(time.RFC3339) != string(request.through) ||
		!from.Before(through) || through.Sub(from) > window || through.After(now) {
		return request, ErrUnavailable
	}
	return request, nil
}

type activityPosition struct {
	At      A.Timestamp `json:"at"`
	Origin  A.Identity  `json:"origin"`
	EventID A.Positive  `json:"eventId"`
}

type activityCursor struct {
	Version         int              `json:"version"`
	Authority       string           `json:"authority"`
	Identity        A.Identity       `json:"identity"`
	Revision        A.Number         `json:"revision"`
	LocalEventUpper A.Number         `json:"localEventUpper"`
	From            A.Timestamp      `json:"from"`
	Through         A.Timestamp      `json:"through"`
	Coverage        string           `json:"coverage"`
	Last            activityPosition `json:"last"`
}

func activityDigest(value any) string {
	b, _ := json.Marshal(value)
	digest := sha256.Sum256(b)
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func (h *Handler) activityTag(payload []byte) []byte {
	mac := hmac.New(sha256.New, h.cursorKey[:])
	_, _ = mac.Write([]byte("localswim/activity/v1\x00"))
	_, _ = mac.Write(h.cursorEpoch[:])
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

func (h *Handler) encodeActivityCursor(p activityCursor) string {
	b, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.activityTag(b))
}

func (h *Handler) decodeActivityCursor(token string, expected activityCursor) (activityCursor, error) {
	var page activityCursor
	// Query parsing has already verified the bounded canonical base64url form.
	payload, tag, _ := strings.Cut(token, ".")
	b, _ := base64.RawURLEncoding.DecodeString(payload)
	sig, _ := base64.RawURLEncoding.DecodeString(tag)
	if !hmac.Equal(sig, h.activityTag(b)) || json.Unmarshal(b, &page) != nil {
		return page, errCursorStale
	}
	canonical, _ := json.Marshal(page)
	if !bytes.Equal(b, canonical) || page.Version != expected.Version ||
		page.Authority != expected.Authority || page.Identity != expected.Identity || page.Revision != expected.Revision ||
		page.LocalEventUpper != expected.LocalEventUpper {
		return page, errCursorStale
	}
	return page, nil
}

// Some ordinary verifiers deliberately report indistinguishable refusals to
// their authentication callers. Remember a failed storage read separately so
// the activity reader does not turn a broken snapshot into a credential result.
type activityAdmissionRead struct {
	readAccess
	err error
}

func (r *activityAdmissionRead) Scan(q string, args []any, dest ...any) error {
	err := r.readAccess.Scan(q, args, dest...)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		r.err = err
	}
	return err
}

func (h *Handler) readActivity(w http.ResponseWriter, r *http.Request, q route, mode string, current T.SessionRead, admission *grants.Service) {
	request, err := parseActivity(r.URL.RawQuery, time.Now())
	if err != nil {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	if request.incarnation != current.Grant.Incarnation {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	lease, err := h.store.Reader(ctx)
	if err != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	var page T.ActivityRead
	err = lease.Do(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "BEGIN"); err != nil {
			return err
		}
		rd := readAccess{ctx, c}
		// A deferred BEGIN does not yet pin SQLite's read snapshot. Pin the
		// selected board before checking an in-memory browser or local token.
		var selected int
		if err := rd.Scan("SELECT singleton FROM board WHERE singleton=1 AND board_id=? AND incarnation=?",
			[]any{q.board, request.incarnation}, &selected); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return grants.ErrDenied
			}
			return err
		}
		authorityRead := &activityAdmissionRead{readAccess: rd}
		admissionErr := admission.RequireCurrent(ctx, authorityRead, q.board, string(request.incarnation), string(current.ActorID), mode, string(current.Grant.GrantID), "read")
		if err := errors.Join(ctx.Err(), authorityRead.err); err != nil {
			return err
		}
		if admissionErr != nil {
			return admissionErr
		}
		authority := activityDigest([3]string{mode, string(current.ActorID), string(current.Grant.GrantID)})
		var err error
		page, err = h.activity(rd, A.Identity{BoardID: A.UUID(q.board), Incarnation: request.incarnation}, authority, request)
		return err
	})
	if closeErr := lease.Close(); closeErr != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	if err != nil {
		code := "UNAVAILABLE"
		switch {
		case errors.Is(err, grants.ErrUnauthenticated):
			code = "UNAUTHENTICATED"
		case errors.Is(err, grants.ErrDenied):
			code = "FORBIDDEN"
		case errors.Is(err, errCursorStale):
			code = "CURSOR_STALE"
		}
		refuse(w, statusFor(code), code)
		return
	}
	b, err := T.Readable(page)
	if err != nil || len(b) > T.ActivityReadResponseLimit {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	respondJSON(w, 200, b)
}

func (h *Handler) activity(r writer.Read, identity A.Identity, authority string, request activityRequest) (T.ActivityRead, error) {
	page := T.ActivityRead{APIVersion: 1, Scope: "activity"}
	view := &page.Result
	var next A.Positive
	var floor sql.NullString
	if err := r.Scan(`SELECT board_id,incarnation,revision,next_event,activity_floor FROM board WHERE singleton=1 AND board_id=? AND incarnation=?`,
		[]any{identity.BoardID, identity.Incarnation}, &view.Identity.BoardID, &view.Identity.Incarnation, &view.Revision, &next, &floor); err != nil {
		return page, err
	}
	if view.Identity != identity || A.Validate(next) != nil || A.Validate(view.Revision) != nil {
		return page, ErrUnavailable
	}
	view.LocalEventUpper = A.Number(next - 1)
	view.From, view.Through = request.from, request.through
	capture := activityCursor{Version: 1, Authority: authority,
		Identity: view.Identity, Revision: view.Revision, LocalEventUpper: view.LocalEventUpper}
	var last activityPosition
	var priorCoverage string
	if request.cursor != "" {
		prior, err := h.decodeActivityCursor(request.cursor, capture)
		if err != nil {
			return page, err
		}
		view.From, view.Through = prior.From, prior.Through
		last, priorCoverage = prior.Last, prior.Coverage
	}
	if floor.Valid {
		v := A.Timestamp(floor.String)
		if A.Validate(v) != nil {
			return page, ErrUnavailable
		}
		view.Coverage.ActivityFloor = &v
	}
	if err := projectActivityCoverage(r, view); err != nil {
		return page, err
	}
	if len(view.Coverage.Gaps) > T.ActivityGapLimit {
		if request.cursor != "" {
			return page, errCursorStale
		}
		return page, ErrUnavailable
	}
	capture.Coverage = activityDigest(view.Coverage)
	if request.cursor != "" && priorCoverage != capture.Coverage {
		return page, errCursorStale
	}
	if err := projectActivityEvents(r, view, last); err != nil {
		return page, err
	}
	if len(view.Events) > T.ActivityEventLimit {
		view.Events = view.Events[:T.ActivityEventLimit]
		lastEvent := view.Events[len(view.Events)-1]
		capture.From, capture.Through = view.From, view.Through
		capture.Last = activityPosition{At: lastEvent.At, Origin: lastEvent.Origin, EventID: lastEvent.EventID}
		token := h.encodeActivityCursor(capture)
		view.NextCursor = &token
	}
	return page, nil
}

func projectActivityCoverage(r writer.Read, view *T.ActivityView) error {
	coverage := &view.Coverage
	coverage.Gaps = []T.ActivityGap{}
	err := searchRows(r, `SELECT DISTINCT origin_board,origin_incarnation,from_exclusive,through_inclusive,reason
		FROM coverage_gap WHERE incarnation=? AND class='activity' AND through_inclusive COLLATE BINARY > ?
		AND (from_exclusive IS NULL OR (from_exclusive COLLATE BINARY < ? AND from_exclusive <> through_inclusive))
		ORDER BY origin_board COLLATE BINARY,origin_incarnation COLLATE BINARY,from_exclusive COLLATE BINARY,
		through_inclusive COLLATE BINARY,reason COLLATE BINARY LIMIT ?`,
		[]any{view.Identity.Incarnation, view.From, view.Through, T.ActivityGapLimit + 1}, func(rows *sql.Rows) error {
			var gap T.ActivityGap
			var from sql.NullString
			if err := rows.Scan(&gap.Origin.BoardID, &gap.Origin.Incarnation, &from, &gap.ThroughInclusive, &gap.Reason); err != nil {
				return err
			}
			if from.Valid {
				lower := A.Timestamp(from.String)
				gap.FromExclusive = &lower
			}
			if err := T.Validate(gap); err != nil {
				return err
			}
			coverage.Gaps = append(coverage.Gaps, gap)
			return nil
		})
	if err != nil {
		return err
	}
	coverage.Status = "complete"
	if coverage.ActivityFloor != nil && *coverage.ActivityFloor >= view.Through {
		coverage.Status = "expired"
	} else if len(coverage.Gaps) > 0 || (coverage.ActivityFloor != nil && *coverage.ActivityFloor > view.From) {
		coverage.Status = "gapped"
	}
	return nil
}

func projectActivityEvents(r writer.Read, view *T.ActivityView, last activityPosition) error {
	view.Events = []T.ActivityEvent{}
	query := `SELECT a.origin_board,a.origin_incarnation,a.event_id,a.at,a.type,a.board_revision,a.actor_id,
		(SELECT CASE WHEN count(*)=1 THEN min(kind) END FROM actor WHERE incarnation=a.incarnation AND actor_id=a.actor_id)
		FROM activity a WHERE a.incarnation=? AND a.at COLLATE BINARY > ? AND a.at COLLATE BINARY <= ?
		AND (a.origin_board<>? OR a.origin_incarnation<>? OR a.event_id<=?)`
	args := []any{view.Identity.Incarnation, view.From, view.Through, view.Identity.BoardID, view.Identity.Incarnation, view.LocalEventUpper}
	if last.EventID != 0 {
		query += ` AND (a.at COLLATE BINARY,a.origin_board COLLATE BINARY,a.origin_incarnation COLLATE BINARY,a.event_id) < (?,?,?,?)`
		args = append(args, last.At, last.Origin.BoardID, last.Origin.Incarnation, last.EventID)
	}
	query += ` ORDER BY a.at COLLATE BINARY DESC,a.origin_board COLLATE BINARY DESC,a.origin_incarnation COLLATE BINARY DESC,a.event_id DESC LIMIT ?`
	args = append(args, T.ActivityEventLimit+1)
	return searchRows(r, query, args, func(rows *sql.Rows) error {
		var event T.ActivityEvent
		var actorID, kind sql.NullString
		if err := rows.Scan(&event.Origin.BoardID, &event.Origin.Incarnation, &event.EventID, &event.At, &event.Type, &event.BoardRevision, &actorID, &kind); err != nil {
			return err
		}
		event.ActorKind = "unknown"
		if actorID.Valid && schema.ValidUUID(actorID.String) {
			event.ActorID = uuidPtr(actorID)
			if kind.Valid && (kind.String == "human" || kind.String == "automation") {
				event.ActorKind = T.ActivityActorKind(kind.String)
			}
		}
		if err := T.Validate(event); err != nil {
			return err
		}
		view.Events = append(view.Events, event)
		return nil
	})
}
