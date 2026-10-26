package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/board/credentials"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/transport/attachments"
	"github.com/localswim/localswim/internal/transport/boardapi"
	"github.com/localswim/localswim/internal/transport/exportapi"
	"github.com/localswim/localswim/internal/transport/grantapi"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/session"
)

var errComposition = errors.New("service composition unavailable")

// Each owned adapter retains its exact method/path parsing and common T01
// boundary. Do not wrap these adapters again: native-owner endpoints have their
// own narrow branches and ordinary requests must preserve their original mode.
func routes(store *S.Store, native *credentials.Service, sessions *session.Service, assets http.Handler) (http.Handler, error) {
	t, e := H.New(native, sessions)
	if e != nil || assets == nil {
		return nil, errComposition
	}
	b, e := boardapi.New(store, native, sessions)
	if e != nil {
		return nil, errComposition
	}
	a, e := attachments.New(store, native, sessions)
	if e != nil {
		return nil, errComposition
	}
	g, e := grantapi.New(store, native, sessions)
	if e != nil {
		return nil, errComposition
	}
	x, e := exportapi.New(store, native, sessions)
	if e != nil {
		return nil, errComposition
	}
	return dispatchRoutes(t, b, a, g, x, staticBootstrap(native, assets)), nil
}

func staticBootstrap(native *credentials.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && (r.URL.Path == "/" || r.URL.Path == "/index.html") && r.URL.RawQuery == "" && !r.URL.ForceQuery && r.URL.RawPath == "" && native.Active() {
			w.Header().Set("X-Localswim-Installation", native.Context().InstallationID)
		}
		next.ServeHTTP(w, r)
	})
}

// Selection is deliberately not http.ServeMux: it must not clean paths, redirect
// trailing slashes or turn escaped aliases into accepted public routes. Selection
// grants no authority and never substitutes another adapter after a refusal.
func dispatchRoutes(t, b, a, g, x, assets http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Split(r.URL.Path, "/")
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/session"):
			t.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/v1/grants"):
			g.ServeHTTP(w, r)
		case len(p) >= 4 && p[1] == "api" && p[2] == "v1" && p[3] == "boards":
			switch {
			case len(p) >= 6 && p[5] == "attachments", len(p) >= 8 && p[5] == "cards" && p[7] == "attachments":
				a.ServeHTTP(w, r)
			case len(p) >= 6 && p[5] == "exports":
				x.ServeHTTP(w, r)
			default:
				b.ServeHTTP(w, r)
			}
		case strings.HasPrefix(r.URL.Path, "/api/"):
			t.ServeHTTP(w, r)
		default:
			assets.ServeHTTP(w, r)
		}
	})
}
