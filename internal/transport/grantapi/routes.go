// Package grantapi adapts only the accepted grant create and revoke routes.
// It supplies no enrollment, credential issuance or outcome lookup endpoint.
package grantapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/board/credentials"
	"github.com/localswim/localswim/internal/contract"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
)

var ErrUnavailable = errors.New("grant transport unavailable")

type Handler struct {
	store    *S.Store
	native   *credentials.Service
	sessions *session.Service
	boundary *H.Handler
	guarded  http.Handler
}

// New borrows the composition owner's existing board and native lifetime.
// Every mounted request passes T01's common boundary before authentication or
// scope resolution; P01 remains responsible for the executable root mount.
func New(store *S.Store, native *credentials.Service, sessions *session.Service) (*Handler, error) {
	if store == nil || native == nil || sessions == nil {
		return nil, ErrUnavailable
	}
	b, err := H.New(native, sessions)
	if err != nil {
		return nil, ErrUnavailable
	}
	h := &Handler{store: store, native: native, sessions: sessions, boundary: b}
	h.guarded = b.Native(http.HandlerFunc(h.dispatch))
	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.guarded == nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	h.guarded.ServeHTTP(w, r)
}

// selectRoute has no cleaning, redirect, prefix aliases or query semantics.
func selectRoute(r *http.Request) (contract.Command, string, int) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		return "", "", 400
	}
	if r.URL.Path == "/api/v1/grants" {
		if r.Method != http.MethodPost {
			return "", "", 405
		}
		return "grant.create", "", 0
	}
	const prefix = "/api/v1/grants/"
	if strings.HasPrefix(r.URL.Path, prefix) {
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if !schema.ValidUUID(id) {
			return "", "", 404
		}
		if r.Method != http.MethodDelete {
			return "", "", 405
		}
		return "grant.revoke", id, 0
	}
	return "", "", 404
}

func (h *Handler) dispatch(w http.ResponseWriter, r *http.Request) {
	command, target, status := selectRoute(r)
	if status != 0 {
		code := "INVALID_INPUT"
		if status == 404 {
			code = "NOT_FOUND"
		}
		refuse(w, status, code)
		return
	}
	token, ok := bearer(r)
	if !ok {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	// Current is credential authentication, not command authorization. T01 Wrap
	// must subsequently validate the browser/CLI request mode and authenticate
	// again. Only the real native verifier can admit the other branch.
	mode := "cli"
	if len(r.Header.Values("Origin")) > 0 {
		mode = "browser"
	}
	_, err := h.sessions.Current(r.Context(), mode, token)
	if err != nil && mode == "browser" {
		_, err = h.sessions.Current(r.Context(), "cli", token)
	}
	if err == nil {
		h.boundary.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.ordinary(w, r, command, target) })).ServeHTTP(w, r)
		return
	}
	h.owner(w, r, command, target, token)
}
