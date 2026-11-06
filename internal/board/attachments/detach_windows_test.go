//go:build windows && amd64

package attachments

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

func detachResult(t *testing.T, f *fixture, id string, rev uint64) (writer.Result, bool) {
	t.Helper()
	i := f.intent(t, "attachment.detach", f.card, rev, rev, map[string]any{"attachmentId": id})
	return f.a.Detach(tc(t), f.g, Route{board, inc, f.card}, i)
}

func TestDetachPreservesMetadataHistoryPinsAndExactReplay(t *testing.T) {
	f := fresh(t)
	data := []byte{0, 255, 13, 10, 1}
	add := f.upload(t, data, 1)
	r, _ := f.add(t, add, data)
	id := committed(t, r)
	before := f.value(t, "SELECT next_event FROM board")
	i := f.intent(t, "attachment.detach", f.card, 2, 2, map[string]any{"attachmentId": id})
	result, fresh := f.a.Detach(tc(t), f.g, Route{board, inc, f.card}, i)
	if committed(t, result) != id || !fresh || result.Terminal.CardRevision == nil || *result.Terminal.CardRevision != 3 || len(result.Terminal.ByteFacts) != 0 {
		t.Fatal("detach terminal")
	}
	checks := map[string]uint64{
		"SELECT revision FROM board":                                     3,
		"SELECT next_event FROM board":                                   before + 1,
		"SELECT count(*) FROM current_reference WHERE kind='attachment'": 0,
		"SELECT count(*) FROM current_attachment_pin":                    0,
		"SELECT count(*) FROM attachment":                                1,
		"SELECT count(*) FROM entity WHERE kind='attachment' AND removed_at IS NULL AND removal_reason IS NULL": 1,
		"SELECT count(*) FROM history_blob_pin WHERE card_revision=2":                                           1,
		"SELECT count(*) FROM operation_blob_pin":                                                               1,
		"SELECT count(*) FROM byte_fact":                                                                        1,
		"SELECT count(*) FROM activity WHERE type='attachment.detach'":                                          1,
	}
	for q, want := range checks {
		if got := f.value(t, q); got != want {
			t.Fatalf("%s = %d want %d", q, got, want)
		}
	}
	if _, err := f.a.Read(tc(t), f.g, board, inc, actor, id); !errors.Is(err, ErrNotFound) {
		t.Fatal("ordinary retained download", err)
	}
	item, err := f.a.Inspect(tc(t), f.g, board, inc, actor, f.card, id)
	if err != nil || item.Result.File.ReferenceState != "retained" || item.Result.File.Attribution.State != "known" {
		t.Fatal("retained metadata", err)
	}
	again, isFresh := f.a.Detach(tc(t), f.g, Route{board, inc, f.card}, i)
	if isFresh || committed(t, again) != id || f.value(t, "SELECT revision FROM board") != 3 {
		t.Fatal("replay")
	}
	exactAdd, isFresh := f.add(t, add, data)
	if isFresh || committed(t, exactAdd) != id {
		t.Fatal("prior add receipt")
	}
	unchanged, _ := detachResult(t, f, id, 3)
	if unchanged.Terminal == nil || unchanged.Terminal.Outcome != "not_committed" || unchanged.Terminal.Code != "NO_CHANGE" {
		t.Fatal("no-change")
	}
	stale, _ := detachResult(t, f, id, 2)
	if stale.Terminal == nil || stale.Terminal.Code != "REVISION_CONFLICT" {
		t.Fatal("stale priority")
	}
	f.raw(t, func(c *sql.Conn) error {
		if err := schema.Validate(tc(t), c, schema.Identity{BoardID: board, Incarnation: inc}); err != nil {
			return err
		}
		var got []byte
		if err := c.QueryRowContext(tc(t), "SELECT bytes FROM blob").Scan(&got); err != nil {
			return err
		}
		if !bytes.Equal(got, data) {
			t.Fatal("retained bytes")
		}
		return nil
	})
}

