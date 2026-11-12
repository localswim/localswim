package v2

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestOwnerNoticeGroupsShareCardBound(t *testing.T) {
	event := ownerNoticeEvent{boardID: newID(), writerID: newOperationID(), label: strings.Repeat("\U0010ffff", 256)}
	all := ^activityOperationSet(0)
	const omitted = uint64(maxSnapshotInteger)
	event.addCard(ownerNoticeCard{activityCard{CardID: newID(), Ticket: omitted}, activityRestored.bit()})
	for ticket := omitted - 1; ticket >= omitted-maxOwnerNoticeCards; ticket-- {
		event.addCard(ownerNoticeCard{activityCard{CardID: newID(), Ticket: ticket}, all &^ activityRestored.bit()})
	}
	message := event.message()
	if !event.truncated || strings.Contains(message, fmt.Sprint(omitted)) || strings.Contains(message, "Restored (") {
		t.Fatal("evicted card left unbounded ticket or operation metadata")
	}
	for _, item := range event.cards {
		event.addCard(ownerNoticeCard{activityCard: item.activityCard, operations: activityRestored.bit()})
	}
	message = event.message()
	if len(event.cards) != maxOwnerNoticeCards || !strings.Contains(message, "Restored (") || len(message) > 32000 {
		t.Fatal("all operation groups must fit with the largest allowed card list and label")
	}
}

func TestOwnerNoticeWriterSurvivesActivationFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		human, bot := lifecycleTokens()
		app, err := Open(t.TempDir(), human, bot)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := app.Close(); err != nil {
				t.Error(err)
			}
		}()
		service := app.builds.service()
		id := newOperationID()
		if _, err := app.lifecycle.begin(service, matchingServiceTag(service.entityTag()), id); err != nil {
			t.Fatal(err)
		}
		if err := app.saveActivation(activationRecord{
			activation: activation{ID: id, BuildID: newID(), State: "accepted"},
			Origin:     service.ServiceInstanceID,
			Version:    "v2-test",
			Digest:     strings.Repeat("a", 64),
		}); err != nil {
			t.Fatal(err)
		}
		releaseStartup, err := LockServiceStartup(app.DataRoot())
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := releaseStartup(); err != nil {
				t.Error(err)
			}
		}()
		previous := app.store.ownerNotices
		previous.submit(ownerNoticeEvent{boardID: newID()})
		if pending, _ := previous.next(); pending == nil {
			t.Fatal("fixture did not queue a burst before handoff")
		}
		if err := app.ReleaseForActivation(); err != nil {
			t.Fatal(err)
		}
		if err := app.ResumeAfterActivationFailure(id); err != nil {
			t.Fatal(err)
		}
		resumed := app.store.ownerNotices
		pending, _ := resumed.next()
		oldPending, _ := previous.next()
		if resumed == previous || pending != nil || oldPending != nil || previous.ctx.Err() == nil {
			t.Fatal("activation recovery did not start a fresh worker and discard the old burst")
		}
		response := lifecycleRequest(app, human, http.MethodGet, "/service", "")
		status := turnOneDecode[serviceStatus](t, response.Body.Bytes())
		if response.Code != 200 || status.State != "running" ||
			status.ServiceInstanceID != service.ServiceInstanceID || snapshotString(status.RestartErrorCode) != "activation_handoff_failed" {
			t.Fatal("failed handoff did not resume the original service")
		}
		if resumed.writerID != status.ServiceInstanceID || resumed.writerID != app.store.activityWriterID {
			t.Fatal("resumed owner notices lost the activity writer identity")
		}
	})
}

