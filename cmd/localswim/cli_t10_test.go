package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testActivationID  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testBuildID       = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testInstanceID    = "cccccccc-cccc-7ccc-8ccc-cccccccccccc"
	testServiceTag    = `"service-instance-001"`
	testDrainingTag   = `"service-instance-002"`
	testLifecyclePath = "/api/2026-11-10"
)

func TestCLIServiceLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name, action, replyMode string
		status                  int
		generateID, unknown     bool
	}{
		{name: "stop accepted", action: "stop", status: http.StatusAccepted},
		{name: "activate accepted", action: "activate", status: http.StatusAccepted},
		{name: "generated activation", action: "activate", status: http.StatusAccepted, generateID: true},
		{name: "preflight mismatch", action: "stop", replyMode: "mismatch"},
		{name: "preflight incomplete", action: "activate", replyMode: "incomplete-preflight"},
		{name: "preflight wrong status", action: "activate", replyMode: "preflight-status"},
		{name: "preflight wrong media type", action: "stop", replyMode: "preflight-type"},
		{name: "stale activation", action: "activate", status: http.StatusPreconditionFailed},
		{name: "stop final flush refusal", action: "stop", status: http.StatusServiceUnavailable, replyMode: "problem"},
		{name: "activate final flush refusal", action: "activate", status: http.StatusServiceUnavailable, replyMode: "problem"},
		{name: "unproven 503", action: "stop", status: http.StatusServiceUnavailable, unknown: true},
		{name: "lost stop response", action: "stop", replyMode: "lost", status: http.StatusAccepted, unknown: true},
		{name: "lost activation response", action: "activate", replyMode: "lost", status: http.StatusAccepted, generateID: true, unknown: true},
		{name: "unexpected success", action: "stop", status: http.StatusOK, unknown: true},
		{name: "unconfirmed stop", action: "stop", status: http.StatusAccepted, replyMode: "running", unknown: true},
		{name: "incomplete stop receipt", action: "stop", status: http.StatusAccepted, replyMode: "incomplete-receipt", unknown: true},
		{name: "stale stop receipt", action: "stop", status: http.StatusAccepted, replyMode: "stale-receipt", unknown: true},
		{name: "foreign stop receipt", action: "stop", status: http.StatusAccepted, replyMode: "foreign-receipt", unknown: true},
		{name: "wrong activation identity", action: "activate", status: http.StatusAccepted, replyMode: "wrong-id", unknown: true},
		{name: "wrong activation location", action: "activate", status: http.StatusAccepted, replyMode: "wrong-location", unknown: true},
		{name: "extra activation field", action: "activate", status: http.StatusAccepted, replyMode: "extra-field", unknown: true},
		{name: "stop receipt wrong media type", action: "stop", status: http.StatusAccepted, replyMode: "receipt-type", unknown: true},
		{name: "activation receipt duplicate media type", action: "activate", status: http.StatusAccepted, replyMode: "receipt-duplicate-type", unknown: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var acceptedID atomic.Value
			var acceptedReply atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("expected configured local credential")
				}
				switch requests.Add(1) {
				case 1:
					if r.Method != http.MethodGet || r.URL.Path != testLifecyclePath+"/service" || r.Header.Get("If-Match") != "" {
						t.Error("expected unconditioned service read")
					}
					tag := testServiceTag
					if tc.replyMode == "mismatch" {
						tag = testDrainingTag
					}
					w.Header().Set("ETag", tag)
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					document := testLifecycleStatus(testServiceTag, "running")
					switch tc.replyMode {
					case "preflight-status":
						w.WriteHeader(http.StatusCreated)
					case "preflight-type":
						w.Header().Set("Content-Type", "text/plain")
					case "incomplete-preflight":
						document = strings.Replace(document, `,"restart_error_code":null`, "", 1)
					}
					fmt.Fprint(w, document)
				case 2:
					var body map[string]string
					if tc.status == 0 || r.Header.Get("Content-Type") != "application/json" || json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("unexpected lifecycle write or invalid write input")
					}
					var document string
					if tc.action == "stop" {
						if r.Method != http.MethodPatch || r.URL.Path != testLifecyclePath+"/service" ||
							r.Header.Get("If-Match") != testServiceTag || len(body) != 1 || body["state"] != "stopped" {
							t.Error("expected a shutdown PATCH bound to the original service ETag")
						}
						state := "draining"
						if tc.replyMode == "running" {
							state = "running"
						}
						document = testLifecycleStatus(testDrainingTag, state)
						switch tc.replyMode {
						case "incomplete-receipt":
							document = strings.Replace(document, `"port":31819,`, "", 1)
						case "stale-receipt":
							document = testLifecycleStatus(testServiceTag, state)
						case "foreign-receipt":
							document = strings.Replace(document, testInstanceID, "dddddddd-dddd-7ddd-8ddd-dddddddddddd", 1)
						}
					} else {
						id := body["id"]
						acceptedID.Store(id)
						if r.Method != http.MethodPost || r.URL.Path != testLifecyclePath+"/activations" ||
							r.Header.Get("If-Match") != "" || len(body) != 3 || body["build_id"] != testBuildID ||
							body["expected_service_etag"] != testServiceTag || !boardIDPattern.MatchString(id) ||
							(!tc.generateID && id != testActivationID) {
							t.Error("expected an activation POST with the requested build, operation ID, and original service ETag")
						}
						if tc.replyMode == "wrong-id" {
							id = testBuildID
						}
						location := testLifecyclePath + "/activations/" + id
						if tc.replyMode == "wrong-location" {
							location = testLifecyclePath + "/activations/" + testBuildID
						}
						w.Header().Set("Location", location)
						document = testActivationDocument(id, "accepted", "null")
						if tc.replyMode == "extra-field" {
							document = strings.TrimSuffix(document, "}") + `,"extra":true}`
						}
					}
					if tc.replyMode == "lost" {
						conn, _, err := http.NewResponseController(w).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						conn.Close()
						return
					}
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					switch tc.replyMode {
					case "receipt-type":
						w.Header().Set("Content-Type", "text/plain")
					case "receipt-duplicate-type":
						w.Header().Add("Content-Type", "application/json")
					case "problem":
						w.Header().Set("Content-Type", "application/problem+json")
						document = `{"type":"about:blank","title":"Service Unavailable","status":503,"code":"unavailable","detail":"Final publication failed. The service remains running.","request_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","outcome":"not_applied","errors":[]}`
					}
					acceptedReply.Store(document)
					w.WriteHeader(tc.status)
					fmt.Fprint(w, document)
				default:
					t.Error("lifecycle command sent an extra request")
				}
			}))
			defer server.Close()
			args := []string{"service", tc.action}
			if tc.action == "activate" {
				if !tc.generateID {
					args = append(args, "--id", strings.ToUpper(testActivationID))
				}
				args = append(args, strings.ToUpper(testBuildID))
			}
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput(args, strings.NewReader(""), &stdout, &stderr, testLifecycleEnv(server.URL))
			wantRequests := int32(2)
			if tc.status == 0 {
				wantRequests = 1
			}
			if requests.Load() != wantRequests {
				t.Fatalf("sent %d requests, wanted %d", requests.Load(), wantRequests)
			}
			if tc.status == http.StatusAccepted && !tc.unknown {
				if exit != 0 || stderr.Len() != 0 || !equalCLIJSON(t, stdout.Bytes(), []byte(acceptedReply.Load().(string))) ||
					!strings.Contains(stdout.String(), "\n  \"state\": ") {
					t.Fatalf("expected full pretty JSON, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
				}
				return
			}
			if exit != 1 || stdout.Len() != 0 ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") != tc.unknown ||
				strings.Contains(stderr.String(), "not applied") != (tc.replyMode == "problem") {
				t.Fatalf("unexpected refusal, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
			}
			if tc.unknown {
				hint := "localswim service status"
				if tc.action == "activate" {
					hint = "localswim service activation " + acceptedID.Load().(string)
				}
				if !strings.Contains(stderr.String(), hint) {
					t.Fatalf("missing inspection hint %q: %s", hint, &stderr)
				}
			}
		})
	}
}

