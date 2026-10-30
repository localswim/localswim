//go:build windows && amd64

package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/writer"
)

func exactRelationship(kind, target string) map[string]any {
	return map[string]any{"kind": kind, "target": map[string]string{"boardId": board, "incarnation": inc, "cardId": target}}
}

// Compare complete domain rows, including counters, allocation, provenance,
// prior snapshots and references. Durable refusal ledger growth is intentional.
func relationshipState(t *testing.T, db *S.Store) string {
	t.Helper()
	l, err := db.Reader(testContext(t))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var state []any
	err = l.Do(testContext(t), func(c *sql.Conn) error {
		for _, table := range []string{"board", "card", "ticket", "entity", "link", "card_parent", "current_reference", "card_revision", "revision_reference", "activity", "approval"} {
			rows, err := c.QueryContext(testContext(t), "SELECT * FROM "+table+" ORDER BY rowid")
			if err != nil {
				return err
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				return err
			}
			var data []any
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for n := range values {
					pointers[n] = &values[n]
				}
				if err := rows.Scan(pointers...); err != nil {
					rows.Close()
					return err
				}
				data = append(data, values)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			if err := rows.Close(); err != nil {
				return err
			}
			state = append(state, table, data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func cardOnlyRelationshipResult(t *testing.T, r writer.Result) string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(*r.Terminal.Result, &m); err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["cardId"] == "" || len(r.Terminal.ResultRefs) != 1 || r.Terminal.ResultRefs[0].Kind != "card" || string(r.Terminal.ResultRefs[0].ID) != m["cardId"] {
		t.Fatal("relationship card mutation widened result or references")
	}
	return m["cardId"]
}

func TestRelationshipsAtomicCreateOwnedReplacementAndOriginals(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	parent := create(t, e, g, 0)
	peer := create(t, e, g, 1)
	input := map[string]any{"title": "Atomic child", "description": "", "parent": map[string]string{"boardId": board, "incarnation": inc, "cardId": parent}, "relationships": []any{exactRelationship("blocking", parent), exactRelationship("reference", peer), exactRelationship("informational", peer)}}
	original := intent(t, "card.create", "", 2, 0, input)
	r := submit(t, e, g, original, "", "committed")
	child := cardOnlyRelationshipResult(t, r)
	if value(t, db, "SELECT count(*) FROM link WHERE card_id='"+child+"' AND active=1") != 3 || value(t, db, "SELECT count(*) FROM current_reference WHERE card_id='"+child+"' AND kind='link'") != 3 || value(t, db, "SELECT count(*) FROM card_parent WHERE child_id='"+child+"' AND parent_id='"+parent+"'") != 1 || value(t, db, "SELECT revision FROM card WHERE card_id='"+peer+"'") != 1 {
		t.Fatal("atomic create omitted facts or edited peer")
	}
	old := string(*r.Terminal.Result)
	oldRefs, _ := json.Marshal(r.Terminal.ResultRefs)
	// Incoming facts remain owned by their actual source, through clear and edit.
	incoming := issue(t, e, db, g, "link.add", peer, exactRelationship("related", child), "committed")
	incomingID := resultID(t, incoming, "linkId")
	before := relationshipState(t, db)
	reorder := []any{exactRelationship("informational", peer), exactRelationship("blocking", parent), exactRelationship("reference", peer)}
	code(t, issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": reorder}, "not_committed"), "NO_CHANGE")
	if before != relationshipState(t, db) {
		t.Fatal("equal reorder changed domain state")
	}
	// Trying to take ownership of the incoming symmetric fact is refused.
	code(t, issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": []any{exactRelationship("related", peer)}}, "not_committed"), "LINK_ALREADY_EXISTS")
	if before != relationshipState(t, db) {
		t.Fatal("incoming ownership refusal changed domain")
	}
	newSet := []any{exactRelationship("reference", peer), exactRelationship("duplicate", parent)}
	r = issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": newSet}, "committed")
	cardOnlyRelationshipResult(t, r)
	if value(t, db, "SELECT count(*) FROM link WHERE card_id='"+child+"' AND active=1") != 2 || value(t, db, "SELECT count(*) FROM link WHERE card_id='"+child+"' AND active=0") != 2 || value(t, db, "SELECT count(*) FROM revision_reference WHERE card_id='"+child+"' AND card_revision=1 AND kind='link'") != 3 || value(t, db, "SELECT count(*) FROM link WHERE link_id='"+incomingID+"' AND active=1 AND card_id='"+peer+"'") != 1 {
		t.Fatal("replacement lost matching/history/incoming facts")
	}
	issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": []any{}}, "committed")
	issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": []any{exactRelationship("blocking", parent)}}, "committed")
	if value(t, db, "SELECT count(*) FROM link WHERE card_id='"+child+"' AND kind='blocking'") != 2 || value(t, db, "SELECT count(*) FROM current_reference WHERE card_id='"+child+"' AND kind='link'") != 1 {
		t.Fatal("inactive link identity resurrected or current refs incorrect")
	}
	before = relationshipState(t, db)
	replay := submit(t, e, g, original, "", "committed")
	replayedRefs, _ := json.Marshal(replay.Terminal.ResultRefs)
	if old != string(*replay.Terminal.Result) || string(oldRefs) != string(replayedRefs) || before != relationshipState(t, db) {
		t.Fatal("old original changed or replay mutated current state")
	}
	// An unfinished archived source remains editable; Completed admits no no-op.
	issue(t, e, db, g, "card.edit", child, map[string]any{"archived": true}, "committed")
	issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": []any{}}, "committed")
	execSQL(t, db, "UPDATE card SET lane='Completed' WHERE card_id=?", child)
	before = relationshipState(t, db)
	code(t, issue(t, e, db, g, "card.edit", child, map[string]any{"relationships": []any{}}, "not_committed"), "COMPLETED")
	if before != relationshipState(t, db) {
		t.Fatal("Completed equal clear changed state")
	}
}

func TestRelationshipsAtomicRefusalAndRollback(t *testing.T) {
	for _, mode := range []string{"foreign", "incarnation", "missing", "self", "duplicate-set", "parent", "rollback"} {
		t.Run(mode, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a := create(t, e, g, 0)
			b := create(t, e, g, 1)
			spec := exactRelationship("reference", b)
			input := map[string]any{"relationships": []any{exactRelationship("blocking", b), spec}}
			cmd, source, br, cr := "card.edit", a, uint64(2), uint64(1)
			switch mode {
			case "foreign":
				spec["target"].(map[string]string)["boardId"] = op
			case "incarnation":
				spec["target"].(map[string]string)["incarnation"] = op
			case "missing":
				spec["target"].(map[string]string)["cardId"] = op
			case "self":
				spec["target"].(map[string]string)["cardId"] = a
			case "duplicate-set":
				input["relationships"] = []any{spec, spec}
			case "parent":
				cmd, source, cr = "card.create", "", 0
				input["title"], input["description"] = "Refused create", ""
				input["parent"] = map[string]string{"boardId": board, "incarnation": inc, "cardId": op}
			}
			if mode == "duplicate-set" {
				before := relationshipState(t, db)
				wire, _ := json.Marshal(map[string]any{"opId": op, "incarnation": inc, "target": map[string]string{"cardId": a}, "expectedRevision": br, "expectedCardRevision": cr, "input": input})
				if _, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "card.edit", BoardID: board, TargetID: a}, contract.AuthenticatedActor{ActorID: actor}); err == nil {
					t.Fatal("duplicate semantic set admitted")
				}
				if before != relationshipState(t, db) {
					t.Fatal("duplicate set changed state")
				}
				return
			}
			i := intent(t, cmd, source, br, cr, input)
			cb := cards.New(g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: source})
			if mode == "rollback" {
				apply := cb.Apply
				cb.Apply = func(ctx context.Context, w writer.Write, i contract.Intent, at A.Timestamp) (writer.Effect, error) {
					if _, err := apply(ctx, w, i, at); err != nil {
						return writer.Effect{}, err
					}
					return writer.Effect{}, errors.New("controlled post-effect interruption")
				}
			}
			before := relationshipState(t, db)
			r := e.Submit(testContext(t), i, cb)
			if mode == "rollback" {
				if r.Unknown == nil {
					t.Fatal("interruption incorrectly established noncommit")
				}
			} else {
				want := "INVALID_INPUT"
				if mode == "missing" {
					want = "NOT_FOUND"
				}
				if mode == "self" {
					want = "SELF_LINK"
				}
				if r.Terminal == nil || r.Terminal.Outcome != "not_committed" {
					t.Fatal("invalid batch was not durably refused")
				}
				code(t, r, want)
			}
			if before != relationshipState(t, db) {
				t.Fatal("refusal/interruption leaked domain state or allocation")
			}
		})
	}
}

func TestRelationshipsKindsGraphsAndStaleWindows(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a, b, c := create(t, e, g, 0), create(t, e, g, 1), create(t, e, g, 2)
	issue(t, e, db, g, "link.add", a, exactRelationship("blocking", b), "committed")
	issue(t, e, db, g, "link.add", b, exactRelationship("blocking", c), "committed")
	code(t, issue(t, e, db, g, "card.edit", c, map[string]any{"relationships": []any{exactRelationship("reference", b), exactRelationship("blocking", a)}}, "not_committed"), "BLOCKING_CYCLE")
	issue(t, e, db, g, "link.add", a, exactRelationship("duplicate", b), "committed")
	issue(t, e, db, g, "link.add", b, exactRelationship("duplicate", c), "committed")
	code(t, issue(t, e, db, g, "link.add", c, exactRelationship("duplicate", a), "not_committed"), "DUPLICATE_CYCLE")
	code(t, issue(t, e, db, g, "link.add", a, exactRelationship("duplicate", c), "not_committed"), "INVALID_INPUT")
	for _, k := range []string{"related", "reference", "informational"} {
		issue(t, e, db, g, "link.add", c, exactRelationship(k, a), "committed")
	}
	code(t, issue(t, e, db, g, "link.add", a, exactRelationship("related", c), "not_committed"), "LINK_ALREADY_EXISTS")
	issue(t, e, db, g, "link.add", a, exactRelationship("reference", c), "committed")
	issue(t, e, db, g, "link.add", a, exactRelationship("informational", c), "committed")
	// Two independently prepared windows share one writer snapshot revision.
	br := uint64(value(t, db, "SELECT revision FROM board"))
	cr := uint64(value(t, db, "SELECT revision FROM card WHERE card_id='"+c+"'"))
	one := intent(t, "card.edit", c, br, cr, map[string]any{"relationships": []any{exactRelationship("reference", b)}})
	two := intent(t, "card.edit", c, br, cr, map[string]any{"relationships": []any{exactRelationship("informational", b)}})
	var results [2]writer.Result
	var wg sync.WaitGroup
	for n, i := range []contract.Intent{one, two} {
		wg.Add(1)
		go func(n int, i contract.Intent) {
			defer wg.Done()
			results[n] = e.Submit(testContext(t), i, cards.New(g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: c}))
		}(n, i)
	}
	wg.Wait()
	committed, conflicts := 0, 0
	for _, r := range results {
		if r.Terminal == nil {
			t.Fatal("stale writer lacked terminal")
		}
		if err := T.Validate(*r.Terminal); err != nil {
			t.Fatal(err)
		}
		if r.Terminal.Outcome == "committed" {
			committed++
		} else if r.Terminal.Code == "REVISION_CONFLICT" {
			conflicts++
		}
	}
	if committed != 1 || conflicts != 1 || value(t, db, "SELECT revision FROM board") != int64(br+1) {
		t.Fatal("stale windows jointly changed state")
	}
}

func TestRelationshipsHundredBoundAndLegacyConservation(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	// Controlled synthetic 101-peer setup, including a Completed archived peer.
	var specs []any
	raw(t, db, func(c *sql.Conn) error {
		if _, err := c.ExecContext(testContext(t), "UPDATE board SET next_ticket=103"); err != nil {
			return err
		}
		for n := 1; n <= 101; n++ {
			id := fmt.Sprintf("70000000-0000-4000-8000-%012d", n)
			// Current target authority comes from Board.Identity even when the
			// card's conserved creation provenance names a former incarnation.
			if _, err := c.ExecContext(testContext(t), "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'card',?,?,?,?)", inc, id, id, op, op2); err != nil {
				return err
			}
			if _, err := c.ExecContext(testContext(t), "INSERT INTO ticket(incarnation,ticket,card_id) VALUES(?,?,?)", inc, n+1, id); err != nil {
				return err
			}
			if _, err := c.ExecContext(testContext(t), "INSERT INTO card(incarnation,card_id,revision,title,description,priority,lane,archived) VALUES(?,?,1,'Controlled peer','','P3','Completed',1)", inc, id); err != nil {
				return err
			}
			specs = append(specs, exactRelationship("reference", id))
		}
		return nil
	})
	before := relationshipState(t, db)
	// 101 refuses structurally before an operation identity can be frozen.
	wire, _ := json.Marshal(map[string]any{"opId": op, "incarnation": inc, "target": map[string]string{"cardId": a}, "expectedRevision": 1, "expectedCardRevision": 1, "input": map[string]any{"relationships": specs}})
	if _, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "card.edit", BoardID: board, TargetID: a}, contract.AuthenticatedActor{ActorID: actor}); err == nil {
		t.Fatal("101 entries admitted")
	}
	if before != relationshipState(t, db) {
		t.Fatal("structural bound changed state")
	}
	issue(t, e, db, g, "card.edit", a, map[string]any{"relationships": specs[:100]}, "committed")
	if value(t, db, "SELECT count(*) FROM link WHERE active=1") != 100 {
		t.Fatal("100-entry legal boundary lost facts")
	}
	// A historical external target is retained exactly, while a new external
	// fact remains inadmissible. Controlled SQL represents an accepted old store.
	foreignID := "80000000-0000-4000-8000-000000000001"
	execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", inc, foreignID, a, board, inc)
	execSQL(t, db, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,active,created_at) VALUES(?,?,?,'informational',?,?,?,1,'2026-10-26T00:00:00Z')", inc, foreignID, a, op, op2, actor)
	execSQL(t, db, "INSERT INTO current_reference VALUES(?,?,'link',?)", inc, a, foreignID)
	// Existing >100 sets can always be cleared without truncation or resurrection.
	issue(t, e, db, g, "card.edit", a, map[string]any{"relationships": []any{}}, "committed")
	if value(t, db, "SELECT count(*) FROM link WHERE active=1") != 0 || value(t, db, "SELECT count(*) FROM link") != 101 || value(t, db, "SELECT count(*) FROM revision_reference WHERE kind='link' AND entity_id='"+foreignID+"'") != 1 {
		t.Fatal("large set clear lost historical evidence")
	}
}

