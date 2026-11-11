package v2

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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

func TestM04BrowserShellHeaders(t *testing.T) {
	_, c := eventHTTPServer(t)
	for _, tc := range []struct{ path, media string }{
		{"/", "text/html"},
		{"/boards/" + newID(), "text/html"},
		{"/assets/app.mjs", "text/javascript"},
		{"/assets/api.mjs", "text/javascript"},
		{"/assets/board-updates.mjs", "text/javascript"},
		{"/assets/operation-id.mjs", "text/javascript"},
		{"/assets/app.css", "text/css"},
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
		if tc.path == "/assets/operation-id.mjs" && !strings.Contains(string(body), "export function newOperationId()") {
			t.Fatal("operation ID module was not served")
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
