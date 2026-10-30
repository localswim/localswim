package boardapi

import (
	A "github.com/localswim/localswim/internal/contract/archive"
	"strings"
	"testing"
)

func TestArchiveListingQueryBoundary(t *testing.T) {
	for _, raw := range []string{"", "q=", "archive=active", "archive=archived", "archive=all&cursor=e30." + strings.Repeat("A", 43)} {
		if _, e := parseSearch(raw); e != nil {
			t.Fatal("valid listing query refused")
		}
	}
	for _, raw := range []string{"archive=", "archive=Active", "archive=all&archive=active", "q=&q=", "unknown=1", "q=%ff", "cursor=opaque", "cursor=", "cursor=" + strings.Repeat("x", 2049), "cursor=%0a", "q=%zz", "archive=all;bad"} {
		if _, e := parseSearch(raw); e == nil {
			t.Fatal("invalid listing query accepted")
		}
	}
}
func TestListingCursorBindsActorSnapshotFilterAndHandler(t *testing.T) {
	h := &Handler{}
	h.cursorKey[0] = 1
	h.cursorEpoch[0] = 2
	p := cursorPayload{Epoch: "epoch", Actor: testActor, Identity: A.Identity{BoardID: testBoard, Incarnation: testInc}, Revision: 1, Filter: filterDigest(searchRequest{archive: "active"}), Priority: "P3", Ticket: 1, CardID: testCard}
	token := h.encodeCursor(p)
	if _, e := h.decodeCursor(token, p); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*cursorPayload){func(p *cursorPayload) { p.Actor = testCard }, func(p *cursorPayload) { p.Identity.Incarnation = testCard }, func(p *cursorPayload) { p.Identity.BoardID = testCard }, func(p *cursorPayload) { p.Revision++ }, func(p *cursorPayload) { p.Filter = filterDigest(searchRequest{archive: "all"}) }, func(p *cursorPayload) { p.Epoch = "restart" }} {
		expected := p
		change(&expected)
		if _, e := h.decodeCursor(token, expected); e != errCursorStale {
			t.Fatal("mismatched cursor accepted")
		}
	}
	for _, bad := range []string{"opaque", token + "x", token + ".x", strings.Repeat("x", 2049)} {
		if _, e := h.decodeCursor(bad, p); e != errCursorStale {
			t.Fatal("invalid cursor accepted")
		}
	}
	h.cursorKey[0]++
	if _, e := h.decodeCursor(token, p); e != errCursorStale {
		t.Fatal("restarted secret accepted")
	}
}
