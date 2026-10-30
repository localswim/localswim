package transport

import (
	"encoding/json"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestRelationshipCurrentReadKindsAndExactScope(t *testing.T) {
	const linkID A.UUID = "00000000-0000-4000-8000-000000000090"
	const peerID A.UUID = "00000000-0000-4000-8000-000000000091"
	for _, kind := range []A.LinkKind{"blocking", "informational", "related", "reference", "duplicate"} {
		board, err := Decode[BoardRead](fixture(t, "board-read.json"))
		if err != nil {
			t.Fatal(err)
		}
		peer := board.Result.Cards[0]
		peer.CardID, peer.Ticket = peerID, 2
		peer.CommentIDs, peer.LinkIDs, peer.AttachmentIDs = []A.UUID{}, []A.UUID{}, []A.UUID{}
		peer.CurrentApprovalID = nil
		board.Result.Cards = append(board.Result.Cards, peer)
		board.Result.Cards[0].LinkIDs = []A.UUID{linkID}
		board.Result.Links = []A.Link{{LinkID: linkID, CardID: board.Result.Cards[0].CardID, Kind: kind, Target: A.CardRef{BoardID: board.Result.Identity.BoardID, Incarnation: board.Result.Identity.Incarnation, CardID: peerID}, Active: true, CreatedAt: "2026-10-01T00:00:00Z", CreatedIn: board.Result.Identity}}
		raw, err := Readable(board)
		if err != nil {
			t.Fatal(kind, err)
		}
		if _, err := Decode[BoardRead](raw); err != nil {
			t.Fatal("current relationship", kind, err)
		}
		board.Result.Links[0].Target.Incarnation = "00000000-0000-4000-8000-000000000092"
		raw, _ = json.Marshal(board)
		_, err = Decode[BoardRead](raw)
		if (err == nil) != !A.IsModernLinkKind(kind) {
			t.Fatal("historical identity admitted as current modern target", kind, err)
		}
		board.Result.Links[0].Kind = "future"
		raw, _ = json.Marshal(board)
		if _, err := Decode[BoardRead](raw); err == nil {
			t.Fatal("unsupported kind accepted")
		}
	}
}
