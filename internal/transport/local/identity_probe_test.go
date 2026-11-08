package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/board/grants"
	I "github.com/localswim/localswim/internal/board/installation"
	R "github.com/localswim/localswim/internal/board/recipient"
	T "github.com/localswim/localswim/internal/contract/transport"
	H "github.com/localswim/localswim/internal/transport/https"
)

const probeTestBoard = "00000000-0000-4000-8000-000000000001"
const probeTestInc = "00000000-0000-4000-8000-000000000002"
const probeTestRuntime = "00000000-0000-4000-8000-000000000003"
const probeTestConfig = "00000000-0000-4000-8000-000000000004"
const probeTestGeneration = "00000000-0000-4000-8000-000000000005"

func probeTestBody() []byte {
	return []byte(fmt.Sprintf("{\"incarnation\":%q,\"expectedRuntimeGeneration\":%q,\"expectedConfigurationGeneration\":%q,\"probeGeneration\":%q}", probeTestInc, probeTestRuntime, probeTestConfig, probeTestGeneration))
}
func probeTestQuery() string {
	return url.Values{"incarnation": {probeTestInc}, "expectedRuntimeGeneration": {probeTestRuntime}, "expectedConfigurationGeneration": {probeTestConfig}}.Encode()
}
func TestIdentityProbeAdapterGrammar(t *testing.T) {
	good := string(probeTestBody())
	for _, tc := range []struct {
		name, method, action, query, body, media string
		want                                     int
	}{
		{"control", "GET", "probe-control", probeTestQuery(), "", "", 0},
		{"version", "POST", "probe-version", "", good, "application/json", 0},
		{"stop", "POST", "stop", "", good, "application/json; charset=UTF-8", 0},
		{"bad-method-control", "POST", "probe-control", probeTestQuery(), "", "", 405},
		{"bad-method-probe", "GET", "probe-version", "", "", "", 405},
		{"bad-action", "GET", "unknown", "", "", "", 404},
		{"body-on-get", "GET", "probe-control", probeTestQuery(), " ", "", 400},
		{"extra-query", "GET", "probe-control", probeTestQuery() + "&extra=x", "", "", 400},
		{"duplicate-query", "GET", "probe-control", probeTestQuery() + "&incarnation=" + probeTestInc, "", "", 400},
		{"query-on-post", "POST", "probe-version", "x=1", good, "application/json", 400},
		{"missing-field", "POST", "probe-version", "", strings.Replace(good, "\"probeGeneration\":\""+probeTestGeneration+"\"", "\"other\":null", 1), "application/json", 400},
		{"duplicate-key", "POST", "probe-version", "", strings.Replace(good, "}", ",\"probeGeneration\":\""+probeTestGeneration+"\"}", 1), "application/json", 400},
		{"wrong-type", "POST", "probe-version", "", strings.Replace(good, "\""+probeTestGeneration+"\"", "true", 1), "application/json", 400},
		{"wrong-uuid", "POST", "probe-version", "", strings.Replace(good, probeTestGeneration, strings.ToUpper(probeTestGeneration[:8])+"-invalid", 1), "application/json", 400},
		{"null-generation", "POST", "probe-version", "", strings.Replace(good, "\""+probeTestGeneration+"\"", "null", 1), "application/json", 400},
		{"bom", "POST", "probe-version", "", "\xef\xbb\xbf" + good, "application/json", 400},
		{"trailing-json", "POST", "probe-version", "", good + "{}", "application/json", 400},
		{"wrong-media", "POST", "probe-version", "", good, "text/plain", 400},
		{"wrong-charset", "POST", "probe-version", "", good, "application/json;charset=latin1", 400},
		{"extra-media-param", "POST", "probe-version", "", good, "application/json;x=y", 400},
		{"size-exact", "POST", "probe-version", "", good + strings.Repeat(" ", T.ProbeRequestLimit-len(good)), "application/json", 0},
		{"size-plus-one", "POST", "probe-version", "", good + strings.Repeat(" ", T.ProbeRequestLimit-len(good)+1), "application/json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := httptest.NewRequest(tc.method, "http://127.0.0.1/?"+tc.query, strings.NewReader(tc.body))
			if tc.media != "" {
				q.Header.Set("Content-Type", tc.media)
			}
			got, status := decodeProbeCall(q, tc.action)
			if status != tc.want {
				t.Fatalf("status %d want%d", status, tc.want)
			}
			if status == 0 && (string(got.Incarnation) != probeTestInc || string(got.ExpectedRuntimeGeneration) != probeTestRuntime || string(got.ExpectedConfigurationGeneration) != probeTestConfig) {
				t.Fatal("decoded context changed")
			}
		})
	}
}
func TestIdentityProbeQueryInclusiveBytes(t *testing.T) {
	values := []struct{ key, value string }{{"incarnation", probeTestInc}, {"expectedRuntimeGeneration", probeTestRuntime}, {"expectedConfigurationGeneration", probeTestConfig}}
	base := len(probeTestQuery())
	remaining := T.ProbeControlQueryLimit - base
	var parts []string
	for _, item := range values {
		var value strings.Builder
		for _, c := range []byte(item.value) {
			if remaining >= 2 {
				fmt.Fprintf(&value, "%%%02X", c)
				remaining -= 2
			} else {
				value.WriteByte(c)
			}
		}
		parts = append(parts, item.key+"="+value.String())
	}
	query := strings.Join(parts, "&")
	if remaining != 0 || len(query) != T.ProbeControlQueryLimit {
		t.Fatal("fixture cannot reach exact bound")
	}
	for _, extra := range []string{"", "&"} {
		q := httptest.NewRequest("GET", "http://127.0.0.1/?"+query+extra, nil)
		_, status := decodeProbeCall(q, "probe-control")
		if extra == "" && status != 0 || extra != "" && status != 400 {
			t.Fatalf("query bound status%d", status)
		}
	}
}
func TestIdentityProbeEncodingRefused(t *testing.T) {
	for _, key := range []string{"Content-Encoding", "Content-Transfer-Encoding"} {
		q := httptest.NewRequest("POST", "http://127.0.0.1/", bytes.NewReader(probeTestBody()))
		q.Header.Set("Content-Type", "application/json")
		q.Header.Set(key, "identity")
		if _, status := decodeProbeCall(q, "probe-version"); status != 400 {
			t.Fatal("encoded body admitted")
		}
	}
}

