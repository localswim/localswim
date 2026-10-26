package admin

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestOriginalPreviewBindingsAndExpiry(t *testing.T) {
	p, e := os.ReadFile("../../../../fixtures/contracts/archive-recovery/preview-clear.json")
	if e != nil {
		t.Fatal(e)
	}
	c, e := os.ReadFile("../../../../fixtures/contracts/archive-recovery/confirm-clear.json")
	if e != nil {
		t.Fatal(e)
	}
	// Fixture decoding itself verifies the original preview inventory hash.
	// Derive synthetic identities from the validated fixture without inventing
	// production defaults; every mutated context below must be independently bound.
	var bindings struct{ BoardID, Incarnation, ActorID, PreviewedAt, ExpiresAt string }
	if e := json.Unmarshal(p, &bindings); e != nil {
		t.Fatal(e)
	}
	now, e := time.Parse("2006-01-02T15:04:05Z", bindings.PreviewedAt)
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(time.Second)
	if _, _, e := matchPreview(p, c, bindings.BoardID, bindings.Incarnation, bindings.ActorID, now); e != nil {
		t.Fatal("matching original preview refused")
	}
	for _, mutated := range [][]byte{
		bytes.Replace(c, []byte(`"expectedRevision": 5`), []byte(`"expectedRevision": 6`), 1),
		bytes.Replace(c, []byte(`2026-10-01T00:00:00Z`), []byte(`2026-10-02T00:00:00Z`), 1),
	} {
		if bytes.Equal(mutated, c) {
			t.Fatal("mutation fixture did not change")
		}
		if _, _, e := matchPreview(p, mutated, bindings.BoardID, bindings.Incarnation, bindings.ActorID, now); e == nil {
			t.Fatal("changed confirmation accepted")
		}
	}
	const other = "00000000-0000-4000-8000-000000000fff"
	for _, bind := range [][3]string{{other, bindings.Incarnation, bindings.ActorID}, {bindings.BoardID, other, bindings.ActorID}, {bindings.BoardID, bindings.Incarnation, other}} {
		if _, _, e := matchPreview(p, c, bind[0], bind[1], bind[2], now); e == nil {
			t.Fatal("wrong current authority accepted")
		}
	}
	expires, _ := time.Parse("2006-01-02T15:04:05Z", bindings.ExpiresAt)
	if _, _, e := matchPreview(p, c, bindings.BoardID, bindings.Incarnation, bindings.ActorID, expires); e == nil {
		t.Fatal("expired preview accepted")
	}
	if _, _, e := matchPreview(p, c, bindings.BoardID, bindings.Incarnation, bindings.ActorID, now.Add(-time.Minute)); e == nil {
		t.Fatal("future preview accepted")
	}
}
