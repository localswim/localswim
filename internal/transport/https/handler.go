// Package https enforces the installation HTTPS request boundary and exact
// session routes. Domain, grant and export adapters remain separate consumers.
package https

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/board/credentials"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/session"
	"github.com/localswim/localswim/internal/transport/setup"
)

type Handler struct {
	native     *credentials.Service
	sessions   *session.Service
	connection setup.ConnectionContext
}

func New(native *credentials.Service, sessions *session.Service) (*Handler, error) {
	if native == nil || sessions == nil {
		return nil, session.ErrDenied
	}
	c := native.Context()
	if c.Authority == "" || c.Origin == "" {
		return nil, session.ErrDenied
	}
	return &Handler{native, sessions, c}, nil
}
func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
func refuse(w http.ResponseWriter, status int, code string) {
	b, e := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
	if e != nil {
		http.Error(w, "Request refused", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func write(w http.ResponseWriter, status int, b []byte, e error) {
	if e != nil {
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func bearer(r *http.Request) (T.SecretToken, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	token := T.SecretToken(strings.TrimPrefix(values[0], "Bearer "))
	if len(string(token)) != 43 || strings.ContainsAny(string(token), " \t\r\n,") {
		return "", false
	}
	return token, true
}
func exactHeader(r *http.Request, key, want string) bool {
	v := r.Header.Values(key)
	return len(v) == 1 && v[0] == want
}
func jsonBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	value, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || value != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
		return nil, session.ErrDenied
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if e != nil {
		return nil, session.ErrDenied
	}
	return b, nil
}
func emptyBody(w http.ResponseWriter, r *http.Request) bool {
	b, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1))
	return e == nil && len(b) == 0
}

type authentication struct {
	mode  string
	token T.SecretToken
}
type authKey struct{}

// Credential is supplied only after boundary authentication. Adapters must
// still invoke G01/S02 in their current snapshot before performing any action.
func Credential(ctx context.Context) (string, T.SecretToken, bool) {
	a, ok := ctx.Value(authKey{}).(authentication)
	return a.mode, a.token, ok
}

func (h *Handler) boundary(w http.ResponseWriter, r *http.Request) bool {
	headers(w)
	if h == nil || !h.native.Active() {
		refuse(w, 503, "UNAVAILABLE")
		return false
	}
	return connectionBoundary(w, r, h.connection)
}

func connectionBoundary(w http.ResponseWriter, r *http.Request, c setup.ConnectionContext) bool {
	if r.TLS == nil || r.Host != c.Authority || r.URL.IsAbs() || r.URL.RawPath != "" || r.URL.Fragment != "" {
		refuse(w, 400, "INVALID_INPUT")
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/session") && (r.URL.RawQuery != "" || r.URL.ForceQuery) {
		refuse(w, 400, "INVALID_INPUT")
		return false
	}
	if r.Method == http.MethodOptions || r.Header.Get("Access-Control-Request-Method") != "" {
		refuse(w, 403, "FORBIDDEN")
		return false
	}
	if len(r.Header.Values("Origin")) > 0 && !exactHeader(r, "Origin", c.Origin) {
		refuse(w, 403, "FORBIDDEN")
		return false
	}
	return true
}

// Native supplies only the common transport boundary to dedicated native
// adapters. Bearer shape conveys NO authenticated principal, board rights or
// lifecycle proof. The adapter MUST call G02 SubmitNative/NativeLookup (or the
// exact guarded issue method) before observing outcomes or performing effects.
func (h *Handler) Native(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.boundary(w, r) {
			return
		}
		if _, ok := bearer(r); !ok {
			refuse(w, 401, "UNAUTHENTICATED")
			return
		}
		if r.Method != http.MethodGet && !writeMedia(r) {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		if next == nil {
			refuse(w, 404, "NOT_FOUND")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func writeMedia(r *http.Request) bool {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || len(r.Header.Values("Content-Type")) != 1 {
		return false
	}
	if media == "application/json" {
		return true
	}
	parts := strings.Split(r.URL.Path, "/")
	return r.Method == http.MethodPost && media == "multipart/form-data" && len(parts) == 8 && parts[1] == "api" && parts[2] == "v1" && parts[3] == "boards" && schema.ValidUUID(parts[4]) && parts[5] == "cards" && schema.ValidUUID(parts[6]) && parts[7] == "attachments"
}

// Wrap authenticates ordinary browser/CLI consumers behind the shared boundary.
// Session queries refuse; later adapters validate their own exact public queries.
func (h *Handler) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.boundary(w, r) {
			return
		}
		present := len(r.Header.Values("Origin")) > 0
		exchange := r.URL.Path == "/api/v1/session/exchange"
		native := r.URL.Path == "/api/v1/session/codes" || r.URL.Path == "/api/v1/session/cli-credentials"
		if exchange {
			if !present {
				refuse(w, 403, "FORBIDDEN")
				return
			}
			h.session(w, r, "browser", "")
			return
		}
		token, ok := bearer(r)
		if !ok {
			refuse(w, 401, "UNAUTHENTICATED")
			return
		}
		mode := "browser"
		if native {
			mode = "native-owner"
		} else if !present {
			if _, e := h.sessions.Current(r.Context(), "cli", token); e == nil {
				mode = "cli"
			} else if r.Method != http.MethodGet || !exactHeader(r, "Sec-Fetch-Site", "same-origin") {
				refuse(w, 403, "FORBIDDEN")
				return
			}
		}
		if !native {
			if _, e := h.sessions.Current(r.Context(), mode, token); e != nil {
				// CLI credentials may also carry an exact Origin, but browser credentials
				// never gain the absent-Origin native exception from a header claim.
				if !present {
					refuse(w, 401, "UNAUTHENTICATED")
					return
				}
				if _, e = h.sessions.Current(r.Context(), "cli", token); e != nil {
					refuse(w, 401, "UNAUTHENTICATED")
					return
				}
				mode = "cli"
			}
		}
		if r.Method != http.MethodGet && !writeMedia(r) {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		if native {
			h.session(w, r, mode, token)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), authKey{}, authentication{mode, token}))
		if strings.HasPrefix(r.URL.Path, "/api/v1/session") {
			h.session(w, r, mode, token)
			return
		}
		if next == nil {
			refuse(w, 404, "NOT_FOUND")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.Wrap(nil).ServeHTTP(w, r) }
func (h *Handler) session(w http.ResponseWriter, r *http.Request, mode string, token T.SecretToken) {
	switch r.URL.Path {
	case "/api/v1/session":
		switch r.Method {
		case http.MethodGet:
			if !emptyBody(w, r) {
				refuse(w, 400, "INVALID_INPUT")
				return
			}
			view, e := h.sessions.Current(r.Context(), mode, token)
			if e != nil {
				refuse(w, 401, "UNAUTHENTICATED")
				return
			}
			b, e := T.Readable(view)
			write(w, 200, b, e)
		case http.MethodDelete:
			if mode == "browser" && !exactHeader(r, "Origin", h.connection.Origin) {
				refuse(w, 403, "FORBIDDEN")
				return
			}
			media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 {
				refuse(w, 400, "INVALID_INPUT")
				return
			}
			if !emptyBody(w, r) {
				refuse(w, 400, "INVALID_INPUT")
				return
			}
			if h.sessions.Revoke(r.Context(), mode, token) != nil {
				refuse(w, 401, "UNAUTHENTICATED")
				return
			}
			b, e := T.Readable(T.SessionRevoked{APIVersion: 1, Scope: "session", Outcome: "revoked"})
			write(w, 200, b, e)
		default:
			refuse(w, 405, "INVALID_INPUT")
		}
	case "/api/v1/session/codes", "/api/v1/session/cli-credentials":
		if r.Method != http.MethodPost {
			refuse(w, 405, "INVALID_INPUT")
			return
		}
		b, e := jsonBody(w, r)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		req, e := T.Decode[T.CodeRequest](b)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		if r.URL.Path == "/api/v1/session/codes" {
			out, e := h.sessions.IssueCode(r.Context(), token, req)
			if e != nil {
				refuse(w, 401, "UNAUTHENTICATED")
				return
			}
			b, e = T.MarshalEphemeral(out)
			write(w, 201, b, e)
		} else {
			out := h.native.Issue(r.Context(), token, req)
			if out.Status != credentials.Confirmed {
				if out.Status == credentials.Refused {
					refuse(w, 401, "UNAUTHENTICATED")
				} else {
					refuse(w, 503, "UNAVAILABLE")
				}
				return
			}
			b, e = out.MarshalEphemeral()
			write(w, 201, b, e)
		}
	case "/api/v1/session/exchange":
		if r.Method != http.MethodPost {
			refuse(w, 405, "INVALID_INPUT")
			return
		}
		b, e := jsonBody(w, r)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		req, e := T.Decode[T.ExchangeRequest](b)
		if e != nil {
			refuse(w, 400, "INVALID_INPUT")
			return
		}
		out, e := h.sessions.Exchange(r.Context(), req.Code)
		if e != nil {
			refuse(w, 401, "UNAUTHENTICATED")
			return
		}
		b, e = T.MarshalEphemeral(out)
		write(w, 200, b, e)
	default:
		refuse(w, 404, "NOT_FOUND")
	}
}
