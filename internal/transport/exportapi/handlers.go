package exportapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	E "github.com/localswim/localswim/internal/archive/export"
	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
	H "github.com/localswim/localswim/internal/transport/https"
)

const maxBody = 4096

func refuse(w http.ResponseWriter, status int, code string) {
	b, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func request(w http.ResponseWriter, r *http.Request) (T.ExportRequest, bool) {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if e != nil {
		var too *http.MaxBytesError
		if errors.As(e, &too) {
			refuse(w, 413, "INPUT_TOO_LARGE")
		} else {
			refuse(w, 400, "INVALID_INPUT")
		}
		return T.ExportRequest{}, false
	}
	q, e := T.Decode[T.ExportRequest](b)
	if e != nil {
		refuse(w, 400, "INVALID_INPUT")
		return q, false
	}
	return q, true
}

// prepare reauthenticates the credential and both ordinary rights through A02's
// actual pinned reader. Current only selects the credential-bound actor/scope;
// neither its earlier result nor any cached rights authorize this snapshot.
func (h *Handler) prepare(ctx context.Context, board, mode string, token T.SecretToken, current T.SessionRead, q T.ExportRequest) (*E.Prepared, error) {
	a, e := h.sessions.Admission(mode, token)
	if e != nil {
		return nil, grants.ErrDenied
	}
	return E.Prepare(ctx, h.store, E.Request{Identity: A.Identity{BoardID: A.UUID(board), Incarnation: q.Incarnation}, Revision: q.ExpectedRevision, ExportedAt: A.Timestamp(time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)), Verify: func(ctx context.Context, r writer.Read) error {
		return a.Require(ctx, r, board, string(q.Incarnation), string(current.ActorID), "read", "export")
	}})
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	board, status := selectRoute(r)
	if status != 0 {
		code := "INVALID_INPUT"
		if status == 404 {
			code = "NOT_FOUND"
		}
		refuse(w, status, code)
		return
	}
	mode, token, ok := H.Credential(r.Context())
	if !ok {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	current, e := h.sessions.Current(r.Context(), mode, token)
	if e != nil {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if board != string(current.Grant.BoardID) {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	q, ok := request(w, r)
	if !ok {
		return
	}
	if q.Incarnation != current.Grant.Incarnation {
		refuse(w, 409, "INCARNATION_CONFLICT")
		return
	}
	p, e := h.prepare(r.Context(), board, mode, token, current, q)
	if e != nil {
		status, code := 503, "UNAVAILABLE"
		if errors.Is(e, E.ErrPrecondition) {
			status, code = 409, "REVISION_CONFLICT"
		} else if errors.Is(e, grants.ErrDenied) {
			status, code = 403, "FORBIDDEN"
		} else if errors.Is(e, E.ErrLimit) {
			status, code = 413, "INPUT_TOO_LARGE"
		}
		refuse(w, status, code)
		return
	}
	deliver(w, r, p)
}

func deliver(w http.ResponseWriter, r *http.Request, p *E.Prepared) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="localswim-export.zip"`)
	w.Header().Set("Content-Length", strconv.FormatInt(p.ByteLength(), 10))
	w.WriteHeader(http.StatusOK)
	if p.Write(r.Context(), w) != nil {
		// Prevent a clean HTTP end after a partial ZIP. No JSON error/trailer or
		// local-save status can be appended once delivery has started.
		panic(http.ErrAbortHandler)
	}
}
