package v2

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
			fields, err := decodeObject([]byte(test.patch))
			if err != nil {
				t.Fatal(err)
			}
			got, err := patchNotifications(test.current, fields)
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
		fields, err := decodeObject([]byte(patch))
		if err != nil {
			continue // Malformed or non-object requests never reach the patch.
		}
		got, err := patchNotifications(current, fields)
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
		fields, err := decodeObject([]byte(patch))
		if err != nil {
			continue // Malformed or non-object requests never reach the patch.
		}
		got, err := patchNotifications(current, fields)
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
		tag, path := created.Header().Get("ETag"), "/notifications/"+state.ID
		reply := send(human, http.MethodGet, path, "", "", 200)
		route := turnOneDecode[notificationRoute](t, reply.Body.Bytes())
		routeTag := reply.Header().Get("ETag")
		applySettings := func(patch string) {
			t.Helper()
			reply := send(human, http.MethodPatch, path, patch, routeTag, 200)
			route = turnOneDecode[notificationRoute](t, reply.Body.Bytes())
			routeTag = reply.Header().Get("ETag")
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
		if route.Enabled || len(attempts) != 0 {
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
		if !route.Enabled {
			t.Fatal("retargeting disabled the active route")
		}
		edit(human)
		applySettings(`{"enabled":false}`)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
		if len(attempts) != 1 || route.Enabled ||
			route.TargetThreadID == nil || *route.TargetThreadID != ownerNoticeTestTarget {
			t.Fatal("disabling did not discard the pending burst and retain the target")
		}
	})
}

func TestM80NotificationRouteSurvivesRestoreAndRestart(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Portable board"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	boardPath, routePath := "/boards/"+b.ID, "/notifications/"+b.ID
	raw, _ = editor.call("PATCH", boardPath, `{"publishing":{"enabled":true,"repository":"origin"}}`, b.entityTag(), 200)
	b = turnOneDecode[board](t, raw)

	_, header := editor.call("GET", routePath, "", "", 200)
	if header.Get("Cache-Control") != "no-store" {
		t.Fatal("notification settings must be uncached")
	}
	_, header = editor.call("PATCH", routePath, fmt.Sprintf(`{"enabled":true,"target_thread_id":%q}`, ownerNoticeTestTarget),
		header.Get("ETag"), 200)
	backupID := newOperationID()
	editor.call("POST", "/backups", fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q}`,
		backupID, b.ID, b.entityTag()), "", 201)

	_, header = editor.call("PATCH", routePath, fmt.Sprintf(`{"target_thread_id":%q}`, ownerNoticeNextTarget),
		header.Get("ETag"), 200)
	raw, header = editor.call("PATCH", routePath, `{"enabled":false}`, header.Get("ETag"), 200)
	disabled := turnOneDecode[notificationRoute](t, raw)
	routeTag := header.Get("ETag")
	if disabled.Enabled || snapshotString(disabled.TargetThreadID) != ownerNoticeNextTarget {
		t.Fatal("the route was not disabled with the new recipient retained")
	}

	// The backup must restore a real board change while the independent route stays current.
	raw, _ = editor.call("PATCH", boardPath, `{"name":"Intervening edit"}`, b.entityTag(), 200)
	edited := turnOneDecode[board](t, raw)
	_, restored := editor.call("POST", "/restorations", fmt.Sprintf(`{"board_id":%q,"backup_id":%q,"expected_board_etag":%q}`,
		b.ID, backupID, edited.entityTag()), "", 201)
	raw, boardHeader := editor.call("GET", boardPath, "", "", 200)
	if state := turnOneDecode[board](t, raw); state.Name != b.Name || state.Revision <= edited.Revision ||
		boardHeader.Get("ETag") != restored.Get("X-Board-ETag") {
		t.Fatal("restore did not replace board state")
	}
	checkRoute := func(c turnOneClient) {
		t.Helper()
		raw, headers := c.call("GET", routePath, "", "", 200)
		got := turnOneDecode[notificationRoute](t, raw)
		if got.BoardID != b.ID || got.Revision != disabled.Revision || got.Enabled ||
			snapshotString(got.TargetThreadID) != ownerNoticeNextTarget || headers.Get("ETag") != routeTag {
			t.Fatal("restore or restart changed saved notification routing")
		}
	}
	checkRoute(editor)

	assertPortable := func(data []byte) {
		t.Helper()
		if bytes.Contains(data, []byte("notifications")) || bytes.Contains(data, []byte("target_thread_id")) ||
			bytes.Contains(data, []byte(ownerNoticeTestTarget)) || bytes.Contains(data, []byte(ownerNoticeNextTarget)) {
			t.Fatal("operational notification data escaped to portable state")
		}
	}
	assertPortable(raw)
	raw, _ = editor.call("GET", "/snapshots/"+b.ID, "", "", 200)
	assertPortable(raw)
	bounds := url.Values{
		"board_id": {b.ID}, "view": {"full"},
		"from": {b.CreatedAt.Format(timestampLayout)}, "to": {canonicalTime(time.Now()).Format(timestampLayout)},
	}
	raw, _ = editor.call("GET", "/activities?"+bounds.Encode(), "", "", 200)
	assertPortable(raw)
	if strings.Contains(string(raw), "Intervening edit") || !strings.Contains(string(raw), recoveryOperation) {
		t.Fatal("full activity did not report restored lineage")
	}
	content, _ := backupHTTPContent(t, editor, backupID, http.MethodGet, "application/zip", 200)
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(file)
		if closeErr := file.Close(); readErr != nil || closeErr != nil {
			t.Fatalf("could not read backup entry: %v, %v", readErr, closeErr)
		}
		assertPortable([]byte(entry.Name))
		assertPortable(data)
	}
	capture, err := app.store.captureBoardPublication(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.files) != 1 || capture.files[0].path != "boards/"+b.ID+".json" {
		t.Fatal("board publication captured unexpected paths")
	}
	data, err := capture.files[0].read()
	if err != nil {
		t.Fatal(err)
	}
	assertPortable(data)

	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	_, restarted := restartBoardPublication(t, app.store.root, editor.token)
	checkRoute(restarted)
}

func TestM80MalformedNotificationRouteFailsClosed(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Disposable board"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	path := "/notifications/" + b.ID
	_, header := editor.call("GET", path, "", "", 200)
	etag := header.Get("ETag")
	invalid := fmt.Sprintf(`{"board_id":%q,"revision":2,"enabled":null,"target_thread_id":%q}`,
		b.ID, ownerNoticeTestTarget)
	routeFile := app.store.notificationRoutePath(b.ID)
	if err := os.WriteFile(routeFile, []byte(invalid), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		raw, _ := editor.call(method, path, `{"enabled":false}`, etag, 503)
		if bytes.Contains(raw, []byte(ownerNoticeTestTarget)) || bytes.Contains(raw, []byte(routeFile)) {
			t.Fatal("route failure exposed private routing data")
		}
	}
	if settings, ok := app.store.ownerNoticeSettings(b.ID); ok || ownerNoticeEnabled(settings) {
		t.Fatal("malformed route remained eligible for delivery")
	}
	saved, err := os.ReadFile(routeFile)
	if err != nil || string(saved) != invalid {
		t.Fatal("settings patch overwrote the malformed route")
	}
	raw, header = editor.call("GET", "/boards/"+b.ID, "", "", 200)
	if header.Get("ETag") != b.entityTag() || bytes.Contains(raw, []byte(ownerNoticeTestTarget)) {
		t.Fatal("malformed route leaked into board reads")
	}
}