func TestGroupedOwnerNoticeMatchesActivityInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		humanToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
		botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
		app, err := Open(t.TempDir(), humanToken, botToken)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := app.Close(); err != nil {
				t.Error(err)
			}
		}()
		app.store.ownerNotices.close()
		type delivery struct{ target, message string }
		var deliveries []delivery
		app.store.ownerNotices = newOwnerNoticeWorker(ownerNoticeQuietPeriod, app.store.ownerNoticeSettings,
			func(_ context.Context, target, message string) bool {
				deliveries = append(deliveries, delivery{target, message})
				return true
			})
		writer := app.store.activityWriterID
		app.store.ownerNotices.writerID = writer
		addr := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8810}
		handler := app.Handler(addr)
		call := func(token, method, path, body, match string, status int) *httptest.ResponseRecorder {
			t.Helper()
			req := httptest.NewRequest(method, Prefix+path, strings.NewReader(body))
			req.Host = addr.String()
			req.Header.Set("Authorization", "Bearer "+token)
			if body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if match != "" {
				req.Header.Set("If-Match", match)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != status {
				t.Fatalf("%s %s returned %d, want %d: %s", method, path, response.Code, status, response.Body)
			}
			return response
		}
		created := call(humanToken, http.MethodPost, "/boards", `{"name":"Synthetic grouped notice"}`, "", 201)
		b := turnOneDecode[board](t, created.Body.Bytes())
		boardTag := b.entityTag()
		makeCard := func() card {
			t.Helper()
			res := call(botToken, http.MethodPost, "/cards", fmt.Sprintf(
				`{"board_id":%q,"expected_board_etag":%q,"title":"Private synthetic card","lane_id":%q}`,
				b.ID, boardTag, b.LaneIDs[0]), "", 201)
			boardTag = res.Header().Get("X-Board-ETag")
			return turnOneDecode[card](t, res.Body.Bytes())
		}
		patch := func(token string, item card, body string) card {
			t.Helper()
			res := call(token, http.MethodPatch, "/cards/"+item.ID, body, item.entityTag(), 200)
			boardTag = res.Header().Get("X-Board-ETag")
			return turnOneDecode[card](t, res.Body.Bytes())
		}
		review, moved, automated := makeCard(), makeCard(), makeCard()
		for _, laneID := range b.LaneIDs[1:4] {
			review = patch(botToken, review, fmt.Sprintf(`{"lane_id":%q}`, laneID))
		}
		settingsPath := "/notifications/" + b.ID
		settings := call(humanToken, http.MethodGet, settingsPath, "", "", 200)
		call(humanToken, http.MethodPatch, settingsPath,
			fmt.Sprintf(`{"enabled":true,"target_thread_id":%q}`, ownerNoticeTestTarget), settings.Header().Get("ETag"), 200)

		time.Sleep(time.Second)
		from := canonicalTime(time.Now()).Format(timestampLayout)
		approved := call(humanToken, http.MethodPost, "/approvals", fmt.Sprintf(
			`{"board_id":%q,"expected_board_etag":%q,"cards":[{"card_id":%q,"etag":%q}]}`,
			b.ID, boardTag, review.ID, review.entityTag()), "", 201)
		receipt := turnOneDecode[approval](t, approved.Body.Bytes())
		time.Sleep(time.Second)
		moved = patch(humanToken, moved, fmt.Sprintf(`{"lane_id":%q}`, b.LaneIDs[1]))
		time.Sleep(time.Second)
		automated = patch(botToken, automated, `{"title":"Private Bot change"}`)
		call(humanToken, http.MethodPost, "/comments", fmt.Sprintf(
			`{"card_id":%q,"expected_card_etag":%q,"text":%q}`, moved.ID, moved.entityTag(), reviewApprovalText), "", 201)
		res := call(humanToken, http.MethodGet, "/cards/"+moved.ID, "", "", 200)
		moved = turnOneDecode[card](t, res.Body.Bytes())
		time.Sleep(time.Second)
		moved = patch(humanToken, moved, `{"archived":true}`)
		time.Sleep(time.Second)
		moved = patch(humanToken, moved, `{"archived":false}`)
		to := canonicalTime(time.Now()).Format(timestampLayout)

		time.Sleep(ownerNoticeQuietPeriod - time.Second)
		call(humanToken, http.MethodPatch, "/cards/"+review.ID, `{"title":"Uncommitted edit"}`, review.entityTag(), 412)
		moved = patch(humanToken, moved, `{"archived":false}`)
		synctest.Wait()
		if len(deliveries) != 0 {
			t.Fatal("notice escaped the quiet period")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(deliveries) != 1 || deliveries[0].target != ownerNoticeTestTarget {
			t.Fatal("expected one burst for the disposable recipient")
		}
		message := deliveries[0].message
		want := "\n- Operations: Comment added (0002), Moved lanes (0002), Reviewed & approved (0001), Archived (0002), Restored (0002)\n"
		for _, field := range []string{want, "- Writer: `" + writer + "`", "- Start: " + from, "- End: " + to,
			fmt.Sprintf("- Cards: 0001 (`%s`), 0002 (`%s`)", review.ID, moved.ID), "- Tickets: 0001, 0002"} {
			if !strings.Contains(message, field) {
				t.Errorf("notice missing expected metadata %q", field)
			}
		}
		for _, excluded := range []string{"Private", "Uncommitted", reviewApprovalText, humanToken, botToken, automated.ID} {
			if strings.Contains(message, excluded) {
				t.Error("notice leaked prose, credentials, or unrelated activity")
			}
		}
		if !strings.HasSuffix(message, "- Tickets: 0001, 0002") {
			t.Fatal("notice tickets included unrelated activity")
		}

		query := url.Values{"board_id": {b.ID}, "writer_id": {writer}, "from": {from}, "to": {to}}
		res = call(humanToken, http.MethodGet, "/activities?"+query.Encode(), "", "", 200)
		page := turnOneDecode[activityTestPage[activityMetadata]](t, res.Body.Bytes())
		if page.WriterID != writer || !page.Coverage.Complete || !page.PaginationComplete || page.BotOnly {
			t.Fatal("notice interval did not verify as complete mixed activity")
		}
		wantGroups := []string{"Reviewed & approved", "Reviewed & approved", "Moved lanes", "Contents modified",
			"Comment added", "Archived", "Restored"}
		wantCards := []card{review, review, moved, automated, moved, moved, moved}
		if len(page.Values) != len(wantGroups) {
			t.Fatalf("interval returned %d events, want %d", len(page.Values), len(wantGroups))
		}
		for i, row := range page.Values {
			item := wantCards[i]
			actor := humanID
			if item.ID == automated.ID {
				actor = botID
			}
			if snapshotString(row.OperationGroup) != wantGroups[i] || row.ActorID != actor ||
				!slices.Equal(row.Cards, []activityCard{{CardID: item.ID, Ticket: item.Ticket}}) {
				t.Errorf("interval event %d did not correlate actor, operation group, and event-time card", i)
			}
		}
		if page.Values[0].ID != receipt.ID || page.Values[1].Revision != receipt.BoardRevision {
			t.Fatal("approval group did not correlate both atomic receipt events")
		}

		patch(botToken, automated, `{"title":"Bot-only edit after the burst"}`)
		if err := os.WriteFile(app.store.snapshotPath(b.ID), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		call(humanToken, http.MethodPatch, "/cards/"+moved.ID, `{"title":"Write refused by durable admission"}`, moved.entityTag(), 503)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
		if len(deliveries) != 1 {
			t.Fatal("Bot-only or failed write produced a notice")
		}
	})
}
