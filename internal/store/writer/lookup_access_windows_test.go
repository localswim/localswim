//go:build windows && amd64

package writer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestLookupCompletionMetadata(t *testing.T) {
	e, _ := fixture(t)
	i := intent(t, op, 0, "Synthetic")
	key := lookupIdentity(t, i)
	r := e.Lookup(testContext(t), key, authority)
	if !r.LookupCompleted || r.Unknown == nil || r.Terminal != nil || r.Refused != nil {
		t.Fatal("missing original observation")
	}
	original := key
	original.Incarnation = "20000000-0000-0000-0000-000000000099"
	r = e.Lookup(testContext(t), original, authority)
	if !r.LookupCompleted || r.Unknown == nil || r.Unknown.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" {
		t.Fatal("known original incarnation observation")
	}
	terminal(t, e.Submit(testContext(t), i, callbacks()), "committed")
	r = e.Lookup(testContext(t), key, authority)
	if !r.LookupCompleted || r.Terminal == nil {
		t.Fatal("terminal observation")
	}
	key.Fingerprint = strings.Repeat("a", 64)
	r = e.Lookup(testContext(t), key, authority)
	if !r.LookupCompleted || r.Refused == nil || r.Refused.Error.Code != "OPERATION_MISMATCH" {
		t.Fatal("mismatch observation")
	}
	raw(t, e, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "UPDATE grant_record SET enabled=0")
		return err
	})
	r = e.Lookup(testContext(t), lookupIdentity(t, i), authority)
	if !r.LookupCompleted || r.Refused == nil || r.Terminal != nil {
		t.Fatal("current authority denial")
	}
	b, err := json.Marshal(r)
	if err != nil || strings.Contains(string(b), "LookupCompleted") {
		t.Fatal("internal metadata escaped")
	}
}

func TestLookupFailuresRemainUnobserved(t *testing.T) {
	e, _ := fixture(t)
	key := lookupIdentity(t, intent(t, op, 0, "Synthetic"))
	e.fault = func(stage string) error {
		if stage == "readback" {
			return errors.New("synthetic readback failure")
		}
		return nil
	}
	r := e.Lookup(testContext(t), key, authority)
	if r.LookupCompleted || r.Unknown == nil {
		t.Fatal("failed readback claimed observation")
	}
	e.fault = nil
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = e.Lookup(ctx, key, authority)
	if r.LookupCompleted || r.Unknown == nil {
		t.Fatal("cancel claimed observation")
	}
	r = New(nil).Lookup(testContext(t), key, authority)
	if r.LookupCompleted || r.Unknown == nil {
		t.Fatal("unavailable store claimed observation")
	}
	r = e.Lookup(testContext(t), key, nil)
	if r.LookupCompleted || r.Unknown == nil {
		t.Fatal("missing authority claimed observation")
	}
	raw(t, e, func(c *sql.Conn) error {
		_, err := c.ExecContext(testContext(t), "ALTER TABLE board RENAME TO damaged_board")
		return err
	})
	r = e.Lookup(testContext(t), key, authority)
	if r.LookupCompleted || r.Unknown == nil || r.Terminal != nil {
		t.Fatal("failed identity query claimed observation")
	}
}
