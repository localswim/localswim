package v2

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCommentOptionalBoardGuard(t *testing.T) {
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	defer server.Close()
	caller := turnOneClient{t, server.URL, human, server.Client()}
	raw, _ := caller.call("POST", "/boards", `{"name":"Comment board guard"}`, "", 201)
	board := turnOneDecode[board](t, raw)
	raw, headers := caller.call("POST", "/cards", fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"expected_board_etag":%q,"title":"First"}`, board.ID, board.LaneIDs[0], board.entityTag()), "", 201)
	first := turnOneDecode[card](t, raw)
	stale := headers.Get("X-Board-ETag")
	_, headers = caller.call("POST", "/cards", fmt.Sprintf(`{"board_id":%q,"lane_id":%q,"expected_board_etag":%q,"title":"Second"}`, board.ID, board.LaneIDs[0], stale), "", 201)
	current := headers.Get("X-Board-ETag")
	body := fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":"Guarded"}`, first.ID, first.entityTag())
	for _, tc := range []struct {
		name   string
		tags   []string
		status int
	}{
		{"stale", []string{stale}, 412},
		{"malformed", []string{"invalid"}, 400},
		{"duplicate", []string{current, current}, 400},
		{"weak", []string{"W/" + current}, 400},
		{"current", []string{current}, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, server.URL+Prefix+"/comments", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+human)
			request.Header.Set("Content-Type", "application/json")
			for _, tag := range tc.tags {
				request.Header.Add("X-Expected-Board-ETag", tag)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			data, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status %d, want %d: %s", response.StatusCode, tc.status, data)
			}
			state, err := app.store.durable(board.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.status == 201 {
				want = 1
			}
			if len(state.Comments) != want {
				t.Fatalf("stored comments %d, want %d", len(state.Comments), want)
			}
		})
	}
	raw, _ = caller.call("GET", "/cards/"+first.ID, "", "", 200)
	updated := turnOneDecode[card](t, raw)
	caller.call("POST", "/comments", fmt.Sprintf(`{"card_id":%q,"expected_card_etag":%q,"text":"Header remains optional"}`, updated.ID, updated.entityTag()), "", 201)
}
