package v2

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
)

type session struct {
	PrincipalID    string `json:"principal_id"`
	Kind           string `json:"kind"`
	APIVersion     string `json:"api_version"`
	WorkspaceAdmin bool   `json:"workspace_admin"`
}

type browserSession struct {
	session
	AccessToken                 string `json:"access_token"`
	ExpiresOnServiceReplacement bool   `json:"expires_on_service_replacement"`
}

func mintBrowserToken() string {
	var bytes [32]byte
	rand.Read(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func (x *request) bootstrapBrowser() {
	site := x.r.Header.Values("Sec-Fetch-Site")
	if x.r.Header.Get("Origin") != "http://"+x.r.Host || len(site) != 1 || site[0] != "same-origin" {
		x.fail(errForbidden)
		return
	}
	if len(x.r.Header.Values("Authorization")) != 0 {
		x.fail(invalid("Authorization", "The browser bootstrap accepts no credentials."))
		return
	}
	if x.r.ContentLength != 0 || x.r.URL.RawQuery != "" {
		x.fail(invalid("body", "The browser bootstrap accepts no input."))
		return
	}
	x.respond(http.StatusOK, browserSession{
		session:     session{PrincipalID: humanID, Kind: "human", APIVersion: Version, WorkspaceAdmin: true},
		AccessToken: x.service.browserToken, ExpiresOnServiceReplacement: true,
	}, "")
}

func (x *request) sessionResource() {
	kind := "human"
	if x.actor == botID {
		kind = "bot"
	}
	data, err := marshalCanonical(session{
		PrincipalID: x.actor, Kind: kind, APIVersion: Version, WorkspaceAdmin: true,
	})
	if err != nil {
		x.fail(errors.New("session encoding failed"))
		return
	}
	x.w.Header().Set("Content-Type", "application/json")
	if x.r.Method == http.MethodHead {
		x.w.Header().Set("Cache-Control", "private, no-cache")
		x.w.WriteHeader(http.StatusOK)
		return
	}
	x.w.WriteHeader(http.StatusOK)
	_, _ = x.w.Write(data)
}
