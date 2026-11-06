package core

import "testing"

func TestAttachmentDetachUsesOrdinaryExactInput(t *testing.T) {
	q, code, _ := parse(args("attachment", "detach", testActor, "--input", "original.json"))
	if code != "" || q.command != "attachment.detach" || q.id != testActor || q.input != "original.json" || q.method != "POST" || q.path != "/api/v1/boards/"+testBoard+"/cards/"+testActor+"/attachment-detach" {
		t.Fatal("detach must retain selected target and immutable input route")
	}
	for _, command := range [][]string{
		{"attachment", "detach", testActor},
		{"attachment", "detach", "not-a-uuid", "--input", "original.json"},
		{"attachment", "detach", testActor, "--input", "original.json", "--input", "replacement.json"},
		{"attachment", "detach", testActor, "--input", "original.json", "--file", "bytes.bin"},
		{"attachment", "detach", testActor, "--input", "original.json", "--actor", testActor},
	} {
		if _, code, _ := parse(args(command...)); code == "" {
			t.Fatal("invalid detach invocation accepted")
		}
	}
}
