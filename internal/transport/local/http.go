package local

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
)

func exact(r *http.Request, key, want string) bool {
	v := r.Header.Values(key)
	return len(v) == 1 && v[0] == want
}
func refuse(w http.ResponseWriter, status int, code string) {
	b, _ := T.Readable(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func headers(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
func (r *Runtime) cookieName() string {
	return "localswim_" + strings.ReplaceAll(r.id.InstallationID, "-", "") + "_session"
}
func (r *Runtime) boundary(w http.ResponseWriter, q *http.Request) bool {
	headers(w)
	if q.TLS != nil || q.Host != strings.TrimPrefix(r.Origin(), "http://") || q.URL.IsAbs() || q.URL.RawPath != "" || q.URL.Fragment != "" || q.URL.ForceQuery {
		refuse(w, 400, "INVALID_INPUT")
		return false
	}
	if q.Method == http.MethodOptions || len(q.Header.Values("Access-Control-Request-Method")) > 0 || len(q.Header.Values("Access-Control-Request-Headers")) > 0 {
		refuse(w, 403, "FORBIDDEN")
		return false
	}
	if len(q.Header.Values("Origin")) > 0 && !exact(q, "Origin", r.Origin()) {
		refuse(w, 403, "FORBIDDEN")
		return false
	}
	if len(q.Header.Values("Content-Encoding")) > 0 || len(q.Header.Values("Content-Transfer-Encoding")) > 0 {
		refuse(w, 400, "INVALID_INPUT")
		return false
	}
	return true
}
func media(q *http.Request) bool {
	m, _, e := mime.ParseMediaType(q.Header.Get("Content-Type"))
	if e != nil || len(q.Header.Values("Content-Type")) != 1 {
		return false
	}
	return m == "application/json" || m == "multipart/form-data" && q.Method == "POST" && strings.HasSuffix(q.URL.Path, "/attachments")
}

// sessionCookie parses every raw Cookie field before selecting an auth channel.
// net/http.Request.Cookies deliberately discards malformed pairs; that forgiving
// behavior must not turn a malformed duplicate into a single human credential.
func sessionCookie(q *http.Request, name string) (value string, present, ok bool) {
	seen := map[string]bool{}
	for _, field := range q.Header.Values("Cookie") {
		for _, part := range strings.Split(field, ";") {
			part = strings.Trim(part, " \t")
			key, raw, found := strings.Cut(part, "=")
			if !found || key == "" || seen[key] {
				return "", false, false
			}
			for _, c := range []byte(key) {
				if c <= 32 || c >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(c)) {
					return "", false, false
				}
			}
			seen[key] = true
			if strings.HasPrefix(raw, "\"") {
				if len(raw) < 2 || raw[len(raw)-1] != '"' {
					return "", false, false
				}
				raw = raw[1 : len(raw)-1]
			}
			for _, c := range []byte(raw) {
				if !(c == 0x21 || c >= 0x23 && c <= 0x2b || c >= 0x2d && c <= 0x3a || c >= 0x3c && c <= 0x5b || c >= 0x5d && c <= 0x7e) {
					return "", false, false
				}
			}
			if key == name {
				value, present = raw, true
			}
		}
	}
	return value, present, true
}

func (r *Runtime) authenticate(w http.ResponseWriter, q *http.Request) (string, T.SecretToken, bool) {
	cookie, hasCookie, validCookies := sessionCookie(q, r.cookieName())
	if !validCookies {
		refuse(w, 401, "UNAUTHENTICATED")
		return "", "", false
	}
	auth := q.Header.Values("Authorization")
	browser := q.Header.Values("X-Localswim-Client")
	var mode string
	var token T.SecretToken
	if len(auth) > 0 {
		if len(auth) != 1 || len(q.Header.Values("Cookie")) > 0 || len(browser) > 0 || len(q.Header.Values("Origin")) > 0 || !strings.HasPrefix(auth[0], "Bearer ") {
			refuse(w, 401, "UNAUTHENTICATED")
			return "", "", false
		}
		raw := strings.TrimPrefix(auth[0], "Bearer ")
		if !validToken(raw) {
			refuse(w, 401, "UNAUTHENTICATED")
			return "", "", false
		}
		mode = "cli"
		token = T.SecretToken(raw)
	} else {
		if !hasCookie || !exact(q, "X-Localswim-Client", "browser") || !validToken(cookie) {
			refuse(w, 401, "UNAUTHENTICATED")
			return "", "", false
		}
		if q.Method != "GET" && !exact(q, "Origin", r.Origin()) || q.Method == "GET" && len(q.Header.Values("Origin")) == 0 && !exact(q, "Sec-Fetch-Site", "same-origin") {
			refuse(w, 403, "FORBIDDEN")
			return "", "", false
		}
		mode = "browser"
		token = T.SecretToken(cookie)
	}
	if _, e := r.auth.Current(q.Context(), mode, token); e != nil {
		refuse(w, 401, "UNAUTHENTICATED")
		return "", "", false
	}
	if q.Method != "GET" && !media(q) {
		refuse(w, 400, "INVALID_INPUT")
		return "", "", false
	}
	return mode, token, true
}

// Wrap is mandatory for every domain adapter. It resolves a live ephemeral
// credential before dispatch; G01/S02 revalidate it and durable rights again.
func (r *Runtime) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if !r.boundary(w, q) {
			return
		}
		mode, token, ok := r.authenticate(w, q)
		if !ok {
			return
		}
		next.ServeHTTP(w, q.WithContext(H.WithCredential(q.Context(), mode, token)))
	})
}
func (r *Runtime) bootstrap(w http.ResponseWriter, q *http.Request) {
	if q.Method != "POST" || q.URL.RawQuery != "" {
		refuse(w, 405, "INVALID_INPUT")
		return
	}
	cookie, hasCookie, validCookies := sessionCookie(q, r.cookieName())
	if !validCookies {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	if !exact(q, "Origin", r.Origin()) || !exact(q, "X-Localswim-Client", "browser") || len(q.Header.Values("Authorization")) > 0 {
		refuse(w, 403, "FORBIDDEN")
		return
	}
	if !media(q) || !strings.HasPrefix(q.Header.Get("Content-Type"), "application/json") {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	b, e := io.ReadAll(http.MaxBytesReader(w, q.Body, 128))
	var empty struct{}
	if e != nil || decode(b, &empty) != nil || strings.TrimSpace(string(b)) != "{}" {
		refuse(w, 400, "INVALID_INPUT")
		return
	}
	var t T.SecretToken
	if hasCookie && validToken(cookie) {
		if _, e = r.auth.Current(q.Context(), "browser", T.SecretToken(cookie)); e == nil {
			t = T.SecretToken(cookie)
		}
	}
	if t == "" {
		t, e = r.auth.browser()
		if e != nil {
			refuse(w, 503, "UNAVAILABLE")
			return
		}
	}
	view, e := r.auth.Current(q.Context(), "browser", t)
	if e != nil {
		refuse(w, 401, "UNAUTHENTICATED")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: r.cookieName(), Value: string(t), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	out := struct {
		APIVersion     int           `json:"apiVersion"`
		InstallationID string        `json:"installationId"`
		BoardID        string        `json:"boardId"`
		Incarnation    string        `json:"incarnation"`
		Epoch          string        `json:"epoch"`
		Session        T.SessionRead `json:"session"`
	}{1, r.id.InstallationID, r.id.BoardID, r.id.Incarnation, r.epoch, view}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
func (r *Runtime) ServeHTTP(w http.ResponseWriter, q *http.Request) {
	r.reqMu.Lock()
	if r.closing {
		r.reqMu.Unlock()
		headers(w)
		refuse(w, 503, "UNAVAILABLE")
		return
	}
	r.requests.Add(1)
	r.reqMu.Unlock()
	defer r.requests.Done()
	if !r.boundary(w, q) {
		return
	}
	if q.URL.Path == "/api/v1/local/bootstrap" {
		r.bootstrap(w, q)
		return
	}
	if q.URL.Path == "/api/v1/session" {
		r.Wrap(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			if q.Method != "GET" || q.URL.RawQuery != "" {
				refuse(w, 405, "INVALID_INPUT")
				return
			}
			b, e := io.ReadAll(http.MaxBytesReader(w, q.Body, 1))
			if e != nil || len(b) != 0 {
				refuse(w, 400, "INVALID_INPUT")
				return
			}
			mode, t, _ := H.Credential(q.Context())
			view, e := r.auth.Current(q.Context(), mode, t)
			if e != nil {
				refuse(w, 401, "UNAUTHENTICATED")
				return
			}
			b, e = T.Readable(view)
			if e != nil {
				refuse(w, 503, "UNAVAILABLE")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		})).ServeHTTP(w, q)
		return
	}
	if strings.HasPrefix(q.URL.Path, "/api/") {
		p := strings.Split(q.URL.Path, "/")
		if len(p) >= 5 && p[1] == "api" && p[2] == "v1" && p[3] == "boards" {
			switch {
			case len(p) >= 6 && p[5] == "exports":
				r.export.ServeHTTP(w, q)
			case len(p) >= 6 && p[5] == "attachments" || len(p) >= 8 && p[7] == "attachments":
				r.attachment.ServeHTTP(w, q)
			default:
				r.board.ServeHTTP(w, q)
			}
			return
		}
		refuse(w, 404, "NOT_FOUND")
		return
	}
	if q.Method != "GET" && q.Method != "HEAD" {
		refuse(w, 405, "INVALID_INPUT")
		return
	}
	if r.assets == nil {
		refuse(w, 404, "NOT_FOUND")
		return
	}
	r.assets.ServeHTTP(w, q)
}
