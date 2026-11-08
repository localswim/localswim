package local

import (
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/localswim/localswim/internal/board/grants"
	I "github.com/localswim/localswim/internal/board/installation"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
)

func (r *Runtime) serveInstallation(w http.ResponseWriter, q *http.Request) {
	if q.Method != http.MethodGet {
		refuse(w, 405, "INVALID_INPUT")
		return
	}
	query, e := url.ParseQuery(q.URL.RawQuery)
	body, be := io.ReadAll(http.MaxBytesReader(w, q.Body, 1))
	if e != nil || be != nil || len(body) != 0 || len(q.URL.RawQuery) > 256 || len(query) != 2 || len(query["incarnation"]) != 1 || len(query["expectedRuntimeGeneration"]) != 1 {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	inc := A.UUID(query.Get("incarnation"))
	generation := T.UUIDv4(query.Get("expectedRuntimeGeneration"))
	if A.Validate(inc) != nil || !uuidV4(string(generation)) {
		refuse(w, 400, "INVALID_INPUT")
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
	if r.installation == nil || r.recipientState == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	o, e := r.installation.Inspect(q.Context(), string(generation), r.recipientCheck(r.noticeAuthority(mode, token)))
	if e != nil {
		switch {
		case errors.Is(e, grants.ErrUnauthenticated):
			refuse(w, 401, "UNAUTHENTICATED")
		case errors.Is(e, grants.ErrDenied):
			refuse(w, 403, "FORBIDDEN")
		case errors.Is(e, I.ErrStale):
			refuse(w, 409, "CONTROL_STALE")
		case errors.Is(e, I.ErrBusy):
			refuse(w, 409, "CONTROL_BUSY")
		default:
			refuse(w, 503, "UNAVAILABLE")
		}
		return
	}
	response := T.InstallationInspectionResponse{APIVersion: 1, Scope: "installation-inspection", Result: T.InstallationInspectionResult{Identity: A.Identity{BoardID: A.UUID(r.id.BoardID), Incarnation: inc}, RuntimeGeneration: generation, State: T.InstallationInspectionState(o.State), Code: T.InstallationInspectionCode(o.Code)}}
	b, e := T.Readable(response)
	if e != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
