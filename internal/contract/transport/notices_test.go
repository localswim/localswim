package transport

import (
	"bytes"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestNoticeResponseRejectsUnverifiedAndForeignReceipts(t *testing.T) {
	const board = "10000000-0000-4000-8000-000000000001"
	const inc = "10000000-0000-4000-8000-000000000002"
	target := UUIDv4("10000000-0000-4000-8000-000000000003")
	generation := UUIDv4("10000000-0000-4000-8000-000000000004")
	checkpoint, count := A.Number(7), A.Number(1)
	pair := NoticePair{TargetID: target, DeliveryID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AcceptedAt: "2026-11-06T12:00:00Z", Class: "human", LowerEvent: 6, UpperEvent: &checkpoint, EventCount: &count}
	message := NoticeResponse{APIVersion: 1, Scope: "notices", Result: NoticeView{
		Identity: A.Identity{BoardID: board, Incarnation: inc}, ControlGeneration: generation, TargetID: &target,
		State: "stopped", Checkpoint: &checkpoint, Entries: []NoticePair{pair},
		Lookup: &NoticeLookup{State: "accepted", Receipt: &pair},
	}}
	wire, err := Readable(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[NoticeResponse](wire); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string][]byte{
		"foreign":    bytes.Replace(wire, []byte(target), []byte(board), 1),
		"unverified": bytes.Replace(wire, []byte(`"accepted"`), []byte(`"unknown"`), 1),
		"oversized":  append(bytes.Repeat([]byte(" "), NoticeResponseLimit), wire...),
	} {
		t.Run(name, func(t *testing.T) {
			if bytes.Equal(input, wire) {
				t.Fatal("test did not modify fixture")
			}
			if _, err := Decode[NoticeResponse](input); err == nil {
				t.Fatal("accepted invalid notice response")
			}
		})
	}
}
