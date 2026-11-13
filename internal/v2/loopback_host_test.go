package v2

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestLoopbackHostAliases(t *testing.T) {
	app, client := eventHTTPServer(t)
	address, err := net.ResolveTCPAddr("tcp", strings.TrimPrefix(client.url, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler(address)
	canonical := address.String()
	localhost := net.JoinHostPort("localhost", strconv.Itoa(address.Port))
	send := func(method, path, host, origin, token, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = host
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if method == http.MethodOptions {
			req.Header.Set("Access-Control-Request-Method", http.MethodPost)
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s on %s: got %d, want %d", method, path, host, res.Code, status)
		}
		return res
	}
	for _, host := range []string{canonical, localhost, strings.ToUpper(localhost)} {
		t.Run(host, func(t *testing.T) {
			origin := "http://" + host
			page := send(http.MethodGet, "/", host, "", "", "", http.StatusOK)
			if page.Header().Get("Content-Type") != "text/html; charset=utf-8" {
				t.Fatal("alias did not serve the browser UI")
			}
			bootstrap := send(http.MethodPost, Prefix+"/browser-session", host, origin, "", "", http.StatusOK)
			session := turnOneDecode[browserSession](t, bootstrap.Body.Bytes())
			send(http.MethodPost, Prefix+"/boards", host, origin, session.AccessToken, `{"name":"Alias board"}`, http.StatusCreated)
			list := send(http.MethodGet, Prefix+"/boards", host, origin, session.AccessToken, "", http.StatusOK)
			if list.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal("response did not preserve the accepted same-origin authority")
			}
			send(http.MethodOptions, Prefix+"/boards", host, origin, "", "", http.StatusNoContent)
		})
	}
	for _, host := range []string{"foreign.invalid", "localhost.example:8811", localhost + ".", "localhost:0"} {
		send(http.MethodGet, "/", host, "", "", "", http.StatusForbidden)
	}
	send(http.MethodGet, Prefix+"/boards", canonical, "http://"+localhost, client.token, "", http.StatusForbidden)
	send(http.MethodGet, Prefix+"/boards", localhost, client.url, client.token, "", http.StatusForbidden)
	send(http.MethodGet, "http://"+localhost+"/", localhost, "", "", "", http.StatusForbidden)
}
