package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

const activityFrom = "2026-10-30T12:00:00Z"
const activityThrough = "2026-11-06T12:00:00Z"

func TestActivityInvocationForms(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		query string
	}{
		{nil, "incarnation=" + testInc},
		{[]string{"--through", activityThrough, "--from", activityFrom}, url.Values{"from": {activityFrom}, "through": {activityThrough}, "incarnation": {testInc}}.Encode()},
		{[]string{"--cursor", "AA.AA"}, "cursor=AA.AA&incarnation=" + testInc},
	} {
		q, code, exit := parse(args(append([]string{"activity", "read"}, tc.flags...)...))
		if code != "" || exit != 0 || q.method != http.MethodGet || q.path != "/api/v1/boards/"+testBoard+"/activity?"+tc.query {
			t.Fatal("activity query form not preserved")
		}
	}
	for _, flags := range [][]string{
		{"--from", activityFrom}, {"--through", activityThrough}, {"--from", "", "--through", ""},
		{"--from", activityThrough, "--through", activityThrough}, {"--from", "2026-10-30T11:59:59Z", "--through", activityThrough},
		{"--from", "2026-11-00T12:00:00Z", "--through", activityThrough}, {"--from", "2026-11-06T11:00:00.0Z", "--through", activityThrough},
		{"--cursor", "AA.AA", "--from", activityFrom, "--through", activityThrough}, {"--cursor", "AA.AA", "--cursor", "AA.AA"},
		{"--cursor", "AQ==.AA"}, {"--cursor", strings.Repeat("A", T.ActivityCursorLimit) + ".AA"}, {"--actor", testActor},
	} {
		if _, code, exit := parse(args(append([]string{"activity", "read"}, flags...)...)); code != "INVOCATION_OR_IO" || exit != 1 {
			t.Error("accepted an invalid invocation")
		}
	}
}

func TestActivityReadResponseBoundary(t *testing.T) {
	for _, mode := range []string{"default", "explicit", "cursor", "default-short", "from", "through", "board", "incarnation", "prose", "oversized", "unreachable", "refusal", "stale"} {
		t.Run(mode, func(t *testing.T) {
			view := T.ActivityView{Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, From: activityFrom, Through: activityThrough,
				Coverage: T.ActivityCoverage{Status: "complete", Gaps: []T.ActivityGap{}}, Events: []T.ActivityEvent{}}
			command := []string{"activity", "read"}
			if mode == "explicit" || mode == "from" || mode == "through" {
				command = append(command, "--from", activityFrom, "--through", activityThrough)
			}
			if mode == "cursor" || mode == "stale" {
				command = append(command, "--cursor", "AA.AA")
			}
			switch mode {
			case "default-short", "from", "cursor":
				view.From = "2026-11-01T12:00:00Z"
			case "through":
				view.Through = "2026-11-06T11:00:00Z"
			case "board":
				view.Identity.BoardID = testInc
			case "incarnation":
				view.Identity.Incarnation = testBoard
			}
			wire, err := T.Readable(T.ActivityRead{APIVersion: 1, Scope: "activity", Result: view})
			if err != nil {
				t.Fatal("fixture envelope invalid")
			}
			const private = "SYNTHETIC_PRIVATE_PROSE"
			switch mode {
			case "prose":
				wire = append([]byte(`{"title":"`+private+`",`), wire[1:]...)
			case "oversized":
				wire = append(wire, bytes.Repeat([]byte{' '}, T.ActivityReadResponseLimit)...)
			}
			calls := 0
			p := &provider{conn: connection(t, func(r *http.Request) (int, []byte, error) {
				calls++
				if r.Method != http.MethodGet || r.Body != nil || r.URL.Path != "/api/v1/boards/"+testBoard+"/activity" || r.URL.Query().Get("incarnation") != testInc || r.Header.Get("Authorization") != "Bearer SYNTHETIC_SECRET" {
					t.Fatal("activity did not use the selected ordinary read")
				}
				switch mode {
				case "unreachable":
					return 0, nil, errors.New(private)
				case "refusal", "stale":
					status, code := 403, "FORBIDDEN"
					if mode == "stale" {
						status, code = 409, "CURSOR_STALE"
					}
					field := private
					b, _ := json.Marshal(T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: private, Field: &field}})
					return status, b, nil
				}
				return 200, wire, nil
			})}
			evidence := &memoryEvidence{}
			var out, diagnostic bytes.Buffer
			got := (Runner{Provider: p, Evidence: evidence}).Run(context.Background(), args(command...), nil, &out, &diagnostic)
			want, code := 3, "UNAVAILABLE"
			switch mode {
			case "default", "explicit", "cursor":
				want, code = 0, ""
			case "refusal":
				want, code = 2, "FORBIDDEN"
			case "stale":
				want, code = 2, "CURSOR_STALE"
			}
			if got != want || strings.TrimSpace(diagnostic.String()) != code || calls != 1 || p.native != 0 || evidence.saved+evidence.loaded+evidence.resolved != 0 {
				t.Fatalf("unexpected exit, diagnostics, dispatch or evidence use; exit %d, want %d", got, want)
			}
			if strings.Contains(out.String()+diagnostic.String(), private) {
				t.Fatal("activity exposed private received bytes")
			}
			if want == 0 {
				if _, err := T.Decode[T.ActivityRead](out.Bytes()); err != nil {
					t.Fatal("success did not emit the shared activity envelope")
				}
			} else if _, err := T.Decode[T.NonterminalRefusal](out.Bytes()); err != nil {
				t.Fatal("failure did not emit the shared refusal envelope")
			}
		})
	}
}
