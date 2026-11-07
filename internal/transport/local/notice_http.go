package local

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/noticemonitor"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
)

type noticeRequest struct {
	action     string
	selected   A.UUID
	generation A.UUID
	revision   A.Number
	target     A.UUID
	delivery   string
}

func parseNoticeRequest(w http.ResponseWriter, q *http.Request, prefix string) (noticeRequest, int) {
	action := strings.TrimPrefix(q.URL.Path, prefix)
	x := noticeRequest{action: action}
	switch action {
	case "status", "inbox":
		if q.Method != http.MethodGet {
			return x, http.StatusMethodNotAllowed
		}
		query, err := url.ParseQuery(q.URL.RawQuery)
		b, bodyErr := io.ReadAll(http.MaxBytesReader(w, q.Body, 1))
		if err != nil || len(query) != 1 || len(query["incarnation"]) != 1 || len(b) != 0 || bodyErr != nil {
			return x, http.StatusBadRequest
		}
		x.selected = A.UUID(query.Get("incarnation"))
		if A.Validate(x.selected) != nil {
			return x, http.StatusBadRequest
		}
	case "configure", "start", "stop", "lookup", "recover", "reload", "retarget":
		if q.Method != http.MethodPost {
			return x, http.StatusMethodNotAllowed
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, q.Body, T.NoticeRequestLimit))
		if err != nil || q.URL.RawQuery != "" || !media(q) {
			return x, http.StatusBadRequest
		}
		if action == "lookup" {
			body, err := T.Decode[T.NoticeLookupRequest](b)
			if err != nil {
				return x, http.StatusBadRequest
			}
			x.selected, x.target, x.delivery = body.Incarnation, A.UUID(body.TargetID), string(body.DeliveryID)
			x.generation, x.revision = A.UUID(body.ExpectedControlGeneration), body.ExpectedConfigurationRevision
		} else if action == "retarget" {
			body, err := T.Decode[T.NoticeRetargetRequest](b)
			if err != nil {
				return x, http.StatusBadRequest
			}
			x.selected, x.target = body.Incarnation, A.UUID(body.TargetID)
			x.generation, x.revision = A.UUID(body.ExpectedControlGeneration), body.ExpectedConfigurationRevision
		} else {
			body, err := T.Decode[T.NoticeControlRequest](b)
			if err != nil {
				return x, http.StatusBadRequest
			}
			x.selected, x.generation, x.revision = body.Incarnation, A.UUID(body.ExpectedControlGeneration), body.ExpectedConfigurationRevision
		}
	default:
		return x, http.StatusNotFound
	}
	return x, 0
}

