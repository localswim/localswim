package client

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestResolveTicketRelationshipRefusesAmbiguousOrInconsistentEdges(t *testing.T) {
	const boardID = "00000000-0000-4000-8000-000000000001"
	const cardID = "00000000-0000-4000-8000-000000000002"
	const relatedID = "00000000-0000-4000-8000-000000000003"
	const linkID = "00000000-0000-4000-8000-000000000004"
	const reverseID = "00000000-0000-4000-8000-000000000005"
	const boardTag = `"00000000-0000-4000-8000-000000000001-12"`
	const related = `{"id":"` + relatedID + `","ticket":2,"revision":10}`
	outgoing := fmt.Sprintf(`{"id":%q,"revision":10,"kind":"references","direction":"outgoing","card":%s}`, linkID, related)
	incoming := strings.ReplaceAll(strings.ReplaceAll(outgoing, linkID, reverseID), "outgoing", "incoming")
	for _, tc := range []struct {
		name, tag, edges, wantError string
	}{
		{"exact outgoing", boardTag, incoming + "," + outgoing, ""},
		{"ambiguous", boardTag, outgoing + "," + strings.ReplaceAll(outgoing, linkID, reverseID), "more than one relationship"},
		{"duplicate identity", boardTag, outgoing + "," + outgoing, "ambiguous or invalid"},
		{"only reverse", boardTag, incoming, "no relationship matches"},
		{"inconsistent endpoint", boardTag, strings.ReplaceAll(outgoing, `"revision":10}`, `"revision":9}`), "endpoint and direction"},
		{"inconsistent board", ticketEntityTag(boardID, 13), outgoing, "board validator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != apiPath+"/boards/"+boardID ||
					r.URL.RawQuery != "view=ticket&ticket=1&related_ticket=2" {
					t.Errorf("unexpected ticket request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("X-Board-ETag", tc.tag)
				_, _ = io.WriteString(w, fmt.Sprintf(`{"board_id":%q,"board_revision":12,"card":{"id":%q,"ticket":1,"revision":10},"related_card":%s,"relationships":[%s]}`,
					boardID, cardID, related, tc.edges))
			}))
			defer server.Close()
			api, err := New(server.URL, "test-token")
			if err != nil {
				t.Fatal(err)
			}
			target, err := api.ResolveTicketRelationship(boardID, 1, 2, "references")
			if tc.wantError == "" {
				if err != nil || target.LinkID != linkID || target.LinkRevision != 10 ||
					target.LinkETag != ticketEntityTag(linkID, 10) || target.RelatedCardID != relatedID {
					t.Fatalf("did not resolve the outgoing edge: %+v (%v)", target, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected %q refusal, got %v", tc.wantError, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("made %d requests; want one lookup", requests.Load())
			}
		})
	}
}