func TestRelationshipsConserveLegacyForeignAndCycleWithoutReadmission(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a, b, c := create(t, e, g, 0), create(t, e, g, 1), create(t, e, g, 2)
	issue(t, e, db, g, "link.add", a, exactRelationship("blocking", b), "committed")
	// Controlled accepted historical rows, including a cycle old storage permits.
	for n, f := range []struct{ owner, kind, target, targetBoard, targetInc string }{
		{b, "blocking", a, board, inc},
		{a, "informational", actor, op, op2},
	} {
		id := fmt.Sprintf("80000000-0000-4000-8000-%012d", n+1)
		execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", inc, id, f.owner, board, inc)
		var internal any
		if f.targetBoard == board {
			internal = f.target
		}
		execSQL(t, db, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,internal_target_id,active,created_at) VALUES(?,?,?,?,?,?,?,?,1,'2026-10-26T00:00:00Z')", inc, id, f.owner, f.kind, f.targetBoard, f.targetInc, f.target, internal)
		execSQL(t, db, "INSERT INTO current_reference VALUES(?,?,'link',?)", inc, f.owner, id)
	}
	foreign := map[string]any{"kind": "informational", "target": map[string]string{"boardId": op, "incarnation": op2, "cardId": actor}}
	set := []any{exactRelationship("blocking", b), foreign, exactRelationship("reference", c)}
	issue(t, e, db, g, "card.edit", a, map[string]any{"relationships": set}, "committed")
	if value(t, db, "SELECT count(*) FROM link WHERE link_id='80000000-0000-4000-8000-000000000002' AND target_board='"+op+"' AND target_incarnation='"+op2+"' AND active=1") != 1 {
		t.Fatal("retained foreign member rewritten")
	}
	before := relationshipState(t, db)
	code(t, issue(t, e, db, g, "card.edit", a, map[string]any{"relationships": []any{foreign, exactRelationship("reference", c), exactRelationship("blocking", b)}}, "not_committed"), "NO_CHANGE")
	if before != relationshipState(t, db) {
		t.Fatal("legacy equal set changed")
	}
	code(t, issue(t, e, db, g, "link.add", a, foreign, "not_committed"), "INVALID_INPUT")
	issue(t, e, db, g, "link.add", c, exactRelationship("blocking", a), "committed") // traversal terminates on legacy a<->b
	code(t, issue(t, e, db, g, "link.add", a, exactRelationship("blocking", c), "not_committed"), "BLOCKING_CYCLE")
	// Removing the old owned edge repairs the cycle, without touching b's fact.
	issue(t, e, db, g, "card.edit", a, map[string]any{"relationships": []any{foreign}}, "committed")
	if value(t, db, "SELECT count(*) FROM link WHERE card_id='"+b+"' AND active=1") != 1 {
		t.Fatal("replacement altered incoming owner")
	}
}

