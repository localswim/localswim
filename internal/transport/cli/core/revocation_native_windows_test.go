//go:build windows && amd64 && k02_native

package core

import (
	"bytes"
	"errors"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http"
	"syscall"
	"testing"
)

// This uses the real T01 global media guard and session handler through the
// existing private root-only overlay/pinned dual-loopback fixture. A fake
// response alone cannot catch its empty-body JSON media requirement.
func TestNativeSelfRevokeT01EmptyJSONBoundary(t *testing.T) {
	f := newNative(t)
	ctx := nativeContext(t)
	c, err := f.provider.Open(ctx, f.provider.metadata.Selected)
	if err != nil {
		t.Fatal("current connection unavailable")
	}
	first := c.do
	status, decoded, networkFailure := 0, false, false
	var networkErrno syscall.Errno
	c.do = func(r *http.Request) (*http.Response, error) {
		resp, e := first(r)
		networkFailure = e != nil
		_ = errors.As(e, &networkErrno)
		if resp != nil && resp.Body != nil {
			status = resp.StatusCode
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(b))
			_, decodeErr := T.Decode[T.SessionRead](b)
			decoded = readErr == nil && decodeErr == nil
		}
		return resp, e
	}
	cl, err := Open(ctx, &provider{conn: c}, f.provider.metadata.Selected)
	if err != nil {
		code, exit := failure(err)
		t.Fatalf("current session binding unavailable: %s exit %d status %d decoded %t networkFailure %t errno %d", code, exit, status, decoded, networkFailure, networkErrno)
	}
	defer cl.Close()
	prior := cl.c.do
	deletes, markers := 0, 0
	cl.c.do = func(r *http.Request) (*http.Response, error) {
		deletes++
		body, e := io.ReadAll(r.Body)
		if deletes != 1 || markers != 1 || r.Method != "DELETE" || r.URL.Path != "/api/v1/session" || r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("Content-Type")) != 1 || r.ContentLength != 0 || r.GetBody != nil || e != nil || len(body) != 0 {
			t.Fatal("one empty JSON credential revoke required")
		}
		return prior(r)
	}
	if err := cl.RevokeCredential(ctx, func() error { markers++; return nil }); err != nil {
		t.Fatal("real T01 boundary refused empty JSON revocation")
	}
	if err := cl.RevokeCredential(ctx, func() error { t.Fatal("replayed marker"); return nil }); err == nil || deletes != 1 || markers != 1 {
		t.Fatal("credential revocation retried")
	}
}
