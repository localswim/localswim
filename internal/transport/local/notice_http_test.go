package local

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/board/noticemonitor"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestNoticeLiveControlStrictRequests(t *testing.T) {
	const inc = "10000000-0000-4000-8000-000000000001"
	const gen = "10000000-0000-4000-8000-000000000002"
	const target = "10000000-0000-4000-8000-000000000003"
	base := `"incarnation":"` + inc + `","expectedControlGeneration":"` + gen + `","expectedConfigurationRevision":1`
	for _, action := range []string{"configure", "start", "stop", "recover", "reload", "retarget", "lookup"} {
		t.Run(action, func(t *testing.T) {
			fields := base
			if action == "retarget" || action == "lookup" {
				fields += `,"targetId":"` + target + `"`
			}
			if action == "lookup" {
				fields += `,"deliveryId":"` + strings.Repeat("a", 64) + `"`
			}
			wire := `{` + fields + `}`
			cases := map[string]string{
				"valid":               wire,
				"missing-revision":    strings.Replace(wire, `,"expectedConfigurationRevision":1`, "", 1),
				"duplicate-revision":  strings.Replace(wire, `"expectedConfigurationRevision":1`, `"expectedConfigurationRevision":1,"expectedConfigurationRevision":1`, 1),
				"fractional-revision": strings.Replace(wire, `"expectedConfigurationRevision":1`, `"expectedConfigurationRevision":1.0`, 1),
				"overflow-revision":   strings.Replace(wire, `"expectedConfigurationRevision":1`, `"expectedConfigurationRevision":9007199254740992`, 1),
				"missing-generation":  strings.Replace(wire, `"expectedControlGeneration":"`+gen+`",`, "", 1),
				"extra-field":         strings.TrimSuffix(wire, "}") + `,"path":"PRIVATE-PATH"}`,
			}
			for name, body := range cases {
				t.Run(name, func(t *testing.T) {
					q := httptest.NewRequest("POST", "http://localhost/notices/"+action, strings.NewReader(body))
					q.Header.Set("Content-Type", "application/json")
					got, status := parseNoticeRequest(httptest.NewRecorder(), q, "/notices/")
					if name == "valid" {
						if status != 0 || got.revision != 1 || got.generation != gen || got.selected != inc {
							t.Fatal("lost coordinated control context", status)
						}
					} else if status != http.StatusBadRequest {
						t.Fatal("invalid strict request admitted", name, status)
					}
				})
			}
		})
	}
}

func TestNoticeUnknownEnvelopeAndForeignRedaction(t *testing.T) {
	w := httptest.NewRecorder()
	refuseNotice(w, noticemonitor.ErrOutcomeUnknown)
	unknown, err := T.Decode[T.NoticeOutcomeUnknown](w.Body.Bytes())
	if w.Code != 503 || err != nil || unknown.Error.Code != "OUTCOME_UNKNOWN" || unknown.Error.Message != T.NoticeUnknownMessage ||
		bytes.Contains(w.Body.Bytes(), []byte("result")) || bytes.Contains(w.Body.Bytes(), []byte("targetId")) || bytes.Contains(w.Body.Bytes(), []byte("field")) {
		t.Fatal("post-admission refusal disclosed private facts", err)
	}
	for _, c := range []struct {
		err    error
		code   string
		status int
	}{{noticemonitor.ErrStale, "CONTROL_STALE", 409}, {noticemonitor.ErrBusy, "CONTROL_BUSY", 409}, {notices.ErrConfigurationExhausted, "CONFIGURATION_EXHAUSTED", 409}} {
		w = httptest.NewRecorder()
		refuseNotice(w, c.err)
		if w.Code != c.status || !bytes.Contains(w.Body.Bytes(), []byte(c.code)) {
			t.Fatal("pre-admission refusal changed")
		}
	}
	view := noticeView(A.Identity{BoardID: "10000000-0000-4000-8000-000000000001", Incarnation: "10000000-0000-4000-8000-000000000002"}, noticemonitor.Status{Generation: "10000000-0000-4000-8000-000000000003", Phase: "guarded", Reason: "foreign"})
	if view.TargetID != nil || view.ConfigurationRevision != 0 || view.ControlOperation != nil || view.Pending != nil || len(view.Entries) != 0 {
		t.Fatal("foreign view disclosed private control facts")
	}
}