// This fake isolates HTTP projection/refusal behavior. It never executes a
// Check or native backend; separate native integration tests cover admission.
type probeHTTPFake struct {
	err            error
	calls          int
	stopGeneration string
	version        string
	checkAuthority bool
	afterCheck     func()
	closeErr       error
}

func (f *probeHTTPFake) authorized(ctx context.Context, check I.Check) error {
	if f.checkAuthority {
		if err := check(ctx); err != nil {
			return err
		}
		if f.afterCheck != nil {
			f.afterCheck()
		}
		if err := check(ctx); err != nil {
			return err
		}
	}
	return f.err
}
func (f *probeHTTPFake) Inspect(context.Context, string, I.Check) (I.Observation, error) {
	return I.Observation{}, errors.New("unused metadata fake")
}
func (f *probeHTTPFake) Control(ctx context.Context, _ string, _ string, check I.Check) (I.ControlView, error) {
	f.calls++
	return I.ControlView{ProbeGeneration: probeTestGeneration, Phase: "ready", Joined: true}, f.authorized(ctx, check)
}
func (f *probeHTTPFake) Probe(ctx context.Context, _ string, _ string, _ string, check I.Check) (I.ProbeObservation, error) {
	f.calls++
	v := f.version
	if v == "" {
		v = "1.2.3"
	}
	return I.ProbeObservation{State: "supported", Code: "OK", ToolVersion: &v, Joined: true}, f.authorized(ctx, check)
}
func (f *probeHTTPFake) Stop(ctx context.Context, _ string, _ string, _ string, check I.Check) (I.StopObservation, error) {
	f.calls++
	g := f.stopGeneration
	if g == "" {
		g = probeTestGeneration
	}
	return I.StopObservation{ProbeGeneration: g, Phase: "joined", Joined: true}, f.authorized(ctx, check)
}
func (f *probeHTTPFake) Close() error { return f.closeErr }

