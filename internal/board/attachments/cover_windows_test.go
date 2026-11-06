//go:build windows && amd64

package attachments

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"reflect"
	"testing"
	"time"

	"github.com/localswim/localswim/internal/board/cards"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/workflow"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/outcome"
	"github.com/localswim/localswim/internal/store/recovery"
	"github.com/localswim/localswim/internal/store/writer"
)

func coverRaster(t *testing.T, width int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, width, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func coverAdd(t *testing.T, f *fixture, rev uint64, b []byte) string {
	t.Helper()
	r, _ := f.add(t, f.upload(t, b, rev), b)
	return committed(t, r)
}

func coverSubmit(t *testing.T, f *fixture, g *grants.Service, i contract.Intent) writer.Result {
	t.Helper()
	return writer.New(f.s).Submit(tc(t), i, cards.New(g).Callbacks(cards.Route{BoardID: board, Incarnation: inc, CardID: f.card}))
}

func coverEdit(t *testing.T, f *fixture, rev uint64, candidate any) contract.Intent {
	t.Helper()
	return f.intent(t, "card.edit", f.card, rev, rev, map[string]any{"coverAttachmentId": candidate})
}

func coverExec(t *testing.T, f *fixture, query string, args ...any) {
	t.Helper()
	f.raw(t, func(c *sql.Conn) error {
		_, err := c.ExecContext(tc(t), query, args...)
		return err
	})
}

func coverText(t *testing.T, f *fixture, query string, args ...any) string {
	t.Helper()
	ctx := tc(t)
	l, err := f.s.Reader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var got string
	if err = l.Do(ctx, func(c *sql.Conn) error { return c.QueryRowContext(ctx, query, args...).Scan(&got) }); err != nil {
		t.Fatal(err)
	}
	return got
}

func coverOriginal(t *testing.T, f *fixture, g *grants.Service, i contract.Intent, r writer.Result, code string) {
	t.Helper()
	if r.Terminal == nil || r.Terminal.Code != code {
		t.Fatalf("wanted terminal %s, got %+v", code, r)
	}
	o, err := outcome.Base(i)
	if err != nil {
		t.Fatal(err)
	}
	original := writer.New(f.s).Lookup(tc(t), outcome.IdentityOf(o), g.Authority)
	if !reflect.DeepEqual(original.Terminal, r.Terminal) ||
		coverText(t, f, "SELECT intent FROM operation_full WHERE op_id=?", o.OpID) != string(i.CanonicalBytes()) {
		t.Fatal("exact retained original changed")
	}
	if code != "OK" {
		if r.Terminal.Outcome != "not_committed" || len(r.Terminal.ResultRefs) != 0 || len(r.Terminal.ByteFacts) != 0 {
			t.Fatal("refusal contains committed effects")
		}
		return
	}
	if i.Command() != "card.edit" {
		return
	}
	if r.Terminal.CardRevision == nil || r.Terminal.Revision == nil {
		t.Fatal("committed cover has no revisions")
	}
	cr := A.Number(*r.Terminal.CardRevision)
	wantRefs := []A.FactRef{{Kind: "card", ID: A.UUID(f.card), Revision: &cr}}
	if r.Terminal.Outcome != "committed" || r.Terminal.Command != "card.edit" ||
		A.Number(*r.Terminal.Revision) != o.ExpectedRevision+1 || *r.Terminal.CardRevision != *o.ExpectedCardRevision+1 ||
		!reflect.DeepEqual(r.Terminal.ResultRefs, wantRefs) || len(r.Terminal.ByteFacts) != 0 ||
		r.Terminal.Result == nil || string(*r.Terminal.Result) != `{"cardId":"`+f.card+`"}` {
		t.Fatal("cover receipt shape")
	}
}

func TestCoverChoiceApprovalHistoryAndDetach(t *testing.T) {
	f := fresh(t)
	a := coverAdd(t, f, 1, coverRaster(t, 1))
	b := coverAdd(t, f, 2, coverRaster(t, 2))
	original := coverEdit(t, f, 3, a)
	coverOriginal(t, f, f.g, original, coverSubmit(t, f, f.g, original), "OK")

	// Synthetic approval under an unfinished default-workflow role. The
	// subsequent cover change must invalidate it and preserve its prior fact.
	const approval = "80000000-0000-0000-0000-000000000002"
	coverExec(t, f, "INSERT INTO entity(incarnation,kind,entity_id,card_id,origin_board,origin_incarnation) VALUES(?,'approval',?,?,?,?)", inc, approval, f.card, board, inc)
	coverExec(t, f, "INSERT INTO approval(incarnation,approval_id,card_id,actor_id,reviewed_card_revision,reviewed_board_revision,policy_version,created_at) VALUES(?,?,?,?,3,3,1,'2026-11-06T00:00:00Z')", inc, approval, f.card, actor)
	coverExec(t, f, "UPDATE card SET current_approval_id=?", approval)
	replace := coverEdit(t, f, 4, b)
	coverOriginal(t, f, f.g, replace, coverSubmit(t, f, f.g, replace), "OK")
	if got := coverText(t, f, "SELECT json_extract(state,'$.coverAttachmentId') FROM card_revision WHERE card_revision=4"); got != a ||
		coverText(t, f, "SELECT json_extract(state,'$.currentApprovalId') FROM card_revision WHERE card_revision=4") != approval ||
		f.value(t, "SELECT count(*) FROM card WHERE current_approval_id IS NULL") != 1 ||
		f.value(t, "SELECT count(*) FROM revision_reference WHERE card_revision=4 AND kind='approval'") != 1 {
		t.Fatal("prior choice and approval were not retained")
	}
	repeat := coverEdit(t, f, 5, b)
	coverOriginal(t, f, f.g, repeat, coverSubmit(t, f, f.g, repeat), "NO_CHANGE")

	coverExec(t, f, "INSERT INTO grant_right VALUES(?,?,'edit')", inc, grant)
	title := f.intent(t, "card.edit", f.card, 5, 5, map[string]any{"title": "Preserve choice"})
	coverOriginal(t, f, f.g, title, coverSubmit(t, f, f.g, title), "OK")
	c := coverAdd(t, f, 6, coverRaster(t, 3))
	if got := coverText(t, f, "SELECT attachment_id FROM card_cover"); got != b {
		t.Fatal("title edit or add changed the cover")
	}
	clear := coverEdit(t, f, 7, nil)
	coverOriginal(t, f, f.g, clear, coverSubmit(t, f, f.g, clear), "OK")
	repeatClear := coverEdit(t, f, 8, nil)
	coverOriginal(t, f, f.g, repeatClear, coverSubmit(t, f, f.g, repeatClear), "NO_CHANGE")
	set := coverEdit(t, f, 8, b)
	coverOriginal(t, f, f.g, set, coverSubmit(t, f, f.g, set), "OK")
	r, _ := detachResult(t, f, c, 9)
	committed(t, r)
	if got := coverText(t, f, "SELECT attachment_id FROM card_cover"); got != b {
		t.Fatal("unrelated detach cleared the cover")
	}
	r, _ = detachResult(t, f, b, 10)
	committed(t, r)
	if f.value(t, "SELECT count(*) FROM card_cover") != 0 ||
		coverText(t, f, "SELECT json_extract(state,'$.coverAttachmentId') FROM card_revision WHERE card_revision=10") != b ||
		f.value(t, "SELECT count(*) FROM history_blob_pin WHERE card_revision=10") != 2 ||
		f.value(t, "SELECT count(*) FROM attachment") != 3 ||
		f.value(t, "SELECT count(*) FROM blob") != 3 ||
		f.value(t, "SELECT count(*) FROM activity") != 11 ||
		f.value(t, "SELECT count(*) FROM activity WHERE type='card.edit'") != 5 {
		t.Fatal("detach did not conserve the prior cover, references, bytes or activity")
	}
	if _, err := f.a.Read(tc(t), f.g, board, inc, actor, b); !errors.Is(err, ErrNotFound) {
		t.Fatal("ordinary download admitted a retained attachment", err)
	}
	absent := coverEdit(t, f, 11, b)
	coverOriginal(t, f, f.g, absent, coverSubmit(t, f, f.g, absent), "NOT_FOUND")
}

func TestCoverExactRightsCASAndReplay(t *testing.T) {
	f := fresh(t)
	a := coverAdd(t, f, 1, coverRaster(t, 1))
	invalid := coverAdd(t, f, 2, []byte("<svg/>"))
	set := coverEdit(t, f, 3, a)
	check := func(i contract.Intent, code string) {
		t.Helper()
		coverOriginal(t, f, f.g, i, coverSubmit(t, f, f.g, i), code)
	}
	coverExec(t, f, "DELETE FROM grant_right WHERE right_name='read'")
	check(set, "OK")
	check(f.intent(t, "card.edit", f.card, 4, 4, map[string]any{"title": "unauthorized"}), "FORBIDDEN")
	if _, err := f.a.Read(tc(t), f.g, board, inc, actor, a); !errors.Is(err, grants.ErrDenied) {
		t.Fatal("attach authority widened ordinary read", err)
	}
	check(coverEdit(t, f, 3, a), "REVISION_CONFLICT")
	check(f.intent(t, "card.edit", f.card, 4, 3, map[string]any{"coverAttachmentId": a}), "REVISION_CONFLICT")
	check(coverEdit(t, f, 4, "81000000-0000-0000-0000-000000000001"), "NOT_FOUND")
	check(coverEdit(t, f, 4, invalid), "INVALID_INPUT")
	coverExec(t, f, "INSERT INTO grant_right VALUES(?,?,'edit')", inc, grant)
	coverExec(t, f, "DELETE FROM grant_right WHERE right_name='attach'")
	check(coverEdit(t, f, 4, a), "FORBIDDEN")
	check(set, "OK")

	_, op, _, _, _, err := set.Context()
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"opId": op, "incarnation": inc, "target": map[string]string{"cardId": f.card},
		"expectedRevision": 3, "expectedCardRevision": 3, "input": map[string]any{"coverAttachmentId": nil}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	mismatch, err := T.FreezeMutation(raw, contract.ResolvedRoute{Command: "card.edit", BoardID: board, TargetID: f.card}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	if r := coverSubmit(t, f, f.g, mismatch); r.Refused == nil || r.Refused.Error.Code != "OPERATION_MISMATCH" {
		t.Fatal("reused ID bypassed original identity")
	}
	coverExec(t, f, "DELETE FROM grant_right WHERE right_name='read-own-outcome'")
	if r := coverSubmit(t, f, f.g, set); r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("retained original ignored live own-outcome authority")
	}
	coverExec(t, f, "INSERT INTO grant_right VALUES(?,?,'read-own-outcome'),(?,?,'attach')", inc, grant, inc, grant)
	native, err := grants.New(installation, origin, func(_ context.Context, _ writer.Read, n time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, ActorID: actor, Mode: "native-owner", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if r := coverSubmit(t, f, native, coverEdit(t, f, 4, nil)); r.Refused == nil || r.Refused.Error.Code != "FORBIDDEN" {
		t.Fatal("native owner gained cover authority")
	}
	coverExec(t, f, "UPDATE card SET archived=1")
	check(coverEdit(t, f, 4, nil), "OK")
	coverExec(t, f, "UPDATE card SET lane='Completed'")
	check(coverEdit(t, f, 5, nil), "COMPLETED")
	if f.value(t, "SELECT revision FROM board") != 5 || f.value(t, "SELECT count(*) FROM activity") != 5 {
		t.Fatal("a refusal changed domain state")
	}
}

func TestCoverRejectsForeignAndTombstonedReferences(t *testing.T) {
	f := fresh(t)
	data := coverRaster(t, 1)
	a := coverAdd(t, f, 1, data)
	set := coverEdit(t, f, 2, a)
	coverOriginal(t, f, f.g, set, coverSubmit(t, f, f.g, set), "OK")
	create := f.intent(t, "card.create", "", 3, 0, map[string]any{"title": "Other card", "description": ""})
	r := writer.New(f.s).Submit(tc(t), create, cards.New(f.g).Callbacks(cards.Route{BoardID: board, Incarnation: inc}))
	if r.Terminal == nil || r.Terminal.Code != "OK" || len(r.Terminal.ResultRefs) != 1 {
		t.Fatal("other card fixture")
	}
	other := *f
	other.card = string(r.Terminal.ResultRefs[0].ID)
	add := f.intent(t, "attachment.add", other.card, 4, 1, map[string]any{"name": "same-digest", "mediaLabel": "", "byteLength": len(data), "sha256": digest(data)})
	r, _ = other.add(t, add, data)
	b := committed(t, r)

	request := map[string]any{"opId": "81000000-0000-0000-0000-000000000002", "incarnation": inc,
		"target": map[string]string{"cardId": f.card}, "expectedRevision": 5, "expectedCardRevision": 3,
		"input": map[string]any{"coverAttachmentId": a}}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = T.FreezeMutation(raw, contract.ResolvedRoute{Command: "card.edit", BoardID: board, TargetID: other.card}, contract.AuthenticatedActor{ActorID: actor}); err == nil {
		t.Fatal("cover intent admitted on another card's route")
	}
	request["incarnation"] = "81000000-0000-0000-0000-000000000003"
	raw, err = json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	wrongInc, err := T.FreezeMutation(raw, contract.ResolvedRoute{Command: "card.edit", BoardID: board, TargetID: f.card}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	wrong := coverSubmit(t, f, f.g, wrongInc)
	if wrong.Unknown == nil || wrong.Unknown.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" {
		t.Fatal("wrong incarnation reached cover evaluation")
	}
	if f.value(t, "SELECT count(*) FROM operation") != 5 {
		t.Fatal("wrong route or incarnation recorded an original")
	}

	foreign := f.intent(t, "card.edit", f.card, 5, 3, map[string]any{"coverAttachmentId": b})
	coverOriginal(t, f, f.g, foreign, coverSubmit(t, f, f.g, foreign), "NOT_FOUND")
	detach := f.intent(t, "attachment.detach", f.card, 5, 3, map[string]any{"attachmentId": a})
	r, _ = f.a.Detach(tc(t), f.g, Route{board, inc, f.card}, detach)
	committed(t, r)
	retained := f.intent(t, "card.edit", f.card, 6, 4, map[string]any{"coverAttachmentId": a})
	coverOriginal(t, f, f.g, retained, coverSubmit(t, f, f.g, retained), "NOT_FOUND")
	if coverText(t, f, "SELECT attachment_id FROM history_blob_pin WHERE card_id=? AND card_revision=3", f.card) != a ||
		coverText(t, f, "SELECT json_extract(state,'$.coverAttachmentId') FROM card_revision WHERE card_id=? AND card_revision=3", f.card) != a {
		t.Fatal("detach lost the selected historical state or pin")
	}

	coverExec(t, f, "INSERT INTO grant_right VALUES(?,?,'history-admin')", inc, grant)
	input, err := json.Marshal(A.ClearInput{ThroughTime: r.Terminal.TerminalAt})
	if err != nil {
		t.Fatal(err)
	}
	svc := recovery.New(f.s, f.g)
	route := recovery.Route{BoardID: board, Incarnation: inc}
	preview, err := svc.Preview(tc(t), route, actor, A.PreviewRequest{Incarnation: inc, ExpectedRevision: 6, Command: "history.clear", Input: input})
	if err != nil {
		t.Fatal(err)
	}
	op, err := uuid()
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(A.Confirmation{OpID: A.UUID(op), Incarnation: inc, ExpectedRevision: 6, Command: "history.clear", Input: input, PreviewHash: preview.PreviewHash})
	if err != nil {
		t.Fatal(err)
	}
	clear, err := T.FreezeMutation(wire, contract.ResolvedRoute{Command: "history.clear", BoardID: board}, contract.AuthenticatedActor{ActorID: actor})
	if err != nil {
		t.Fatal(err)
	}
	result := svc.Confirm(tc(t), route, clear)
	if result.Terminal == nil || result.Terminal.Code != "OK" ||
		coverText(t, f, "SELECT entity_id FROM entity WHERE kind='attachment' AND removed_at IS NOT NULL") != a ||
		coverText(t, f, "SELECT attachment_id FROM attachment") != b ||
		coverText(t, f, "SELECT entity_id FROM current_reference WHERE kind='attachment'") != b ||
		coverText(t, f, "SELECT attachment_id FROM current_attachment_pin") != b ||
		coverText(t, f, "SELECT sha256 FROM blob") != digest(data) ||
		coverText(t, f, "SELECT count(*) FROM card_revision WHERE card_id=? AND card_revision=3 AND state IS NULL AND removal_reason='cleared'", f.card) != "1" {
		t.Fatal("authorized retention did not tombstone selected history while conserving the peer")
	}
	tombstone := f.intent(t, "card.edit", f.card, 7, 4, map[string]any{"coverAttachmentId": a})
	coverOriginal(t, f, f.g, tombstone, coverSubmit(t, f, f.g, tombstone), "NOT_FOUND")
}

const coverRaceActor = "83000000-0000-0000-0000-000000000001"
const coverRaceGrant = "83000000-0000-0000-0000-000000000002"

func coverSecondActor(t *testing.T, f *fixture, kind string) *grants.Service {
	t.Helper()
	coverExec(t, f, "INSERT INTO actor VALUES(?,?,?,'Synthetic peer',?,?)", inc, coverRaceActor, kind, board, inc)
	coverExec(t, f, "INSERT INTO grant_fact VALUES(?,?)", inc, coverRaceGrant)
	coverExec(t, f, "INSERT INTO grant_record VALUES(?,?,?,1)", inc, coverRaceGrant, coverRaceActor)
	coverExec(t, f, "INSERT INTO grant_right VALUES(?,?,'attach'),(?,?,'read-own-outcome')", inc, coverRaceGrant, inc, coverRaceGrant)
	g, err := grants.New(installation, origin, func(_ context.Context, _ writer.Read, n time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: board, Incarnation: inc, ActorID: coverRaceActor, GrantID: coverRaceGrant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestCoverConfiguredActorCeilingAndCompletedRole(t *testing.T) {
	const initial, progress, review, done = "84000000-0000-0000-0000-000000000001", "84000000-0000-0000-0000-000000000002", "84000000-0000-0000-0000-000000000003", "84000000-0000-0000-0000-000000000004"
	wf := &A.Workflow{
		WorkflowID: "84000000-0000-0000-0000-000000000005", Revision: 1,
		Lanes: []A.WorkflowLane{
			{ID: initial, Label: "Completed", Role: "initial"}, {ID: progress, Label: "Build", Role: "in_progress"},
			{ID: review, Label: "Inspect", Role: "review"}, {ID: done, Label: "Accepted", Role: "completed"},
		},
		Transitions: []A.WorkflowTransition{
			{From: initial, To: progress, Actors: []A.Kind{"human"}},
			{From: progress, To: review, Actors: []A.Kind{"human"}},
			{From: review, To: done, Actors: []A.Kind{"human"}},
		},
		ActorRights: A.WorkflowActorRights{
			Human:      []string{"approve", "attach", "create", "edit", "read", "read-own-outcome", "review-handoff"},
			Automation: []string{"read", "read-own-outcome"},
		},
	}
	f := fresh(t, wf)
	g := coverSecondActor(t, f, "automation")
	a := coverAdd(t, f, 1, coverRaster(t, 1))
	forbidden := f.intentAs(t, coverRaceActor, "card.edit", f.card, 2, 2, map[string]any{"coverAttachmentId": a})
	coverOriginal(t, f, g, forbidden, coverSubmit(t, f, g, forbidden), "FORBIDDEN")
	set := coverEdit(t, f, 2, a)
	coverOriginal(t, f, f.g, set, coverSubmit(t, f, f.g, set), "OK")
	clear := coverEdit(t, f, 3, nil)
	coverOriginal(t, f, f.g, clear, coverSubmit(t, f, f.g, clear), "OK")

	coverExec(t, f, "INSERT INTO grant_right VALUES(?,?,'edit'),(?,?,'approve'),(?,?,'review-handoff')", inc, grant, inc, grant, inc, grant)
	move := f.intent(t, "card.edit", f.card, 4, 4, map[string]any{"laneId": progress})
	coverOriginal(t, f, f.g, move, coverSubmit(t, f, f.g, move), "OK")
	cb := workflow.New(f.g).Callbacks(workflow.Route{BoardID: board, Incarnation: inc, CardID: f.card})
	engine := writer.New(f.s)
	handoff := f.intent(t, "review-handoff", f.card, 5, 5, map[string]any{})
	approve := f.intent(t, "approve", f.card, 6, 6, map[string]any{"policyVersion": 1})
	for _, i := range []contract.Intent{handoff, approve} {
		if r := engine.Submit(tc(t), i, cb); r.Terminal == nil || r.Terminal.Code != "OK" {
			t.Fatalf("configured workflow transition failed: %s %+v", i.Command(), r)
		}
	}
	for _, candidate := range []any{nil, a} {
		i := coverEdit(t, f, 7, candidate)
		coverOriginal(t, f, f.g, i, coverSubmit(t, f, f.g, i), "COMPLETED")
	}
}

func TestCoverSameBaseRaces(t *testing.T) {
	for _, pair := range []struct {
		name, peerCommand string
		setB, detachA     bool
	}{
		{name: "set-clear", peerCommand: "card.edit", setB: true},
		{name: "set-detach", peerCommand: "attachment.detach", setB: true},
		{name: "clear-detach", peerCommand: "attachment.detach", detachA: true},
	} {
		t.Run(pair.name, func(t *testing.T) {
			f := fresh(t)
			g := coverSecondActor(t, f, "human")
			a := coverAdd(t, f, 1, coverRaster(t, 1))
			b := coverAdd(t, f, 2, coverRaster(t, 2))
			set := coverEdit(t, f, 3, a)
			coverOriginal(t, f, f.g, set, coverSubmit(t, f, f.g, set), "OK")
			var choice any
			if pair.setB {
				choice = b
			}
			first := coverEdit(t, f, 4, choice)
			peerInput := map[string]any{"coverAttachmentId": nil}
			detached := b
			if pair.detachA {
				detached = a
			}
			if pair.peerCommand == "attachment.detach" {
				peerInput = map[string]any{"attachmentId": detached}
			}
			peer := f.intentAs(t, coverRaceActor, pair.peerCommand, f.card, 4, 4, peerInput)
			start := make(chan struct{})
			ready := make(chan struct{}, 2)
			firstDone := make(chan writer.Result, 1)
			peerDone := make(chan writer.Result, 1)
			go func() {
				ready <- struct{}{}
				<-start
				firstDone <- coverSubmit(t, f, f.g, first)
			}()
			go func() {
				ready <- struct{}{}
				<-start
				if pair.peerCommand == "card.edit" {
					peerDone <- coverSubmit(t, f, g, peer)
					return
				}
				r, _ := f.a.Detach(tc(t), g, Route{board, inc, f.card}, peer)
				peerDone <- r
			}()
			<-ready
			<-ready
			close(start)
			r1, r2 := <-firstDone, <-peerDone
			if r1.Terminal == nil || r2.Terminal == nil {
				t.Fatalf("race did not return two durable originals: %+v, %+v", r1, r2)
			}
			firstCode, peerCode := "REVISION_CONFLICT", "OK"
			wantCover := ""
			wantAttachments := uint64(2)
			if r1.Terminal.Code == "OK" {
				firstCode, peerCode = "OK", "REVISION_CONFLICT"
				if pair.setB {
					wantCover = b
				}
			} else if pair.peerCommand == "attachment.detach" {
				wantAttachments = 1
				if !pair.detachA {
					wantCover = a
				}
			}
			coverOriginal(t, f, f.g, first, r1, firstCode)
			coverOriginal(t, f, g, peer, r2, peerCode)
			if coverText(t, f, "SELECT coalesce((SELECT attachment_id FROM card_cover),'')") != wantCover ||
				f.value(t, "SELECT count(*) FROM current_reference WHERE kind='attachment'") != wantAttachments ||
				f.value(t, "SELECT revision FROM board") != 5 || f.value(t, "SELECT revision FROM card") != 5 ||
				f.value(t, "SELECT count(*) FROM activity") != 5 ||
				coverText(t, f, "SELECT json_extract(state,'$.coverAttachmentId') FROM card_revision WHERE card_revision=4") != a ||
				f.value(t, "SELECT count(*) FROM history_blob_pin WHERE card_revision=4") != 2 {
				t.Fatal("race did not retain exactly one effect and full prior state")
			}
		})
	}
}

func TestCoverRollbackAndIntegrityFences(t *testing.T) {
	for _, fault := range []string{"edit-activity", "detach-activity", "digest", "missing"} {
		t.Run(fault, func(t *testing.T) {
			f := fresh(t)
			data := coverRaster(t, 1)
			a := coverAdd(t, f, 1, data)
			set := coverEdit(t, f, 2, a)
			coverOriginal(t, f, f.g, set, coverSubmit(t, f, f.g, set), "OK")
			var r writer.Result
			switch fault {
			case "edit-activity":
				coverExec(t, f, "CREATE TRIGGER synthetic_cover_abort BEFORE INSERT ON activity WHEN NEW.type='card.edit' BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END")
				r = coverSubmit(t, f, f.g, coverEdit(t, f, 3, nil))
			case "detach-activity":
				coverExec(t, f, "CREATE TRIGGER synthetic_cover_abort BEFORE INSERT ON activity WHEN NEW.type='attachment.detach' BEGIN SELECT RAISE(ABORT,'synthetic rollback'); END")
				r, _ = detachResult(t, f, a, 3)
			case "digest":
				coverExec(t, f, "DROP TRIGGER blob_immutable")
				bad := bytes.Clone(data)
				bad[0] ^= 1
				coverExec(t, f, "UPDATE blob SET bytes=?", bad)
				r = coverSubmit(t, f, f.g, coverEdit(t, f, 3, a))
			case "missing":
				// Deliberately corrupt the synthetic store below the normal FK boundary.
				f.raw(t, func(c *sql.Conn) error {
					ctx := tc(t)
					if _, err := c.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
						return err
					}
					_, delErr := c.ExecContext(ctx, "DELETE FROM blob")
					_, restoreErr := c.ExecContext(ctx, "PRAGMA foreign_keys=ON")
					return errors.Join(delErr, restoreErr)
				})
				r = coverSubmit(t, f, f.g, coverEdit(t, f, 3, a))
			}
			if r.Unknown == nil ||
				coverText(t, f, "SELECT attachment_id FROM card_cover") != a ||
				f.value(t, "SELECT revision FROM board") != 3 || f.value(t, "SELECT revision FROM card") != 3 ||
				f.value(t, "SELECT count(*) FROM current_attachment_pin") != 1 ||
				f.value(t, "SELECT count(*) FROM card_revision WHERE card_revision=3") != 0 ||
				f.value(t, "SELECT count(*) FROM operation") != 3 || f.value(t, "SELECT count(*) FROM activity") != 3 {
				t.Fatal("failed transaction inferred noncommit or changed domain state")
			}
			l, err := f.s.Writer(tc(t))
			if l != nil {
				l.Close()
			}
			if fault == "digest" || fault == "missing" {
				if !errors.Is(err, S.ErrFenced) {
					t.Fatal("confirmed integrity failure did not fence writes", err)
				}
			} else if err != nil {
				t.Fatal("clean rollback fenced writes", err)
			}
		})
	}
}
