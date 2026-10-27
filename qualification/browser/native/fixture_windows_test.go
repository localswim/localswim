//go:build windows && amd64 && v02qualification

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This checks delivery interposition mechanics only. It is not browser/native
// positive qualification and creates no listener, trust change or grant.
func TestDelayAfterHandlerPreservesActualResponse(t *testing.T) {
	var committed atomic.Bool
	d := &delivery{nextMode: "delay", next: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		committed.Store(true)
		w.Header().Set("X-Actual", "product")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("actual-handler-result"))
	})}
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.ServeHTTP(w, httptest.NewRequest("POST", "https://fixture.invalid/api/v1/boards/synthetic/cards", nil))
	}()
	deadline := time.After(3 * time.Second)
	for {
		d.mu.Lock()
		reached := d.reached
		d.mu.Unlock()
		if reached {
			break
		}
		select {
		case <-deadline:
			t.Fatal("actual handler did not reach delivery barrier")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if !committed.Load() {
		t.Fatal("delivery barrier preceded handler completion")
	}
	select {
	case <-done:
		t.Fatal("actual response escaped barrier")
	default:
	}
	d.release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("release did not finish handler")
	}
	if w.Code != 201 || w.Header().Get("X-Actual") != "product" || w.Body.String() != "actual-handler-result" {
		t.Fatal("delivery interposition changed actual response")
	}
	if d.mutations.Load() != 1 {
		t.Fatal("actual mutation submission was not counted")
	}
	second := httptest.NewRecorder()
	d.ServeHTTP(second, httptest.NewRequest("POST", "https://fixture.invalid/api/v1/boards/synthetic/cards", nil))
	if second.Code != 201 || d.mutations.Load() != 2 {
		t.Fatal("delivery control repeated or counter drifted")
	}
}

// The invented code exercises encoding only, without issuance, entropy,
// native owner authority, listeners or any browser/session activation.
func TestPipeCodeFrameUsesEphemeralEncoder(t *testing.T) {
	secret := T.CodeSecret{APIVersion: 1, InstallationID: T.UUIDv4("00000000-0000-4000-8000-000000000001"), BoardID: A.UUID("00000000-0000-4000-8000-000000000002"), Incarnation: A.UUID("00000000-0000-4000-8000-000000000003"), GrantID: A.UUID("00000000-0000-4000-8000-000000000004"), ActorID: A.UUID("00000000-0000-4000-8000-000000000005"), Code: T.SecretCode("AAAAAAAAAAAAAAAAAAAAAA"), IssuedAt: A.Timestamp("2026-10-27T00:00:00Z"), ExpiresAt: A.Timestamp("2026-10-27T00:01:00Z")}
	if _, e := json.Marshal(secret); e == nil {
		t.Fatal("generic secret encoding unexpectedly permitted")
	}
	frame, e := encodePipeFrame(secret)
	defer clear(frame)
	if e != nil || len(frame) > 8192 || bytes.Count(frame, []byte{'\n'}) != 1 || len(frame) == 0 || frame[len(frame)-1] != '\n' {
		t.Fatal("ephemeral pipe framing unavailable")
	}
	var decoded T.CodeSecret
	if json.Unmarshal(frame[:len(frame)-1], &decoded) != nil || decoded != secret {
		t.Fatal("ephemeral pipe frame changed synthetic typed response")
	}
	invalid := secret
	invalid.Code = "bad"
	if b, e := encodePipeFrame(invalid); e == nil || len(b) != 0 {
		clear(b)
		t.Fatal("invalid secret accepted")
	}
	if b, e := encodePipeFrame(map[string]any{"unexpected": secret}); e == nil || len(b) != 0 {
		clear(b)
		t.Fatal("nested secret entered generic output")
	}
	public, e := encodePipeFrame(map[string]any{"ok": true})
	defer clear(public)
	if e != nil || string(public) != "{\"ok\":true}\n" {
		t.Fatal("public pipe framing changed")
	}
	if b, e := encodePipeFrame(map[string]any{"value": string(make([]byte, 8192))}); e == nil || len(b) != 0 {
		clear(b)
		t.Fatal("oversized pipe frame accepted")
	}
}

func TestFixedStageDiagnosticsAndSyntheticLongLine(t *testing.T) {
	expected := strings.Repeat("A", 422)
	for _, suffix := range []string{"\n", "\r\n"} {
		if validateDelegationLine(expected, expected+suffix, nil) != nil {
			t.Fatal("synthetic exact long line refused")
		}
	}
	if fixtureDiagnostic(validateDelegationLine(expected, expected+" \n", nil)) != "V02_DELEGATION_LINE_MISMATCH" {
		t.Fatal("line mismatch stage unavailable")
	}
	if fixtureDiagnostic(validateDelegationLine(expected, "", io.EOF)) != "V02_DELEGATION_INPUT_UNAVAILABLE" {
		t.Fatal("input stage unavailable")
	}
	if fixtureDiagnostic(errors.New("synthetic untrusted detail")) != "V02_UNAVAILABLE" || fixtureDiagnostic(fixtureFailure("synthetic untrusted detail")) != "V02_UNAVAILABLE" {
		t.Fatal("unapproved error detail escaped diagnostic boundary")
	}
}