func TestRelationshipsInvalidCreateEveryPositionAllocatesNothing(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	peer := create(t, e, g, 0)
	for position := 0; position < 3; position++ {
		members := []any{exactRelationship("blocking", peer), exactRelationship("reference", peer), exactRelationship("informational", peer)}
		members[position].(map[string]any)["target"].(map[string]string)["incarnation"] = op
		before := relationshipState(t, db)
		r := submit(t, e, g, intent(t, "card.create", "", 1, 0, map[string]any{"title": "Atomic refusal", "description": "", "parent": map[string]string{"boardId": board, "incarnation": inc, "cardId": peer}, "relationships": members}), "", "not_committed")
		code(t, r, "INVALID_INPUT")
		if before != relationshipState(t, db) {
			t.Fatalf("invalid create member %d allocated or mutated state", position)
		}
	}
}

func TestRelationshipsConcurrentOpposingFactsReadFreshSnapshot(t *testing.T) {
	for _, kind := range []string{"blocking", "duplicate", "related"} {
		t.Run(kind, func(t *testing.T) {
			e, db := fixture(t)
			g := service(t, db)
			a, b := create(t, e, g, 0), create(t, e, g, 1)
			ids, peers := []string{a, b}, []string{b, a}
			intents := []contract.Intent{intent(t, "link.add", a, 2, 1, exactRelationship(kind, b)), intent(t, "link.add", b, 2, 1, exactRelationship(kind, a))}
			var results [2]writer.Result
			var wg sync.WaitGroup
			for n := range intents {
				wg.Add(1)
				go func(n int) {
					defer wg.Done()
					results[n] = e.Submit(testContext(t), intents[n], New(g).Callbacks(Route{BoardID: board, Incarnation: inc, CardID: ids[n]}))
				}(n)
			}
			wg.Wait()
			loser, committed := -1, 0
			for n, r := range results {
				if r.Terminal == nil {
					t.Fatal("concurrent fact lacked terminal")
				}
				if r.Terminal.Outcome == "committed" {
					committed++
				} else if r.Terminal.Code == "REVISION_CONFLICT" {
					loser = n
				}
			}
			if committed != 1 || loser < 0 || value(t, db, "SELECT count(*) FROM link") != 1 {
				t.Fatal("concurrent graph facts jointly committed")
			}
			before := relationshipState(t, db)
			want := "BLOCKING_CYCLE"
			if kind == "duplicate" {
				want = "DUPLICATE_CYCLE"
			}
			if kind == "related" {
				want = "LINK_ALREADY_EXISTS"
			}
			code(t, issue(t, e, db, g, "link.add", ids[loser], exactRelationship(kind, peers[loser]), "not_committed"), want)
			if before != relationshipState(t, db) {
				t.Fatal("deliberate new intent missed current graph")
			}
		})
	}
}

