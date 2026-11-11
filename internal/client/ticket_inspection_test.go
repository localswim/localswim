package client

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveTicketReadsOnceAndChecksValidators(t *testing.T) {
	const boardID = "00000000-0000-4000-8000-000000000001"
	const cardID = "00000000-0000-4000-8000-000000000002"
	const boardTag = `"valid-board-etag"`
	const cardTag = `"00000000-0000-4000-8000-000000000002-4"`
	valid := fmt.Sprintf(`{"board_id":%q,"board_revision":8,"card":{"id":%q,"revision":4,"ticket":2}}`, boardID, cardID)
	for _, test := range []struct {
		name string
		tag  string
		data string
		ok   bool
	}{
		{"valid", boardTag, valid, true},
		{"different board", boardTag, strings.Replace(valid, boardID, cardID, 1), false},
		{"different ticket", boardTag, strings.Replace(valid, `"ticket":2`, `"ticket":3`, 1), false},
		{"missing card revision", boardTag, strings.Replace(valid, `"revision":4,`, "", 1), false},
		{"duplicate card revision", boardTag, strings.Replace(valid, `"revision":4`, `"revision":7,"revision":4`, 1), false},
		{"missing board validator", "", valid, false},
		{"zero card revision", boardTag, strings.Replace(valid, `"revision":4`, `"revision":0`, 1), false},
		{"card revision beyond board", boardTag, strings.Replace(valid, `"revision":4`, `"revision":9`, 1), false},
		{"unexpected prose", boardTag, strings.TrimSuffix(valid, "}") + `,"prose":null}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != "GET" || r.URL.Path != apiPath+"/boards/"+boardID ||
					r.URL.RawQuery != "view=ticket&ticket=2" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Errorf("unexpected ticket request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("X-Board-ETag", test.tag)
				io.WriteString(w, test.data)
			}))
			defer server.Close()
			api, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			target, err := api.ResolveTicket(boardID, 2)
			if test.ok {
				want := TicketTarget{BoardID: boardID, BoardRevision: 8, BoardETag: boardTag,
					CardID: cardID, CardRevision: 4, CardETag: cardTag}
				if err != nil || target != want {
					t.Fatalf("target=%+v error=%v", target, err)
				}
			} else if err == nil {
				t.Fatal("invalid target accepted")
			}
			if requests != 1 {
				t.Fatalf("requests=%d, expected one read", requests)
			}
		})
	}
}
