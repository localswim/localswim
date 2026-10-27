//go:build windows && amd64 && v02qualification

package main

import (
	"bytes"
	"context"
	"errors"
	"github.com/localswim/localswim/internal/transport/local"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStrictControl(t *testing.T) {
	for _, s := range []string{`{"op":"status","x":1}`, `{"op":"status","op":"stop"}`, `{"op":"status"} {}`, `null`} {
		if _, ok := parse([]byte(s)); ok {
			t.Fatalf("accepted ambiguous request")
		}
	}
}
func TestPostHandlerDelivery(t *testing.T) {
	d := &delivery{}
	d.arm("delayNext")
	done := make(chan struct{})
	w := httptest.NewRecorder()
	h := d.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("X-Actual", "one")
		w.Header().Add("X-Actual", "two")
		w.WriteHeader(409)
		_, _ = w.Write([]byte("actual response"))
	}))
	go func() {
		defer close(done)
		h.ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1:1234/api/v1/boards/synthetic/cards", nil))
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !d.delayReached() {
		if time.Now().After(deadline) {
			t.Fatal("barrier unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("response escaped barrier")
	default:
	}
	d.release()
	<-done
	if w.Code != 409 || w.Body.String() != "actual response" || len(w.Header().Values("X-Actual")) != 2 || d.count.Load() != 1 {
		t.Fatal("real response changed")
	}
}
func TestDropTruncatesActualResponseAfterHeaders(t *testing.T) {
	d := &delivery{}
	d.arm("dropNext")
	var called atomic.Uint64
	s := httptest.NewServer(d.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.Header().Set("X-Actual", "real-handler")
		w.WriteHeader(201)
		_, _ = w.Write([]byte("actual"))
	})))
	defer s.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	resp, e := client.Post(s.URL+"/api/v1/boards/synthetic/cards", "application/json", bytes.NewReader([]byte("{}")))
	if e != nil {
		t.Fatalf("actual response headers were lost: %v", e)
	}
	defer resp.Body.Close()
	body, readError := io.ReadAll(resp.Body)
	if resp.StatusCode != 201 || resp.Header.Get("X-Actual") != "real-handler" || resp.ContentLength != int64(len("actual")) || len(body) != 0 || !errors.Is(readError, io.ErrUnexpectedEOF) || called.Load() != 1 || d.count.Load() != 1 {
		t.Fatalf("truncation mechanics failed: status=%d length=%d body=%d readError=%v invocations=%d submissions=%d", resp.StatusCode, resp.ContentLength, len(body), readError, called.Load(), d.count.Load())
	}
}
func TestNativeStartRestartAndEOF(t *testing.T) {
	base := os.Getenv("V02_TEST_ROOT")
	if base == "" {
		base = t.TempDir()
	}
	root, e := os.MkdirTemp(base, "fixture-")
	if e != nil {
		t.Fatal(e)
	}
	listener, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	c := &controller{data: filepath.Join(root, "data"), port: port, delivery: &delivery{}}
	local.V02QualificationDelivery(c.delivery.wrap)
	if e = c.start(); e != nil {
		t.Fatal("native start unavailable")
	}
	defer c.close()
	before, e := local.ReadClient(c.data)
	if e != nil {
		t.Fatal("metadata unavailable")
	}
	req, _ := http.NewRequestWithContext(context.Background(), "GET", before.Origin+"/", nil)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	res, e := client.Do(req)
	if e != nil {
		t.Fatalf("embedded root unavailable: %v", e)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !bytes.Contains(b, []byte("<html")) {
		t.Fatal("not actual embedded assets")
	}
	// Closing while a real refused mutation waits at delivery must release it
	// before storage closes. A refusal is still an original handler submission.
	if !c.delivery.arm("delayNext") {
		t.Fatal("cannot arm native delay")
	}
	delivered := make(chan error, 1)
	go func() {
		response, err := client.Post(before.Origin+"/api/v1/boards/"+before.BoardID+"/cards", "application/json", bytes.NewReader([]byte("{}")))
		if response != nil {
			_, _ = io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
		delivered <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for !c.delivery.delayReached() {
		if time.Now().After(deadline) {
			t.Fatal("native handler did not reach delivery barrier")
		}
		time.Sleep(time.Millisecond)
	}
	if c.close() != nil || c.start() != nil {
		t.Fatal("restart unavailable")
	}
	select {
	case err := <-delivered:
		if err != nil {
			t.Fatalf("close lost drained response: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not drain delivery")
	}
	if c.delivery.count.Load() != 1 || c.delivery.delayReached() {
		t.Fatal("restart lost cumulative counter or delivery barrier")
	}
	after, e := local.ReadClient(c.data)
	if e != nil || before.BoardID != after.BoardID || before.InstallationID != after.InstallationID || before.Incarnation != after.Incarnation || before.Epoch == after.Epoch || before.Token == after.Token {
		t.Fatal("restart identity/rotation incorrect")
	}
	var output bytes.Buffer
	if c.serve(bytes.NewReader(nil), &output) != nil || c.close() != nil {
		t.Fatal("EOF shutdown unavailable")
	}
	if _, e = local.ReadClient(c.data); e == nil {
		t.Fatal("EOF retained live metadata")
	}
}
