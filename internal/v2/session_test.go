package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/localswim/localswim/web"
)

func TestM04BrowserBootstrapAndSession(t *testing.T) {
	app, c := eventHTTPServer(t)
	address, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(c.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler(address)
	bootstrap := func(host, path, body string, origins, sites []string, status int, auth ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Host = host
		req.Header["Origin"] = origins
		req.Header["Sec-Fetch-Site"] = sites
		if len(auth) != 0 {
			req.Header.Set("Authorization", "Bearer "+auth[0])
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("browser bootstrap status %d, want %d", w.Code, status)
		}
		if w.Header().Get("Cache-Control") != "no-store" ||
			w.Header().Get("Set-Cookie") != "" ||
			w.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatal("browser bootstrap must not persist its capability")
		}
		return w
	}
	path := Prefix + "/browser-session"
	same := []string{"same-origin"}
	allowed := []string{c.url}
	bootstrap(address.String(), path, "", nil, same, 403)
	bootstrap(address.String(), path, "", []string{"null"}, same, 403)
	bootstrap(address.String(), path, "", []string{"https://foreign.invalid"}, same, 403)
	bootstrap(address.String(), path, "", allowed, nil, 403)
	bootstrap(address.String(), path, "", allowed, []string{"same-site"}, 403)
	bootstrap(address.String(), path, "", allowed, []string{"same-origin", "same-origin"}, 403)
	bootstrap("foreign.invalid", path, "", allowed, same, 403)
	bootstrap(address.String(), path, `{"actor":"bot"}`, allowed, same, 400)
	bootstrap(address.String(), path+"?actor=bot", "", allowed, same, 400)
	botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	bootstrap(address.String(), path, "", allowed, same, 400, botToken)
	success := bootstrap(address.String(), path, "", allowed, same, 200)
	got := turnOneDecode[map[string]any](t, success.Body.Bytes())
	accessToken, ok := got["access_token"].(string)
	if !ok || !validToken(accessToken) || accessToken == c.token || accessToken == botToken {
		t.Fatal("bootstrap did not supply an independent browser capability")
	}
	want := map[string]any{
		"principal_id": humanID, "kind": "human", "api_version": Version, "workspace_admin": true,
		"access_token": accessToken, "expires_on_service_replacement": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("bootstrap response must contain only Human context and its runtime capability")
	}
	delete(want, "access_token")
	delete(want, "expires_on_service_replacement")
	browser := c
	browser.token = accessToken
	raw, headers := browser.call("GET", "/session", "", "", 200)
	if got := turnOneDecode[map[string]any](t, raw); !reflect.DeepEqual(got, want) ||
		headers.Get("Cache-Control") != "no-store" || headers.Get("ETag") != "" {
		t.Fatal("authenticated session leaked a capability or allowed caching")
	}
	bot := c
	bot.token = botToken
	want["principal_id"], want["kind"] = botID, "bot"
	raw, _ = bot.call("GET", "/session", "", "", 200)
	if got := turnOneDecode[map[string]any](t, raw); !reflect.DeepEqual(got, want) {
		t.Fatal("authenticated session did not distinguish the Bot principal")
	}
	c.token = ""
	c.call("GET", "/session", "", "", 401)
}

func TestM04BrowserCapabilityExpiresOnServiceReplacement(t *testing.T) {
	root := t.TempDir()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	oldToken := app.browserToken
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(root, human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := next.Close(); err != nil {
			t.Error(err)
		}
	})
	address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 5551}
	handler := next.Handler(address)
	for _, tc := range []struct {
		token  string
		status int
	}{{oldToken, 401}, {next.browserToken, 200}, {human, 200}, {bot, 200}} {
		req := httptest.NewRequest(http.MethodGet, Prefix+"/session", nil)
		req.Host = address.String()
		req.Header.Set("Authorization", "Bearer "+tc.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("after service replacement got %d, want %d", response.Code, tc.status)
		}
	}
}

// faviconVersion is the content hash the build appends to the favicon link.
func faviconVersion(t *testing.T) string {
	t.Helper()
	data, err := web.Assets.ReadFile("generated/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])[:12]
}

func TestM04BrowserShellHeaders(t *testing.T) {
	_, c := eventHTTPServer(t)
	for _, tc := range []struct{ path, media string }{
		{"/", "text/html"},
		{"/board/" + newID(), "text/html"},
		{"/board/missing-fixture", "text/html"},
		{"/boards/" + newID(), "text/html"},
		{"/assets/app.mjs", "text/javascript"},
		{"/assets/app.css", "text/css"},
		{"/assets/board.css", "text/css"},
		{"/assets/inter-variable.woff2", "font/woff2"},
		{"/assets/OFL.txt", "text/plain"},
		{"/assets/favicon.svg", "image/svg+xml"},
	} {
		res, err := c.client.Get(c.url + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		_ = res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.HasPrefix(tc.media, "text/html") && (!strings.Contains(string(body), "/assets/app.mjs") ||
			!strings.Contains(string(body), `<link rel="icon" type="image/svg+xml" href="/assets/favicon.svg?v=`+faviconVersion(t)+`"`)) {
			t.Fatal("optimized browser entry was not served with its versioned favicon")
		}
		if tc.path == "/assets/favicon.svg" {
			embedded, err := web.Assets.ReadFile("generated/favicon.svg")
			if err != nil || !bytes.Equal(body, embedded) || !bytes.HasPrefix(body, []byte("<svg ")) {
				t.Fatal("favicon is not the embedded SVG")
			}
		}
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), tc.media) ||
			res.Header.Get("Content-Security-Policy") != browserCSP || res.Header.Get("Cache-Control") != "no-store" ||
			res.Header.Get("Cross-Origin-Opener-Policy") != "same-origin" ||
			res.Header.Get("Cross-Origin-Resource-Policy") != "same-origin" ||
			res.Header.Get("X-Content-Type-Options") != "nosniff" ||
			res.Header.Get("Referrer-Policy") != "no-referrer" ||
			res.Header.Get("Permissions-Policy") != "camera=(), microphone=(), geolocation=()" {
			t.Fatalf("UI resource %s is unavailable or missing isolation headers", tc.path)
		}
	}
}