func (r *Runtime) serveNotices(w http.ResponseWriter, q *http.Request) {
	x, invalid := parseNoticeRequest(w, q, r.boardPath()+"notices/")
	if invalid != 0 {
		code := "INVALID_INPUT"
		if invalid == http.StatusNotFound {
			code = "NOT_FOUND"
		}
		refuse(w, invalid, code)
		return
	}
	if string(x.selected) != r.id.Incarnation {
		refuse(w, http.StatusForbidden, "FORBIDDEN")
		return
	}
	mode, token, ok := H.Credential(q.Context())
	if !ok {
		refuse(w, http.StatusUnauthorized, "UNAUTHENTICATED")
		return
	}
	auth := r.noticeAuthority(mode, token)
	ctx := q.Context()
	m := r.notices
	if m == nil {
		refuse(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	identity := A.Identity{BoardID: A.UUID(r.id.BoardID), Incarnation: x.selected}
	var status noticemonitor.Status
	var receipt *notices.Pair
	if q.Method == http.MethodPost {
		result, err := m.Control(ctx, x.action, x.generation, x.revision, x.target, x.delivery, auth)
		if err != nil {
			refuseNotice(w, err)
			return
		}
		status, receipt = result.Status, result.Lookup
	} else {
		if err := m.Check(ctx, auth); err != nil {
			if errors.Is(err, notices.ErrForeign) && noticeForeignView(w, m, identity, x.action) {
				return
			}
			refuseNotice(w, err)
			return
		}
		status = m.Snapshot()
	}
	view := noticeView(identity, status)
	if x.action == "inbox" {
		for _, pair := range m.Inbox() {
			view.Entries = append(view.Entries, noticePair(pair))
		}
	}
	if x.action == "lookup" {
		view.Lookup = &T.NoticeLookup{State: "unknown"}
		if receipt != nil {
			pair := noticePair(*receipt)
			view.Lookup = &T.NoticeLookup{State: "accepted", Receipt: &pair}
		}
	}
	writeNoticeView(w, view)
}

func noticeForeignView(w http.ResponseWriter, m *noticemonitor.Monitor, identity A.Identity, action string) bool {
	if action != "status" && action != "inbox" && action != "lookup" {
		return false
	}
	s := m.Snapshot()
	s.Phase, s.Reason, s.State, s.Operation = "failed", "foreign", nil, ""
	view := noticeView(identity, s)
	if action == "lookup" {
		view.Lookup = &T.NoticeLookup{State: "unknown"}
	}
	writeNoticeView(w, view)
	return true
}

func writeNoticeView(w http.ResponseWriter, view T.NoticeView) {
	b, err := T.Readable(T.NoticeResponse{APIVersion: 1, Scope: "notices", Result: view})
	if err != nil {
		refuse(w, http.StatusServiceUnavailable, "UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func refuseNotice(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, noticemonitor.ErrOutcomeUnknown):
		b, encodeErr := T.Readable(T.NoticeOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: T.NoticeUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.NoticeUnknownMessage}})
		if encodeErr != nil {
			panic("invalid fixed notice unknown envelope")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(b)
	case errors.Is(err, notices.ErrConfigurationExhausted):
		refuse(w, http.StatusConflict, "CONFIGURATION_EXHAUSTED")
	case errors.Is(err, notices.ErrStale), errors.Is(err, noticemonitor.ErrStale):
		refuse(w, http.StatusConflict, "CONTROL_STALE")
	case errors.Is(err, grants.ErrUnauthenticated):
		refuse(w, http.StatusUnauthorized, "UNAUTHENTICATED")
	case errors.Is(err, grants.ErrDenied), errors.Is(err, noticemonitor.ErrAuthority):
		refuse(w, http.StatusForbidden, "FORBIDDEN")
	case errors.Is(err, noticemonitor.ErrBusy), errors.Is(err, notices.ErrGuarded):
		refuse(w, http.StatusConflict, "CONTROL_BUSY")
	case errors.Is(err, notices.ErrInvalid):
		refuse(w, http.StatusBadRequest, "INVALID_INPUT")
	default:
		refuse(w, http.StatusServiceUnavailable, "UNAVAILABLE")
	}
}

func noticePair(p notices.Pair) T.NoticePair {
	return T.NoticePair{TargetID: T.UUIDv4(p.TargetID), DeliveryID: A.Digest(p.DeliveryID),
		AcceptedAt: p.AcceptedAt, Class: T.NoticeClass(p.Class), LowerEvent: p.LowerEvent,
		UpperEvent: p.UpperEvent, EventCount: p.EventCount}
}

func noticeCut(target A.UUID, class string, lower noticeinterval.Cut, upper *noticeinterval.Cut, count *A.Number, id string) *T.NoticeCut {
	cut := &T.NoticeCut{TargetID: T.UUIDv4(target), Class: T.NoticeClass(class), LowerEvent: lower.Event, EventCount: count}
	if upper != nil {
		cut.UpperEvent = &upper.Event
	}
	if id != "" {
		digest := A.Digest(id)
		cut.DeliveryID = &digest
	}
	return cut
}

func noticeView(identity A.Identity, s noticemonitor.Status) T.NoticeView {
	view := T.NoticeView{Identity: identity, ControlGeneration: T.UUIDv4(s.Generation),
		State: T.NoticePhase(s.Phase), Entries: []T.NoticePair{}}
	if s.Operation != "" {
		operation := T.NoticeOperation(s.Operation)
		view.ControlOperation = &operation
	}
	if s.Reason != "" {
		reason := T.NoticeReason(s.Reason)
		view.Reason = &reason
	}
	if d := s.State; d != nil {
		view.ConfigurationRevision = d.ConfigurationRevision
		target := T.UUIDv4(d.TargetID)
		view.TargetID, view.Checkpoint, view.GapCount = &target, &d.Checkpoint.Event, A.Number(len(d.Gaps))
		if last := d.Last; last != nil {
			view.Last = noticeCut(last.TargetID, last.Class, last.Lower, last.Upper, last.Count, last.DeliveryID)
		}
		if pending := d.Pending; pending != nil {
			view.Pending = noticeCut(pending.TargetID, pending.Class, pending.Lower, pending.Upper, pending.Count, pending.DeliveryID)
		}
	}
	return view
}
