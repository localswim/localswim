//go:build windows && amd64

package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	I "github.com/localswim/localswim/internal/board/installation"
	R "github.com/localswim/localswim/internal/board/recipient"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func configureProbeHTTPFixture(t *testing.T, r *Runtime, info ClientInfo) (T.UUIDv4, T.UUIDv4) {
	t.Helper()
	status := request(t, r, "GET", r.boardPath()+"recipient/status?incarnation="+info.Incarnation, nil, info.Token, nil, nil)
	view, err := T.Decode[T.RecipientResponse](status.b)
	if status.status != 200 || err != nil {
		t.Fatal("recipient fixture status unavailable")
	}
	body, err := T.Readable(T.RecipientConfigureRequest{Incarnation: A.UUID(info.Incarnation), ExpectedRuntimeGeneration: view.Result.RuntimeGeneration, RequestID: "00000000-0000-4000-8000-000000000019", Input: T.RecipientInput{Version: 1, Recipient: T.RecipientReference{Kind: "codex-thread", ThreadID: "00000000-0000-7000-8000-000000000016"}, Policy: "conservative-metadata-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	saved := request(t, r, "POST", r.boardPath()+"recipient/configure", body, info.Token, nil, nil)
	result, err := T.Decode[T.RecipientResponse](saved.b)
	if saved.status != 200 || err != nil || result.Result.ConfigurationGeneration == nil {
		t.Fatal("recipient fixture configure failed")
	}
	return result.Result.RuntimeGeneration, *result.Result.ConfigurationGeneration
}

// The native engine is deliberately replaced before any new route is called.
// This tests real selected-board admission/HTTP/physical ownership without an
// installed process or an alternate production selector.
func TestProbeNativeControlledHTTPAuthority(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Fatal("requires explicit private native fixture parent")
	}
	for _, scenario := range []string{"configured", "stale-configuration", "late-credential-revoked", "late-read-right-revoked", "physical-change", "lost-sidecar"} {
		t.Run(scenario, func(t *testing.T) {
			parent := testRoot(t)
			defer os.RemoveAll(parent)
			root := filepath.Join(parent, "probe-http")
			r, err := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			info, err := ReadClient(root)
			if err != nil {
				t.Fatal(err)
			}
			generation, configuration := configureProbeHTTPFixture(t, r, info)
			if err = r.installation.Close(); err != nil {
				t.Fatal(err)
			}
			fake := &probeHTTPFake{checkAuthority: true}
			r.installation = fake
			probe := "00000000-0000-4000-8000-000000000025"
			body := func(config T.UUIDv4) []byte {
				b, e := T.Readable(T.ProbeRequest{Incarnation: A.UUID(info.Incarnation), ExpectedRuntimeGeneration: generation, ExpectedConfigurationGeneration: config, ProbeGeneration: T.UUIDv4(probe)})
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			want, code := 200, ""
			switch scenario {
			case "stale-configuration":
				configuration = "00000000-0000-4000-8000-000000000026"
				want, code = 409, "CONTROL_STALE"
			case "late-credential-revoked":
				fake.afterCheck = func() {
					r.auth.mu.Lock()
					delete(r.auth.credentials, sha256.Sum256([]byte(info.Token)))
					r.auth.mu.Unlock()
				}
				want, code = 401, "UNAUTHENTICATED"
			case "late-read-right-revoked":
				fake.afterCheck = func() {
					configuredGrantSQL(t, r, "DELETE FROM grant_right WHERE incarnation=? AND grant_id=? AND right_name='read'", r.id.Incarnation, r.id.BotGrant)
				}
				want, code = 403, "FORBIDDEN"
			case "physical-change":
				r.recipientState.verify = func() error { return R.ErrForeign }
				want, code = 409, "IDENTITY_CHANGED"
			case "lost-sidecar":
				if err = os.Remove(filepath.Join(root, recipientStateName)); err != nil {
					t.Fatal(err)
				}
				want, code = 503, "UNAVAILABLE"
			}
			out := request(t, r, "POST", r.boardPath()+"recipient/tool/probe-version", body(configuration), info.Token, nil, nil)
			if out.status != want {
				t.Fatalf("controlled HTTP status%d want%d", out.status, want)
			}
			if code != "" {
				assertActivityRefusal(t, out.status, out.b, want, code)
			} else {
				v, e := T.Decode[T.ProbeResult](out.b)
				if e != nil || v.ToolVersion == nil || string(*v.ToolVersion) != "1.2.3" || !v.Joined {
					t.Fatal("controlled projection failed")
				}
			}
			if fake.calls != 1 {
				t.Fatal("adapter dispatched more than once")
			}
		})
	}
}

func TestProbeRuntimeCloseRetainsPhysicalOwner(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Fatal("requires explicit private native fixture parent")
	}
	parent := testRoot(t)
	defer os.RemoveAll(parent)
	root := filepath.Join(parent, "probe-close")
	r, err := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.installation.Close(); err != nil {
		t.Fatal(err)
	}
	info, readErr := ReadClient(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	fake := &probeHTTPFake{closeErr: I.ErrIncomplete}
	r.installation = fake
	defer func() { fake.closeErr = nil; _ = r.Close() }()
	started := time.Now()
	if !errors.Is(r.Close(), I.ErrIncomplete) || r.closed || time.Since(started) > 3*time.Second {
		t.Fatal("incomplete component close released or hung")
	}
	release, claimErr := lockRoot(root)
	if release != nil {
		release()
	}
	if claimErr == nil {
		t.Fatal("physical claim released before native join")
	}
	fake.closeErr = nil
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	other, e := Start(context.Background(), Options{DataDir: root, Port: info.Port})
	if e != nil {
		t.Fatal("actual completed close did not release owner")
	}
	if err = other.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestProbeHTTPDoesNotRenewBrowserIdle(t *testing.T) {
	if os.Getenv("L02_TEST_ROOT") == "" {
		t.Fatal("requires explicit private native fixture parent")
	}
	parent := testRoot(t)
	defer os.RemoveAll(parent)
	root := filepath.Join(parent, "probe-idle")
	r, err := Start(context.Background(), Options{DataDir: root, Port: freePort(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	runtimeGeneration, configuration := configureProbeHTTPFixture(t, r, info)
	cookie := bootstrapTest(t, r)
	key := sha256.Sum256([]byte(cookie.Value))
	r.auth.mu.Lock()
	credential := r.auth.credentials[key]
	credential.IdleExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	original := credential.IdleExpiresAt
	r.auth.credentials[key] = credential
	r.auth.mu.Unlock()
	if err = r.installation.Close(); err != nil {
		t.Fatal(err)
	}
	r.installation = &probeHTTPFake{checkAuthority: true}
	query := url.Values{"incarnation": {info.Incarnation}, "expectedRuntimeGeneration": {string(runtimeGeneration)}, "expectedConfigurationGeneration": {string(configuration)}}.Encode()
	body, err := T.Readable(T.ProbeRequest{Incarnation: A.UUID(info.Incarnation), ExpectedRuntimeGeneration: runtimeGeneration, ExpectedConfigurationGeneration: configuration, ProbeGeneration: probeTestGeneration})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"probe-control", "probe-version", "stop"} {
		method, path, input := "POST", r.boardPath()+"recipient/tool/"+action, body
		if action == "probe-control" {
			method = "GET"
			path += "?" + query
			input = nil
		}
		out := request(t, r, method, path, input, "", cookie, nil)
		if out.status != 200 {
			t.Fatalf("controlled browser %s status%d", action, out.status)
		}
		r.auth.mu.Lock()
		after := r.auth.credentials[key].IdleExpiresAt
		r.auth.mu.Unlock()
		if !after.Equal(original) {
			t.Fatal("native identity request renewed browser idle authority")
		}
	}
	// Existing ordinary recipient reads retain their established user-activity behavior.
	out := request(t, r, "GET", r.boardPath()+"recipient/status?incarnation="+info.Incarnation, nil, "", cookie, nil)
	if out.status != 200 {
		t.Fatal("ordinary recipient status unavailable")
	}
	r.auth.mu.Lock()
	after := r.auth.credentials[key].IdleExpiresAt
	r.auth.mu.Unlock()
	if !after.After(original) {
		t.Fatal("normal user-read renewal changed")
	}
}