func TestCLIServiceActivationInspection(t *testing.T) {
	document := testActivationDocument(testActivationID, "failed", `"  東京  "`)
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != testLifecyclePath+"/activations/"+testActivationID ||
			r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("expected only a read for the requested activation with the current credential")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", testServiceTag)
		fmt.Fprint(w, document)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	exit := runCLIWithInput([]string{"service", "activation", strings.ToUpper(testActivationID)}, strings.NewReader(""),
		&stdout, &stderr, testLifecycleEnv(server.URL))
	if exit != 0 || reads.Load() != 1 || stderr.Len() != 0 || !equalCLIJSON(t, stdout.Bytes(), []byte(document)) ||
		!strings.Contains(stdout.String(), "\n  \"error_code\": \"  東京  \"") {
		t.Fatalf("expected full pretty UTF-8 activation JSON, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
	}
}

func TestCLIServiceActivationInspectionRefusesInvalidEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, tag, wantError string
		status                            int
	}{
		{"unexpected status", "application/json", testServiceTag, "unexpected HTTP 202", http.StatusAccepted},
		{"wrong media type", "text/plain", testServiceTag, "application/json", http.StatusOK},
		{"malformed media type", "application/json; charset", testServiceTag, "application/json", http.StatusOK},
		{"missing tag", "application/json", "", "strong activation ETag", http.StatusOK},
		{"weak tag", "application/json", "W/" + testServiceTag, "strong activation ETag", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != testLifecyclePath+"/activations/"+testActivationID {
					t.Error("expected only a read for the requested activation")
				}
				w.Header().Set("Content-Type", tc.contentType)
				if tc.tag != "" {
					w.Header().Set("ETag", tc.tag)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, testActivationDocument(testActivationID, "activated", "null"))
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"service", "activation", testActivationID}, strings.NewReader(""),
				&stdout, &stderr, testLifecycleEnv(server.URL))
			if exit != 1 || reads.Load() != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.wantError) ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") {
				t.Fatalf("expected invalid response envelope, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
			}
		})
	}
}

