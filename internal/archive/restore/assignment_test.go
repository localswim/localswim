package restore

import (
	"context"
	A "github.com/localswim/localswim/internal/contract/archive"
	"testing"
)

func TestAssignmentRestoreTransformPreservesCurrentAndHistory(t *testing.T) {
	members := fixture(t)
	b, err := A.DecodeReadable[A.Board](members["board.json"])
	if err != nil {
		t.Fatal(err)
	}
	h, err := A.DecodeReadable[A.History](members["history.json"])
	if err != nil {
		t.Fatal(err)
	}
	owner := b.Actors[0].ActorID
	b.Cards[0].OwnerID = &owner
	count := 0
	for j := range h.Records {
		if h.Records[j].State != nil {
			h.Records[j].State.OwnerID = &owner
			count++
		}
	}
	if count == 0 {
		t.Fatal("history fixture missing")
	}
	members["board.json"], err = A.Readable(b)
	if err != nil {
		t.Fatal(err)
	}
	members["history.json"], err = A.Readable(h)
	if err != nil {
		t.Fatal(err)
	}
	source := sourcePackage(t, members)
	p, err := prepare(context.Background(), source, testTime, testInc)
	if err != nil {
		t.Fatal(err)
	}
	if p.board.Cards[0].OwnerID == nil || *p.board.Cards[0].OwnerID != owner {
		t.Fatal("current assignee lost")
	}
	for _, s := range p.history.Records {
		if s.State != nil && (s.State.OwnerID == nil || *s.State.OwnerID != owner) {
			t.Fatal("history assignee lost")
		}
	}
}