func TestRelationshipsLegacyAddPrecedenceAgainstHistoricalEvidence(t *testing.T) {
	e, db := fixture(t)
	g := service(t, db)
	a := create(t, e, g, 0)
	// Controlled accepted evidence can be unavailable or self-referential with
	// no current internal target. It must not change legacy add refusal order.
	for n, f := range []struct{ kind, target string }{{"informational", a}, {"blocking", op}} {
		id := fmt.Sprintf("80000000-0000-4000-8000-%012d", n+1)
		execSQL(t, db, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'link',?,?,?,?)", inc, id, a, board, inc)
		execSQL(t, db, "INSERT INTO link(incarnation,link_id,card_id,kind,target_board,target_incarnation,target_card_id,active,created_at) VALUES(?,?,?,?,?,?,?,1,'2026-10-26T00:00:00Z')", inc, id, a, f.kind, board, inc, f.target)
		execSQL(t, db, "INSERT INTO current_reference VALUES(?,?,'link',?)", inc, a, id)
	}
	before := relationshipState(t, db)
	code(t, issue(t, e, db, g, "link.add", a, map[string]any{"kind": "informational", "targetCardId": a}, "not_committed"), "SELF_LINK")
	code(t, issue(t, e, db, g, "link.add", a, map[string]any{"kind": "blocking", "targetCardId": op}, "not_committed"), "NOT_FOUND")
	if before != relationshipState(t, db) {
		t.Fatal("legacy precedence refusal changed evidence")
	}
}