func probeFakeRuntime(f *probeHTTPFake) *Runtime {
	return &Runtime{id: identity{InstallationID: probeTestRuntime, BoardID: probeTestBoard, Incarnation: probeTestInc}, effectiveOrigin: "http://127.0.0.1:19199", recipient: &R.Store{}, recipientState: &recipientNativeState{verify: func() error { return nil }}, installation: f}
}
func probeFakeCall(r *Runtime, action string) *httptest.ResponseRecorder {
	path := r.boardPath() + "recipient/tool/" + action
	method := "POST"
	var body *bytes.Reader
	if action == "probe-control" {
		method = "GET"
		path += "?" + probeTestQuery()
		body = bytes.NewReader(nil)
	} else {
		body = bytes.NewReader(probeTestBody())
	}
	q := httptest.NewRequest(method, r.Origin()+path, body)
	q.Header.Set("Content-Type", "application/json")
	q = q.WithContext(H.WithCredential(q.Context(), "cli", "controlled-test-token"))
	out := httptest.NewRecorder()
	r.serveProbe(out, q)
	return out
}
func TestIdentityProbeHTTPControlledProjection(t *testing.T) {
	for _, action := range []string{"probe-control", "probe-version", "stop"} {
		t.Run(action, func(t *testing.T) {
			fake := &probeHTTPFake{}
			out := probeFakeCall(probeFakeRuntime(fake), action)
			if out.Code != 200 || fake.calls != 1 || out.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("controlled projection failed")
			}
			var v map[string]json.RawMessage
			if json.Unmarshal(out.Body.Bytes(), &v) != nil {
				t.Fatal("invalid projection JSON")
			}
			if bytes.Contains(out.Body.Bytes(), []byte("threadId")) || bytes.Contains(out.Body.Bytes(), []byte("codex.exe")) {
				t.Fatal("private data projected")
			}
		})
	}
}
func TestIdentityProbeHTTPRefusalsRedact(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"denied", grants.ErrDenied, 403, "FORBIDDEN"},
		{"auth", grants.ErrUnauthenticated, 401, "UNAUTHENTICATED"},
		{"busy", I.ErrBusy, 409, "CONTROL_BUSY"},
		{"stale", I.ErrStale, 409, "CONTROL_STALE"},
		{"identity", I.ErrIdentityChanged, 409, "IDENTITY_CHANGED"},
		{"foreign", R.ErrForeign, 409, "IDENTITY_CHANGED"},
		{"unknown", errProbeUnavailable, 503, "UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := probeFakeCall(probeFakeRuntime(&probeHTTPFake{err: tc.err}), "probe-version")
			v, e := T.Decode[T.NonterminalRefusal](out.Body.Bytes())
			if out.Code != tc.status || e != nil || v.Error.Code != tc.code || bytes.Contains(out.Body.Bytes(), []byte("toolVersion")) || bytes.Contains(out.Body.Bytes(), []byte("\"result\"")) {
				t.Fatal("refusal disclosed facts or changed code")
			}
		})
	}
}
func TestIdentityProbeHTTPRejectsBackendContext(t *testing.T) {
	for _, fake := range []*probeHTTPFake{{stopGeneration: probeTestRuntime}, {version: "private/path"}} {
		action := "probe-version"
		if fake.stopGeneration != "" {
			action = "stop"
		}
		out := probeFakeCall(probeFakeRuntime(fake), action)
		if out.Code != 503 || bytes.Contains(out.Body.Bytes(), []byte("private/path")) {
			t.Fatal("invalid backend response disclosed")
		}
	}
}