func TestCLIServiceStatusSchema(t *testing.T) {
	document := testLifecycleStatus(testServiceTag, "running")
	for _, tc := range []struct{ name, original, replacement string }{
		{"port out of range", `"port":31819`, `"port":65536`},
		{"port invalid type", `"port":31819`, `"port":true`},
		{"invalid service instance ID", testInstanceID, testBuildID},
		{"noncanonical service instance ID", testInstanceID, strings.ToUpper(testInstanceID)},
		{"invalid build ID", testBuildID, testInstanceID},
		{"noncanonical build ID", testBuildID, strings.ToUpper(testBuildID)},
		{"wrong API version", `"api_version":"2026-11-10"`, `"api_version":"2026-11-09"`},
		{"invalid ETag", "service-instance-001", "short"},
		{"invalid state", `"running"`, `"stopped"`},
		{"missing restart error", `,"restart_error_code":null`, ""},
		{"invalid restart error type", `"restart_error_code":null`, `"restart_error_code":[]`},
		{"oversized restart error", `"restart_error_code":null`, `"restart_error_code":"` + strings.Repeat("東", 81) + `"`},
		{"extra member", `"restart_error_code":null`, `"restart_error_code":null,"extra":0`},
		{"duplicate member", `"port":31819`, `"port":31819,"port":31819`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var status cliServiceStatus
			if status.decode(json.RawMessage(strings.Replace(document, tc.original, tc.replacement, 1))) {
				t.Fatal("accepted an invalid Service response")
			}
		})
	}
}

func TestCLIServiceActivationInspectionRefusesInvalidMetadata(t *testing.T) {
	for _, tc := range []struct{ name, document string }{
		{"missing error code", strings.Replace(testActivationDocument(testActivationID, "accepted", "null"), `,"error_code":null`, "", 1)},
		{"failed without an error code", testActivationDocument(testActivationID, "failed", "null")},
		{"accepted with an error code", testActivationDocument(testActivationID, "accepted", `"unexpected"`)},
		{"activated with an error code", testActivationDocument(testActivationID, "activated", `"unexpected"`)},
		{"failed with an invalid error code", testActivationDocument(testActivationID, "failed", "true")},
		{"failed with an oversized error code", testActivationDocument(testActivationID, "failed", `"`+strings.Repeat("東", 81)+`"`)},
		{"invalid state", testActivationDocument(testActivationID, "running", "null")},
		{"noncanonical build ID", strings.Replace(testActivationDocument(testActivationID, "activated", "null"), testBuildID, strings.ToUpper(testBuildID), 1)},
		{"extra field", strings.Replace(testActivationDocument(testActivationID, "accepted", "null"), `"error_code":null`, `"error_code":null,"extra":false`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != testLifecyclePath+"/activations/"+testActivationID {
					t.Error("expected only a read for the requested activation")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("ETag", testServiceTag)
				fmt.Fprint(w, tc.document)
			}))
			defer server.Close()
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"service", "activation", testActivationID}, strings.NewReader(""),
				&stdout, &stderr, testLifecycleEnv(server.URL))
			if exit != 1 || reads.Load() != 1 || stdout.Len() != 0 ||
				!strings.Contains(stderr.String(), "did not return the requested activation") ||
				strings.Contains(stderr.String(), "Mutation outcome unknown") {
				t.Fatalf("expected invalid read response, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
			}
		})
	}
}

func TestCLIServiceLifecycleArguments(t *testing.T) {
	for _, action := range []string{"activate", "activation", "stop"} {
		t.Run(action+" help", func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exit := runCLIWithInput([]string{"service", action, "--help"}, strings.NewReader(""), &stdout, &stderr, func(string) string {
				t.Error("help must not require configuration")
				return ""
			})
			if exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "localswim service "+action) {
				t.Fatalf("expected command help, exit %d, stdout %q, stderr %q", exit, &stdout, &stderr)
			}
		})
	}
	for _, args := range [][]string{
		{"activate", "--id=", testBuildID},
		{"activate", "invalid"},
		{"activation"},
		{"stop", "extra"},
	} {
		if _, err := parseCLI(append([]string{"service"}, args...)); err == nil {
			t.Errorf("accepted invalid service arguments %q", args)
		}
	}
}

func testLifecycleStatus(tag, state string) string {
	return fmt.Sprintf(`{"port":31819,"etag":%q,"service_instance_id":%q,"build_id":%q,"state":%q,"api_version":"2026-11-10","restart_error_code":null}`,
		tag, testInstanceID, testBuildID, state)
}

func testActivationDocument(id, state, errorCode string) string {
	return fmt.Sprintf(`{"id":%q,"build_id":%q,"state":%q,"error_code":%s}`, id, testBuildID, state, errorCode)
}

func testLifecycleEnv(origin string) func(string) string {
	values := map[string]string{"LOCALSWIM_URL": origin, "LOCALSWIM_TOKEN": "test-token"}
	return func(key string) string { return values[key] }
}
