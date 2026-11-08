package local

import (
	"errors"
	"github.com/localswim/localswim/internal/board/grants"
	R "github.com/localswim/localswim/internal/board/recipient"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (r *Runtime) serveRecipient(w http.ResponseWriter, q *http.Request) {
	action := strings.TrimPrefix(q.URL.Path, r.boardPath()+"recipient/")
	var inc A.UUID
	var request T.RecipientConfigureRequest
	switch action {
	case "status":
		if q.Method != "GET" {
			refuse(w, 405, "INVALID_INPUT")
			return
		}
		query, e := url.ParseQuery(q.URL.RawQuery)
		b, be := io.ReadAll(http.MaxBytesReader(w, q.Body, 1))
		if e != nil || be != nil || len(b) != 0 || len(query) != 1 || len(query["incarnation"]) != 1 {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		inc = A.UUID(query.Get("incarnation"))
		if A.Validate(inc) != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
	case "configure":
		if q.Method != "POST" {
			refuse(w, 405, "INVALID_INPUT")
			return
		}
		b, e := io.ReadAll(http.MaxBytesReader(w, q.Body, T.RecipientRequestLimit))
		if e != nil || q.URL.RawQuery != "" || !media(q) {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		request, e = T.Decode[T.RecipientConfigureRequest](b)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		inc = request.Incarnation
	default:
		refuse(w, 404, "NOT_FOUND")
		return
	}
	if string(inc) != r.id.Incarnation {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	mode, token, ok := H.Credential(q.Context())
	if !ok {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if r.recipient == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	check := r.recipientCheck(r.noticeAuthority(mode, token))
	var v R.View
	var e error
	if action == "configure" {
		v, e = r.recipient.Configure(q.Context(), A.UUID(request.ExpectedRuntimeGeneration), request.ExpectedConfigurationRevision, A.UUID(request.RequestID), R.Input{Version: request.Input.Version, Recipient: R.Reference{Kind: string(request.Input.Recipient.Kind), ThreadID: string(request.Input.Recipient.ThreadID)}, Policy: string(request.Input.Policy)}, check)
	} else {
		v, e = r.recipient.Status(q.Context(), check)
	}
	if e != nil {
		refuseRecipient(w, e)
		return
	}
	view := T.RecipientView{Identity: A.Identity{BoardID: A.UUID(r.id.BoardID), Incarnation: inc}, RuntimeGeneration: T.UUIDv4(v.RuntimeGeneration), State: T.RecipientState(v.State)}
	if v.Reason != "" {
		reason := T.RecipientReason(v.Reason)
		view.Reason = &reason
	}
	if d := v.Record; d != nil {
		generation, id := T.UUIDv4(d.ConfigurationGeneration), T.UUIDv4(d.RequestID)
		input := T.RecipientInput{Version: 1, Recipient: T.RecipientReference{Kind: T.RecipientKind(d.Input.Recipient.Kind), ThreadID: T.RecipientThreadID(d.Input.Recipient.ThreadID)}, Policy: T.RecipientPolicy(d.Input.Policy)}
		view.ConfigurationRevision = 1
		view.ConfigurationGeneration = &generation
		view.RequestID = &id
		view.Input = &input
	}
	b, e := T.Readable(T.RecipientResponse{APIVersion: 1, Scope: "recipient", Result: view})
	if e != nil {
		if action == "configure" {
			refuseRecipient(w, R.ErrUnknown)
		} else {
			refuse(w, 503, "UNAVAILABLE")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
func refuseRecipient(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, R.ErrUnknown):
		b, err := T.Readable(T.RecipientOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: T.RecipientUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.RecipientUnknownMessage}})
		if err != nil {
			panic("invalid fixed recipient unknown")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = w.Write(b)
	case errors.Is(e, R.ErrBusy):
		refuse(w, 409, "CONTROL_BUSY")
	case errors.Is(e, R.ErrStale):
		refuse(w, 409, "CONTROL_STALE")
	case errors.Is(e, R.ErrFixed):
		refuse(w, 409, "CONFIGURATION_FIXED")
	case errors.Is(e, R.ErrInvalid):
		refuse(w, 400, "INVALID_INPUT")
	case errors.Is(e, grants.ErrUnauthenticated):
		refuse(w, 401, "UNAUTHENTICATED")
	case errors.Is(e, grants.ErrDenied):
		refuse(w, 403, "FORBIDDEN")
	default:
		refuse(w, 503, "UNAVAILABLE")
	}
}