func TestDetachOnlySelectedReferenceAndRefusalPrecedence(t *testing.T) {
	f := fresh(t)
	data := []byte("same digest")
	r, _ := f.add(t, f.upload(t, data, 1), data)
	id := committed(t, r)
	r, _ = f.add(t, f.upload(t, data, 2), data)
	peer := committed(t, r)
	r, _ = detachResult(t, f, id, 3)
	committed(t, r)
	if f.value(t, "SELECT count(*) FROM current_attachment_pin") != 1 {
		t.Fatal("peer pin lost")
	}
	if _, err := f.a.Read(tc(t), f.g, board, inc, actor, peer); err != nil {
		t.Fatal("peer download", err)
	}
	absent, _ := detachResult(t, f, "00000000-0000-4000-8000-000000000099", 4)
	if absent.Terminal == nil || absent.Terminal.Code != "NOT_FOUND" {
		t.Fatal("absent")
	}
	f.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(tc(t), "DELETE FROM grant_right WHERE right_name='attach'")
		return err
	})
	denied, _ := detachResult(t, f, id, 4)
	if denied.Terminal == nil || denied.Terminal.Code != "FORBIDDEN" {
		t.Fatal("authority before no-change", denied)
	}
	f.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(tc(t), "INSERT INTO grant_right VALUES(?,?,'attach')", inc, grant)
		return err
	})
	f.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(tc(t), "UPDATE card SET lane='Completed' WHERE card_id=?", f.card)
		return err
	})
	completed, _ := detachResult(t, f, id, 4)
	if completed.Terminal == nil || completed.Terminal.Code != "COMPLETED" {
		t.Fatal("completed before no-change")
	}
	if f.value(t, "SELECT revision FROM board") != 4 || f.value(t, "SELECT count(*) FROM activity WHERE type='attachment.detach'") != 1 {
		t.Fatal("refusal changed state")
	}
}

func TestDetachArchivedCard(t *testing.T) {
	f := fresh(t)
	r, _ := f.add(t, f.upload(t, nil, 1), nil)
	id := committed(t, r)
	f.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(tc(t), "UPDATE card SET archived=1 WHERE card_id=?", f.card)
		return err
	})
	r, _ = detachResult(t, f, id, 2)
	committed(t, r)
}

func TestDetachRacingSameBaseAndRollback(t *testing.T) {
	f := fresh(t)
	r, _ := f.add(t, f.upload(t, []byte("one"), 1), []byte("one"))
	id := committed(t, r)
	first := f.intent(t, "attachment.detach", f.card, 2, 2, map[string]any{"attachmentId": id})
	second := f.intent(t, "attachment.detach", f.card, 2, 2, map[string]any{"attachmentId": id})
	out := make(chan writer.Result, 2)
	for _, i := range []contract.Intent{first, second} {
		go func(i contract.Intent) { r, _ := f.a.Detach(tc(t), f.g, Route{board, inc, f.card}, i); out <- r }(i)
	}
	commits, conflicts := 0, 0
	for n := 0; n < 2; n++ {
		r := <-out
		if r.Terminal == nil {
			t.Fatal("race unavailable")
		}
		if r.Terminal.Outcome == "committed" {
			commits++
		}
		if r.Terminal.Code == "REVISION_CONFLICT" {
			conflicts++
		}
	}
	if commits != 1 || conflicts != 1 || f.value(t, "SELECT revision FROM board") != 3 {
		t.Fatal("race not single commit")
	}

	g := fresh(t)
	r, _ = g.add(t, g.upload(t, nil, 1), nil)
	id = committed(t, r)
	g.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(tc(t), "CREATE TRIGGER synthetic_detach_abort BEFORE INSERT ON activity WHEN NEW.type='attachment.detach' BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END")
		return err
	})
	r, _ = detachResult(t, g, id, 2)
	if r.Terminal != nil && r.Terminal.Outcome == "committed" {
		t.Fatal("failed transaction committed")
	}
	for q, want := range map[string]uint64{"SELECT revision FROM board": 2, "SELECT count(*) FROM current_attachment_pin": 1, "SELECT count(*) FROM current_reference WHERE kind='attachment'": 1, "SELECT count(*) FROM card_revision WHERE card_revision=2": 0, "SELECT count(*) FROM operation WHERE command='attachment.detach'": 0} {
		if g.value(t, q) != want {
			t.Fatal("partial detach", q)
		}
	}
}
func TestDetachInvalidatesApprovalAndPreservesPriorReference(t *testing.T) {
	f := fresh(t)
	first, _ := f.add(t, f.upload(t, nil, 1), nil)
	id := committed(t, first)
	const approval = "80000000-0000-0000-0000-000000000001"
	f.raw(t, func(c *sql.Conn) error {
		for _, q := range []struct {
			q string
			a []any
		}{
			{"INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", []any{inc, approval, f.card, board, inc}},
			{"INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,1,1,1,'2026-10-26T00:00:00Z')", []any{inc, approval, f.card, actor}},
			{"UPDATE card SET current_approval_id=? WHERE card_id=?", []any{approval, f.card}},
		} {
			if _, err := c.ExecContext(tc(t), q.q, q.a...); err != nil {
				return err
			}
		}
		return nil
	})
	r, _ := detachResult(t, f, id, 2)
	committed(t, r)
	if f.value(t, "SELECT count(*) FROM card WHERE current_approval_id IS NULL") != 1 || f.value(t, "SELECT count(*) FROM approval") != 1 || f.value(t, "SELECT count(*) FROM revision_reference WHERE card_revision=2 AND kind='approval'") != 1 {
		t.Fatal("approval history lost")
	}
}
