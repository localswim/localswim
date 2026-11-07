package transport

import (
	"bytes"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestNoticeResponseRejectsUnverifiedAndInvalidReceipts(t *testing.T) {
	const board = "10000000-0000-4000-8000-000000000001"
	const inc = "10000000-0000-4000-8000-000000000002"
	target := UUIDv4("10000000-0000-4000-8000-000000000003")
	generation := UUIDv4("10000000-0000-4000-8000-000000000004")
	checkpoint, count := A.Number(7), A.Number(1)
	pair := NoticePair{TargetID: target, DeliveryID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AcceptedAt: "2026-11-06T12:00:00Z", Class: "human", LowerEvent: 6, UpperEvent: &checkpoint, EventCount: &count}
	message := NoticeResponse{APIVersion: 1, Scope: "notices", Result: NoticeView{
		Identity: A.Identity{BoardID: board, Incarnation: inc}, ConfigurationRevision: 1, ControlGeneration: generation, TargetID: &target,
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
		"invalid-target": bytes.ReplaceAll(wire, []byte(target), []byte("10000000-0000-4000-7000-000000000003")),
		"unverified":     bytes.Replace(wire, []byte(`"accepted"`), []byte(`"unknown"`), 1),
		"oversized":      append(bytes.Repeat([]byte(" "), NoticeResponseLimit), wire...),
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

func TestNoticeResponseAllowsOriginalReceiptAfterRetarget(t *testing.T) {
	target := UUIDv4("10000000-0000-4000-8000-000000000003")
	oldTarget := UUIDv4("10000000-0000-4000-8000-000000000005")
	upper, count := A.Number(7), A.Number(1)
	pair := NoticePair{TargetID: oldTarget, DeliveryID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AcceptedAt: "2026-11-06T12:00:00Z", Class: "human", LowerEvent: 6, UpperEvent: &upper, EventCount: &count}
	response := NoticeResponse{APIVersion: 1, Scope: "notices", Result: NoticeView{
		Identity:              A.Identity{BoardID: "10000000-0000-4000-8000-000000000001", Incarnation: "10000000-0000-4000-8000-000000000002"},
		ConfigurationRevision: 1, ControlGeneration: "10000000-0000-4000-8000-000000000004", TargetID: &target, State: "stopped", Checkpoint: &upper,
		Entries: []NoticePair{pair}, Lookup: &NoticeLookup{State: "accepted", Receipt: &pair},
	}}
	wire, err := Readable(response)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode[NoticeResponse](wire); err != nil {
		t.Fatal(err)
	}
}
