package v2

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestNotificationSettingsPatch(t *testing.T) {
	target := "abcdef01-2345-7678-abcd-ef0123456789"
	configured := notifications{TargetThreadID: &target}
	enabled := notifications{Enabled: true, TargetThreadID: &target}
	tests := []struct {
		name    string
		current notifications
		patch   string
		want    notifications
	}{
		{"target stays off and normalizes", notifications{}, `{"target_thread_id":"ABCDEF01-2345-7678-ABCD-EF0123456789"}`, configured},
		{"explicit enable retains target", configured, `{"enabled":true}`, enabled},
		{"target preserves enabled", enabled, `{"target_thread_id":"ABCDEF01-2345-7678-ABCD-EF0123456789"}`, enabled},
		{"explicit disable retains target", enabled, `{"enabled":false}`, configured},
		{"clear while disabled", configured, `{"target_thread_id":null}`, notifications{}},
		{"disable and clear atomically", enabled, `{"enabled":false,"target_thread_id":null}`, notifications{}},
		{"enable and target atomically", notifications{}, `{"enabled":true,"target_thread_id":"ABCDEF01-2345-7678-ABCD-EF0123456789"}`, enabled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := patchNotifications(test.current, json.RawMessage(test.patch))
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("patch %s produced %+v, %v, want %+v", test.patch, got, err, test.want)
			}
		})
	}
}

func TestNotificationSettingsRejectsInvalidPatch(t *testing.T) {
	target := "abcdef01-2345-8678-bbcd-ef0123456789"
	current := notifications{Enabled: true, TargetThreadID: &target}
	for _, patch := range []string{
		`null`, `[]`, `{}`, `{"Enabled":true}`, `{"enabled":true,"enabled":false}`,
		`{"enabled":null}`, `{"enabled":0}`, `{"enabled":"false"}`,
		`{"enabled":false,"actor_id":"ignored"}`, `{"target_thread_id":true}`,
		`{"target_thread_id":"abcdef01-2345-9678-abcd-ef0123456789"}`,
		`{"target_thread_id":"abcdef01-2345-8678-cbcd-ef0123456789"}`,
		`{"target_thread_id":null}`,
	} {
		got, err := patchNotifications(current, json.RawMessage(patch))
		if err == nil || !reflect.DeepEqual(got, current) {
			t.Errorf("patch %s produced %+v, %v, want unchanged settings and error", patch, got, err)
		}
	}
}

func TestNotificationSettingsRequiresTargetWhenEnabled(t *testing.T) {
	current := notifications{}
	for _, patch := range []string{
		`{"enabled":true}`, `{"enabled":true,"target_thread_id":null}`,
	} {
		got, err := patchNotifications(current, json.RawMessage(patch))
		if err == nil || got != current {
			t.Errorf("patch %s produced %+v, %v, want unchanged settings and error", patch, got, err)
		}
	}
	if err := (notifications{Enabled: true}).validate(); err == nil {
		t.Fatal("accepted targetless enabled settings")
	}
}

func TestNotificationSettingsControlHumanQuietBurst(t *testing.T) {
	root := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
		bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
		app, err := Open(root, human, bot)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := app.Close(); err != nil {
				t.Error(err)
			}
		}()

		type attempt struct{ target, message string }
		var attempts []attempt
		app.store.ownerNotices.close()
		app.store.ownerNotices = newOwnerNoticeWorker(ownerNoticeQuietPeriod, app.store.ownerNoticeSettings,
			func(_ context.Context, target, message string) bool {
				attempts = append(attempts, attempt{target, message})
				return true
			})

		address := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
		handler := app.Handler(address)
		send := func(token, method, path, body, match string, want int) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, Prefix+path, strings.NewReader(body))
			req.Host = address.String()
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			if match != "" {
				req.Header.Set("If-Match", match)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != want {
				t.Fatalf("%s %s status %d, want %d: %s", method, path, recorder.Code, want, recorder.Body.Bytes())
			}
			return recorder
		}
		created := send(human, http.MethodPost, "/boards", `{"name":"Synthetic notice"}`, "", 201)
		state := turnOneDecode[board](t, created.Body.Bytes())
		tag, path := state.ETag, "/boards/"+state.ID
		applySettings := func(patch string) {
			t.Helper()
			reply := send(human, http.MethodPatch, path, `{"notifications":`+patch+`}`, tag, 200)
			state = turnOneDecode[board](t, reply.Body.Bytes())
			tag = state.ETag
		}
		edit := func(token string) {
			t.Helper()
			reply := send(token, http.MethodPost, "/cards", fmt.Sprintf(
				`{"board_id":%q,"expected_board_etag":%q,"title":"Synthetic title","lane_id":%q}`,
				state.ID, tag, state.LaneIDs[0]), "", 201)
			tag = reply.Header().Get("X-Board-ETag")
		}

		applySettings(`{"target_thread_id":"` + ownerNoticeTestTarget + `"}`)
		edit(human)
		applySettings(`{"target_thread_id":"` + ownerNoticeNextTarget + `"}`)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
		if state.Board.Notifications.Enabled || len(attempts) != 0 {
			t.Fatal("setting or changing a disabled target activated delivery")
		}

		applySettings(`{"enabled":true}`)
		edit(human)
		time.Sleep(10 * time.Second)
		edit(human)
		time.Sleep(5 * time.Second)
		edit(bot)
		synctest.Wait()
		if len(attempts) != 0 {
			t.Fatal("notice was sent before the final Human edit's quiet deadline")
		}
		time.Sleep(10 * time.Second)
		synctest.Wait()
		if len(attempts) != 1 || attempts[0].target != ownerNoticeNextTarget ||
			!strings.Contains(attempts[0].message+"\n", "\n- Tickets: 0002, 0003\n") ||
			strings.Contains(attempts[0].message, "Synthetic title") {
			t.Fatal("expected one Human burst delivered to the configured fake recipient")
		}

		applySettings(`{"target_thread_id":"` + ownerNoticeTestTarget + `"}`)
		if !state.Board.Notifications.Enabled {
			t.Fatal("retargeting disabled the active route")
		}
		edit(human)
		applySettings(`{"enabled":false}`)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
		if len(attempts) != 1 || state.Board.Notifications.Enabled ||
			state.Board.Notifications.TargetThreadID == nil || *state.Board.Notifications.TargetThreadID != ownerNoticeTestTarget {
			t.Fatal("disabling did not discard the pending burst and retain the target")
		}
	})
}
