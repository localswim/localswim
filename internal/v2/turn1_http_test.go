package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type turnOneClient struct {
	t          *testing.T
	url, token string
	client     *http.Client
}

func (c turnOneClient) call(method, path, body, match string, want int) ([]byte, http.Header) {
	c.t.Helper()
	req, err := http.NewRequest(method, c.url+Prefix+path, strings.NewReader(body))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if match != "" {
		req.Header.Set("If-Match", match)
	}
	response, err := c.client.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if response.StatusCode != want {
		c.t.Fatalf("%s %s status %d, want %d: %s", method, path, response.StatusCode, want, data)
	}
	checkSnakeCaseResponse(c.t, turnOneDecode[any](c.t, data))
	return data, response.Header
}

var snakeCaseField = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func checkSnakeCaseResponse(t *testing.T, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			if !snakeCaseField.MatchString(key) {
				t.Fatalf("API response key %q is not snake_case", key)
			}
			checkSnakeCaseResponse(t, child)
		}
	case []any:
		for _, child := range value {
			checkSnakeCaseResponse(t, child)
		}
	}
}

func turnOneDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := unmarshalCanonical(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

type collectionPage[T any] struct {
	Values     []T
	NextCursor *string
}

func turnOnePage[T any](t *testing.T, data []byte, resource string) collectionPage[T] {
	t.Helper()
	fields := turnOneDecode[map[string]json.RawMessage](t, data)
	values, hasValues := fields[resource]
	cursor, hasCursor := fields["next_cursor"]
	if len(fields) != 2 || !hasValues || !hasCursor || len(values) == 0 || values[0] != '[' {
		t.Fatalf("expected only %s array and next_cursor: %s", resource, data)
	}
	return collectionPage[T]{
		Values:     turnOneDecode[[]T](t, values),
		NextCursor: turnOneDecode[*string](t, cursor),
	}
}

func TestTurnOneLiveHTTP(t *testing.T) {
	root := t.TempDir()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	start := func() (turnOneClient, func()) {
		t.Helper()
		app, err := Open(root, human, bot)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(nil)
		server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
		server.Start()
		client := turnOneClient{t, server.URL, human, server.Client()}
		raw, _ := client.call("GET", "/service", "", "", 200)
		if got := turnOneDecode[serviceStatus](t, raw).Port; got != server.Listener.Addr().(*net.TCPAddr).Port {
			t.Fatalf("service port = %d, want bound listener port", got)
		}
		return client, func() {
			server.Close()
			if err := app.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	c, stop := start()
	checkTurnOneBoundary(t, c)
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	if competing, err := Open(root, human, bot); err == nil {
		_ = competing.Close()
		t.Fatal("a second writer opened the same data directory")
	}

	raw, _ := c.call("GET", "/boards", "", "", 200)
	if page := turnOnePage[boardSummary](t, raw, "boards"); len(page.Values) != 0 || page.NextCursor != nil {
		t.Fatal("expected an empty terminal board page")
	}
	raw, _ = c.call("GET", "/builds", "", "", 200)
	if page := turnOnePage[build](t, raw, "builds"); len(page.Values) != 0 || page.NextCursor != nil {
		t.Fatal("expected an empty terminal build page")
	}
	raw, h := c.call("POST", "/boards", `{"name":"First"}`, "", 201)
	first := turnOneDecode[board](t, raw)
	if h.Get("Location") != Prefix+"/boards/"+first.ID || h.Get("ETag") != first.entityTag() {
		t.Fatal("board location or revision missing")
	}
	raw, _ = c.call("POST", "/boards", `{"name":"Second"}`, "", 201)
	second := turnOneDecode[board](t, raw)
	raw, _ = c.call("GET", "/boards", "", "", 200)
	if got := turnOnePage[boardSummary](t, raw, "boards"); len(got.Values) != 2 || first.ID == second.ID {
		t.Fatal("expected two isolated boards")
	}

	for _, resource := range []string{"users", "lanes", "priorities"} {
		raw, _ = c.call("GET", "/"+resource+"?board_id="+first.ID, "", "", 200)
		if page := turnOnePage[json.RawMessage](t, raw, resource); len(page.Values) == 0 || page.NextCursor != nil {
			t.Fatalf("expected nonempty terminal %s page", resource)
		}
	}
	raw, _ = c.call("GET", "/boards?limit=1", "", "", 200)
	firstPage := turnOnePage[boardSummary](t, raw, "boards")
	if len(firstPage.Values) != 1 || firstPage.NextCursor == nil {
		t.Fatal("expected one board and a continuation cursor")
	}
	raw, _ = c.call("GET", "/boards?limit=1&cursor="+*firstPage.NextCursor, "", "", 200)
	lastPage := turnOnePage[boardSummary](t, raw, "boards")
	if len(lastPage.Values) != 1 || lastPage.NextCursor != nil || firstPage.Values[0].ID == lastPage.Values[0].ID {
		t.Fatal("expected the remaining distinct board and a terminal cursor")
	}
	verifyTurnOneCustomWorkflow(t, c, bot)
	firstPath := "/boards/" + first.ID
	raw, h = c.call("GET", firstPath, "", "", 200)
	if createdAt := turnOneDecode[map[string]any](t, raw)["created_at"]; len(createdAt.(string)) != 25 {
		t.Fatal("timestamp does not use UTC whole seconds")
	}
	c.call("PATCH", firstPath, `{"name":"Discarded","notifications":{"enabled":true}}`, h.Get("ETag"), 400)
	noticePath := "/notifications/" + first.ID
	raw, h = c.call("GET", noticePath, "", "", 200)
	notice := turnOneDecode[notificationRoute](t, raw)
	if notice.Enabled || notice.TargetThreadID != nil || notice.Revision != 1 || h.Get("ETag") == first.entityTag() {
		t.Fatal("new board route is not independently disabled")
	}
	c.call("PATCH", noticePath, `{"enabled":true}`, h.Get("ETag"), 400)
	targetPatch := `{"target_thread_id":"018E7F0A-BCDE-7ABC-8DEF-0123456789AB"}`
	c.call("PATCH", noticePath, targetPatch, "", 428)
	c.call("PATCH", noticePath, targetPatch, first.entityTag(), 412)
	raw, h = c.call("PATCH", noticePath, targetPatch, h.Get("ETag"), 200)
	notice = turnOneDecode[notificationRoute](t, raw)
	if notice.Enabled || notice.TargetThreadID == nil || *notice.TargetThreadID != "018e7f0a-bcde-7abc-8def-0123456789ab" {
		t.Fatal("target-only update did not preserve off and normalize target")
	}
	staleNoticeTag := h.Get("ETag")
	raw, h = c.call("PATCH", noticePath, `{"enabled":true}`, staleNoticeTag, 200)
	c.call("PATCH", noticePath, `{"enabled":false}`, staleNoticeTag, 412)
	c.call("PATCH", noticePath, `{"target_thread_id":null}`, h.Get("ETag"), 400)
	raw, h = c.call("PATCH", noticePath, `{"target_thread_id":"018e7f0a-bcde-7abc-8def-0123456789ac"}`, h.Get("ETag"), 200)
	notice = turnOneDecode[notificationRoute](t, raw)
	if !notice.Enabled {
		t.Fatal("retarget disabled notifications")
	}
	noticeTag := h.Get("ETag")
	raw, h = c.call("GET", "/workspace", "", "", 200)
	originalWorkspaceTag := h.Get("ETag")
	if fields := turnOneDecode[map[string]any](t, raw); len(fields) != 3 || fields["etag"] != nil {
		t.Fatal("workspace GET must publish revision, pins and publishing without JSON etag")
	}
	if turnOneDecode[map[string]any](t, raw)["revision"] != float64(1) {
		t.Fatal("workspace response did not publish its initial revision")
	}
	pins := `{"pinned_board_ids":["` + second.ID + `","` + first.ID + `"]}`
	raw, h = c.call("PATCH", "/workspace", pins, originalWorkspaceTag, 200)
	pinsTag := h.Get("ETag")
	if fields := turnOneDecode[map[string]any](t, raw); len(fields) != 3 || fields["etag"] != nil || fields["revision"] != float64(2) {
		t.Fatal("workspace PATCH response must publish its numeric revision without JSON etag")
	}
	if pinsTag == originalWorkspaceTag {
		t.Fatal("workspace version did not advance")
	}
	c.call("PATCH", "/workspace", `{"pinned_board_ids":[]}`, originalWorkspaceTag, 412)
	stop()
	stop = nil
	c, stop = start()
	raw, _ = c.call("GET", firstPath, "", "", 200)
	saved := turnOneDecode[board](t, raw)
	if saved.ID != first.ID || saved.Name != first.Name || saved.entityTag() != first.entityTag() {
		t.Fatal("notice settings modified the board")
	}
	raw, h = c.call("GET", noticePath, "", "", 200)
	if got := turnOneDecode[notificationRoute](t, raw); !got.Enabled || h.Get("ETag") != noticeTag ||
		!sameText(got.TargetThreadID, notice.TargetThreadID) {
		t.Fatal("notice settings did not survive restart")
	}
	raw, _ = c.call("GET", "/notifications/"+second.ID, "", "", 200)
	if got := turnOneDecode[notificationRoute](t, raw); got.BoardID != second.ID || got.TargetThreadID != nil || got.Enabled {
		t.Fatal("second board did not have an isolated disabled route")
	}
	other := c
	other.token = bot
	raw, h = other.call("GET", "/workspace", "", "", 200)
	result := turnOneDecode[workspace](t, raw)
	if result.Revision != 2 || h.Get("ETag") != pinsTag || len(result.PinnedBoardIDs) != 2 || result.PinnedBoardIDs[0] != second.ID {
		t.Fatal("another client did not see persisted pin order")
	}
	raw, _ = c.call("PATCH", noticePath, `{"enabled":false}`, noticeTag, 200)
	if got := turnOneDecode[notificationRoute](t, raw); got.Enabled || got.TargetThreadID == nil {
		t.Fatal("disable discarded target")
	}
	stop()
	stop = nil
	c, stop = start()
	raw, _ = c.call("GET", noticePath, "", "", 200)
	if turnOneDecode[notificationRoute](t, raw).Enabled {
		t.Fatal("disabled state did not survive restart")
	}
	raw, _ = c.call("GET", "/snapshots/"+first.ID, "", "", 200)
	snapshot := turnOneDecode[snapshot](t, raw)
	if snapshot.Board.ID != first.ID || len(snapshot.Activity) != 1 {
		t.Fatal("snapshot omitted identity or audit")
	}
	verifyPath := "/verifications/" + first.ID
	raw, _ = c.call("GET", verifyPath, "", "", 200)
	if !turnOneDecode[verification](t, raw).Valid {
		t.Fatal("valid durable snapshot failed verification")
	}
	if err := os.WriteFile(filepath.Join(root, "boards", first.ID+".json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ = c.call("GET", verifyPath, "", "", 200)
	if turnOneDecode[verification](t, raw).Valid {
		t.Fatal("damaged durable snapshot passed verification")
	}
	report, err := VerifyOffline(root, first.ID)
	if err != nil || report.Valid {
		t.Fatal("offline verifier disagreed with live check")
	}
	c.call("GET", firstPath, "", "", 200)
	c.call("GET", "/snapshots/"+first.ID, "", "", 503)
	raw, _ = c.call("GET", "/boards", "", "", 200)
	for _, item := range turnOnePage[boardSummary](t, raw, "boards").Values {
		if item.ID == first.ID && item.Attention != nil {
			t.Fatal("damaged board showed known counts")
		}
		if item.ID == second.ID && (item.Attention == nil || *item.Attention != (attention{})) {
			t.Fatal("healthy empty board did not show known zero counts")
		}
	}
	if err := os.WriteFile(filepath.Join(root, "localswim-workspace.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	c.call("GET", "/workspace", "", "", 503)
	c.call("PATCH", "/workspace", pins, pinsTag, 503)
	runningBefore, _ := c.call("GET", "/service", "", "", 200)
	if err := os.WriteFile(filepath.Join(root, "candidate.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, _ = c.call("GET", "/builds", "", "", 200)
	candidates := turnOnePage[build](t, raw, "builds")
	if len(candidates.Values) != 1 || candidates.Values[0].Validation != "invalid" {
		t.Fatal("damaged staged manifest was not reported")
	}
	c.call("GET", "/builds/"+candidates.Values[0].ID, "", "", 200)
	runningAfter, _ := c.call("GET", "/service", "", "", 200)
	if string(runningBefore) != string(runningAfter) {
		t.Fatal("invalid staging changed running service status")
	}
	c.call("GET", "/boards/"+second.ID, "", "", 200)
	t.Log("M01 two boards and restart, M27 pins and corrupt preferences, M28 durable and accepted verification, M32 toggle and restart, M38 combined service/build status")
}

func verifyTurnOneCustomWorkflow(t *testing.T, c turnOneClient, bot string) {
	t.Helper()
	laneID, completed, priorityID := newID(), newID(), newID()
	body := fmt.Sprintf(`{"name":"Named workflow","workflow":{
 "users":[{"id":"%s","label":"Human","kind":"HUMAN"},{"id":"%s","label":"Bot","kind":"BOT"}],
 "lanes":[{"id":"%s","label":"Inbox","key":"inbox"},{"id":"%s","label":"Done","key":"completed","role":"COMPLETED"}],
 "priorities":[{"id":"%s","key":"regular","label":"Regular","rank":0}],
 "policy":{"default_priority_id":"%s","create_grants":[{"user_id":"%s","lane_id":"%s"}],"move_grants":[{"user_id":"%s","from_lane_id":"%s","to_lane_id":"%s"}],
 "resource_grants":[{"user_id":"%s","capabilities":["read","manage_board","create_card"]},{"user_id":"%s","capabilities":["manage_notifications"]}]}}}`,
		strings.ToUpper(humanID), botID, strings.ToUpper(laneID), completed, priorityID, priorityID, humanID, laneID, humanID, laneID, completed, humanID, botID)
	bad := strings.Replace(body, `"default_priority_id":"`+priorityID, `"default_priority_id":"`+newID(), 1)
	c.call("POST", "/boards", bad, "", 400)
	badIdentity := strings.Replace(body, `"label":"Human"`, `"label":"Unrelated"`, 1)
	c.call("POST", "/boards", badIdentity, "", 409)
	for _, grant := range []string{"create_grants", "move_grants"} {
		input := turnOneDecode[map[string]any](t, []byte(body))
		workflow := input["workflow"].(map[string]any)
		policy := workflow["policy"].(map[string]any)
		policy[grant] = []any{}
		invalid, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		c.call("POST", "/boards", string(invalid), "", 400)
	}
	raw, _ := c.call("GET", "/boards", "", "", 200)
	if len(turnOnePage[boardSummary](t, raw, "boards").Values) != 2 {
		t.Fatal("invalid custom workflow was saved")
	}
	raw, _ = c.call("POST", "/boards", body, "", 201)
	created := turnOneDecode[board](t, raw)
	if len(created.LaneIDs) != 2 || created.LaneIDs[0] != laneID || len(created.PriorityIDs) != 1 || created.PriorityIDs[0] != priorityID {
		t.Fatal("custom workflow identities did not resolve")
	}
	raw, _ = c.call("GET", "/lanes?board_id="+created.ID, "", "", 200)
	lanes := turnOnePage[lane](t, raw, "lanes")
	if len(lanes.Values) != 2 || lanes.Values[0].Label != "Inbox" || lanes.Values[1].Role != "completed" {
		t.Fatal("saved lanes differ from custom workflow")
	}
	raw, _ = c.call("GET", "/policies/"+created.PolicyID, "", "", 200)
	if turnOneDecode[policy](t, raw).DefaultPriorityID != priorityID {
		t.Fatal("resolved policy did not survive admission")
	}
	path := "/boards/" + created.ID
	restricted := c
	restricted.token = bot
	routePath := "/notifications/" + created.ID
	c.call("GET", routePath, "", "", 403)
	restricted.call("GET", routePath, "", "", 404)
	restricted.call("PATCH", routePath, `{"enabled":true}`, etag("notifications-"+created.ID, 1), 404)
	c.call("PATCH", routePath, `{"enabled":true}`, etag("notifications-"+created.ID, 1), 403)
	raw, _ = c.call("PATCH", path, `{"name":"Renamed workspace project"}`, "W/"+created.entityTag()+", \"unrelated\", "+created.entityTag(), 200)
	changed := turnOneDecode[board](t, raw)
	if changed.ID != created.ID || changed.Name != "Renamed workspace project" || changed.entityTag() == created.entityTag() {
		t.Fatal("rename changed board address or failed to advance its revision")
	}
	raw, _ = c.call("GET", path, "", "", 200)
	if turnOneDecode[board](t, raw).Name != changed.Name {
		t.Fatal("same address did not expose rename")
	}
	t.Log("M16 custom workflow rejection, resolved identities and stable rename address")
}

func checkTurnOneBoundary(t *testing.T, c turnOneClient) {
	t.Helper()
	send := func(method, host, origin, preflight, token, condition string, want int) http.Header {
		t.Helper()
		req, err := http.NewRequest(method, c.url+Prefix+"/boards", nil)
		if err != nil {
			t.Fatal(err)
		}
		if host != "" {
			req.Host = host
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		if preflight != "" {
			req.Header.Set("Access-Control-Request-Method", preflight)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if condition != "" {
			req.Header.Set("If-None-Match", condition)
		}
		res, err := c.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("boundary: status %d, want %d", res.StatusCode, want)
		}
		return res.Header
	}
	send("GET", "foreign.invalid", "", "", c.token, "", 403)
	if h := send("GET", "", "null", "", c.token, "", 403); h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin received CORS grant")
	}
	if h := send("GET", "", "", "", "", "", 401); h.Get("WWW-Authenticate") != `Bearer realm="localswim"` {
		t.Fatal("missing bearer challenge")
	}
	if h := send("OPTIONS", "", c.url, "post", "", "", 204); h.Get("Access-Control-Max-Age") != "600" || h.Get("Access-Control-Allow-Origin") != c.url || !strings.Contains(h.Get("Access-Control-Allow-Headers"), "X-Expected-Board-ETag") {
		t.Fatal("incomplete accepted preflight")
	}
	send("OPTIONS", "", c.url, "OPTIONS", "", "", 400)
	if h := send("GET", "", "", "", c.token, "malformed", 400); h.Get("Cache-Control") != "no-store" || h.Get("ETag") != "" {
		t.Fatal("invalid precondition response must be uncached")
	}
}
