package v2

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestM04CardTextConflictAndLostReply(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	handler := app.Handler(server.Listener.Addr().(*net.TCPAddr))
	lostID := newID()
	var lostCalls atomic.Int32
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Request-ID") == lostID {
			lostCalls.Add(1)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code == http.StatusOK {
				panic(http.ErrAbortHandler)
			}
			w.WriteHeader(response.Code)
			return
		}
		handler.ServeHTTP(w, req)
	})
	server.Start()
	t.Cleanup(server.Close)
	editor := turnOneClient{t, server.URL, human, server.Client()}
	reader := editor
	reader.token = bot
	id, now := newID(), canonicalTime(time.Now())
	wf := defaultWorkflow(id, now, app.principals)
	for i := range wf.Policy.ResourceGrants {
		grant := &wf.Policy.ResourceGrants[i]
		if grant.UserID == botID {
			grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(value string) bool { return value == "edit_card" })
		}
	}
	initial := freshSnapshot(id, "Text authorization", wf, humanID, now)
	if err := app.store.create(initial); err != nil {
		t.Fatal(err)
	}
	makeCard := func(tag string) (card, string) {
		t.Helper()
		body := fmt.Sprintf("{\"board_id\":%q,\"expected_board_etag\":%q,\"lane_id\":%q,\"title\":\"Synthetic\"}", id, tag, wf.Lanes[0].ID)
		raw, headers := editor.call("POST", "/cards", body, "", 201)
		return turnOneDecode[card](t, raw), headers.Get("X-Board-ETag")
	}
	first, tag := makeCard(initial.Board.ETag)
	second, _ := makeCard(tag)
	path := "/cards/" + first.ID
	reader.call("GET", path, "", "", 200)
	reader.call("PATCH", path, `{"title":"Denied"}`, first.entityTag(), 403)
	editor.call("PATCH", path, `{"title":"Missing precondition"}`, "", 428)
	editor.call("PATCH", path, `{"title":"Invalid description","description":9}`, first.entityTag(), 400)
	editor.call("PATCH", path, `{"title":"\ud800"}`, first.entityTag(), 400)
	editor.call("PATCH", "/cards/"+second.ID, `{"title":"Unrelated change"}`, second.entityTag(), 200)

	title, description := "  \U0001f41d e\u0301  ", "  exact\r\ntext\n "
	body, err := marshalCanonical(map[string]string{"title": title, "description": description})
	if err != nil {
		t.Fatal(err)
	}
	raw, headers := editor.call("PATCH", path, string(body), first.entityTag(), 200)
	changed := turnOneDecode[card](t, raw)
	if changed.Title != title || changed.Description != description || changed.Revision != 5 ||
		changed.entityTag() == first.entityTag() || changed.entityTag() != headers.Get("ETag") || headers.Get("X-Board-ETag") == "" {
		t.Fatal("unrelated edit invalidated the card tag or text was changed in transit")
	}
	editor.call("PATCH", path, `{"title":"Stale edit"}`, first.entityTag(), 412)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPatch, server.URL+Prefix+path, strings.NewReader(`{"description":null}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+human)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", changed.entityTag())
	req.Header.Set("X-Request-ID", lostID)
	res, err := editor.client.Do(req)
	if res != nil {
		_ = res.Body.Close()
	}
	if err == nil || lostCalls.Load() != 1 {
		t.Fatal("expected a single attempt with an unobservable PATCH response")
	}
	raw, _ = editor.call("GET", path, "", "", 200)
	readback := turnOneDecode[card](t, raw)
	if readback.Title != title || readback.Description != "" || readback.Revision != 6 || readback.entityTag() == changed.entityTag() {
		t.Fatal("readback failed to establish the final text and current card tag")
	}
	_, headers = editor.call("PATCH", path, `{"description":null}`, readback.entityTag(), 200)
	if headers.Get("ETag") != readback.entityTag() {
		t.Fatal("unchanged text advanced the card tag")
	}
	saved, err := app.store.durable(id)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Board.Revision != 6 || len(saved.Activity) != 7 {
		t.Fatalf("expected one board revision per real edit, got %d revisions and %d activities", saved.Board.Revision, len(saved.Activity))
	}
	for i, want := range []struct{ field, before, after string }{
		{"title", first.Title, title}, {"description", "", description}, {"description", description, ""},
	} {
		event := saved.Activity[4+i]
		if event.ActorID != humanID || event.ResourceID != first.ID || event.Operation != "patch" ||
			event.Revision != 5+uint64(i/2) || event.Field == nil || *event.Field != want.field ||
			event.Before == nil || *event.Before != want.before || event.After == nil || *event.After != want.after {
			t.Fatalf("text edit audit %d differs from its saved change", i)
		}
	}
}
