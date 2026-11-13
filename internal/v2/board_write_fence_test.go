package v2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func m85FailSettlement(s *store, ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeRecoverySettlement = func(id string) error {
		for _, target := range ids {
			if target == id {
				return errStale // The replacement has already committed the snapshot.
			}
		}
		return nil
	}
}

func m85Board(t *testing.T, human turnOneClient, name string) board {
	t.Helper()
	raw, _ := human.call("POST", "/boards", fmt.Sprintf(`{"name":%q}`, name), "", 201)
	return turnOneDecode[board](t, raw)
}

func m85PendingEffects(t *testing.T, human turnOneClient, b board) card {
	t.Helper()
	path := "/notifications/" + b.ID
	_, headers := human.call("GET", path, "", "", 200)
	human.call("PATCH", path, fmt.Sprintf(`{"enabled":true,"target_thread_id":%q}`, ownerNoticeTestTarget), headers.Get("ETag"), 200)
	raw, _ := human.call("PATCH", "/boards/"+b.ID, `{"publishing":{"enabled":true}}`, b.entityTag(), 200)
	return attachmentHTTPCard(t, human, turnOneDecode[board](t, raw))
}

func m85PendingNotice(worker *ownerNoticeWorker, id string) *pendingOwnerNotice {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.pending[id]
}

func m85FakeNotices(s *store) *ownerNoticeWorker {
	s.ownerNotices.close()
	worker := newOwnerNoticeWorker(time.Hour, s.ownerNoticeSettings, func(context.Context, string, string) bool { return false })
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ownerNotices = worker
	return worker
}

func m85AssertProblem(t *testing.T, raw []byte, code, outcome string) {
	t.Helper()
	got := turnOneDecode[problemBody](t, raw)
	if got.Code != code || got.Outcome != outcome {
		t.Fatalf("problem = %s/%s, want %s/%s", got.Code, got.Outcome, code, outcome)
	}
}

func TestM85PostReplacementSettlementFailureIsBoardLocal(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	s := app.store
	worker := m85FakeNotices(s)
	target, neighbor := m85Board(t, human, "Affected board"), m85Board(t, human, "Neighbor board")
	targetCard, neighborCard := m85PendingEffects(t, human, target), m85PendingEffects(t, human, neighbor)
	previous, _ := s.accepted(target.ID)
	neighborBurst := m85PendingNotice(worker, neighbor.ID)
	if m85PendingNotice(worker, target.ID) == nil || neighborBurst == nil {
		t.Fatal("committed Human card edits did not queue both fixture bursts")
	}
	m85FailSettlement(s, target.ID)
	const title = "Replaced before reference settlement"
	raw, _ := human.call("PATCH", "/cards/"+targetCard.ID, fmt.Sprintf(`{"title":%q}`, title), targetCard.entityTag(), 503)
	m85AssertProblem(t, raw, "outcome_unknown", "unknown")
	raw, _ = human.call("GET", "/snapshots/"+target.ID, "", "", 200)
	durable := turnOneDecode[snapshot](t, raw)
	reference, valid := s.readRecoveryReference(target.ID)
	if len(durable.Cards) != 1 || durable.Cards[0].Title != title || durable.Board.Revision != previous.Board.Revision+1 ||
		!valid || reference.Committed.RevisionFloor != previous.Board.Revision || reference.Pending == nil ||
		reference.Pending.SnapshotDigest != recoveryDigest(mustSnapshotJSON(t, durable)) {
		t.Fatal("the combined failure did not leave the replaced snapshot and unsettled reference")
	}
	if current, _ := s.accepted(target.ID); current.Board.Revision != previous.Board.Revision || current.Cards[0].Title != targetCard.Title {
		t.Fatal("the failed commit advanced accepted state")
	}
	raw, headers := human.call("GET", "/cards/"+targetCard.ID, "", "", 200)
	readCard := turnOneDecode[card](t, raw)
	if readCard.Title != title || readCard.Revision != durable.Board.Revision || headers.Get("ETag") != readCard.entityTag() {
		t.Fatal("focused card read did not return the durable revision")
	}
	if m85PendingNotice(worker, target.ID) != nil || m85PendingNotice(worker, neighbor.ID) != neighborBurst {
		t.Fatal("pending notices were not canceled only for the affected board")
	}
	if _, ok := s.ownerNoticeSettings(target.ID); ok {
		t.Fatal("the fenced board still supplies a notice route")
	}
	if _, enabled := s.publishers[target.ID].enabledSelection(); enabled {
		t.Fatal("the fenced board retained a runnable publisher")
	}
	if _, err := s.captureBoardPublication(target.ID); err == nil {
		t.Fatal("the fenced board remained eligible for automatic capture")
	}
	if _, err := s.captureBoardPublication(neighbor.ID); err != nil {
		t.Fatal("the neighbor could not capture its accepted revision", err)
	}
	raw, _ = human.call("PATCH", "/cards/"+targetCard.ID, `{"title":"Refused repeat"}`, targetCard.entityTag(), 503)
	m85AssertProblem(t, raw, "unavailable", "not_applied")
	_, headers = human.call("GET", "/notifications/"+target.ID, "", "", 200)
	raw, _ = human.call("PATCH", "/notifications/"+target.ID, `{"enabled":false}`, headers.Get("ETag"), 503)
	m85AssertProblem(t, raw, "unavailable", "not_applied")
	if _, err := s.captureBackupSnapshot(target.ID, humanID, previous.Board.Revision); !errors.Is(err, errWriteFenced) {
		t.Fatal("explicit capture bypassed the fence", err)
	}
	if readback, err := s.durable(target.ID); err != nil || readback.Board.Revision != durable.Board.Revision || readback.Cards[0].Title != title {
		t.Fatal("a refused attempt changed the durable outcome", err)
	}
	human.call("PATCH", "/cards/"+neighborCard.ID, `{"title":"Neighbor continued"}`, neighborCard.entityTag(), 200)
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, human := restartBoardPublication(t, s.root, human.token)
	settled, valid := restarted.store.readRecoveryReference(target.ID)
	if !valid || settled.Pending != nil || settled.Committed.RevisionFloor != durable.Board.Revision {
		t.Fatal("restart did not verify and settle the exact durable outcome")
	}
	human.call("PATCH", "/cards/"+targetCard.ID, `{"title":"Writable after verification"}`, durable.Cards[0].entityTag(), 200)
}

func TestM85IndependentFencesReserveTentativeIdentity(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	first, second, healthy := m85Board(t, human, "First"), m85Board(t, human, "Second"), m85Board(t, human, "Healthy")
	id := newID()
	create := func(b board, cardID string, status int) []byte {
		body := fmt.Sprintf(`{"id":%q,"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"New card"}`,
			cardID, b.ID, b.entityTag(), b.LaneIDs[0])
		raw, _ := human.call("POST", "/cards", body, "", status)
		return raw
	}
	m85FailSettlement(app.store, first.ID, second.ID)
	m85AssertProblem(t, create(first, id, 503), "outcome_unknown", "unknown")
	if accepted, _ := app.store.accepted(first.ID); len(accepted.Cards) != 0 {
		t.Fatal("the fenced creation advanced accepted state")
	}
	raw, headers := human.call("GET", "/cards/"+id, "", "", 200)
	created := turnOneDecode[card](t, raw)
	if created.ID != id || created.BoardID != first.ID || created.Revision != first.Revision+1 ||
		created.Title != "New card" || headers.Get("ETag") != created.entityTag() {
		t.Fatal("focused readback did not find the fenced creation")
	}
	human.call("GET", "/cards/"+newID(), "", "", 404)
	create(healthy, id, 409)
	m85AssertProblem(t, create(second, newID(), 503), "outcome_unknown", "unknown")
	m85AssertProblem(t, create(first, newID(), 503), "unavailable", "not_applied")
	m85AssertProblem(t, create(second, newID(), 503), "unavailable", "not_applied")
	create(healthy, newID(), 201)
}

func TestM85UncertainBoardCreationFencesWorkspace(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	s := app.store
	worker := m85FakeNotices(s)
	neighbor := m85Board(t, human, "Existing board")
	item := m85PendingEffects(t, human, neighbor)
	if m85PendingNotice(worker, neighbor.ID) == nil {
		t.Fatal("fixture did not queue a pending notice")
	}
	id := newID()
	m85FailSettlement(s, id)
	raw, _ := human.call("POST", "/boards", fmt.Sprintf(`{"id":%q,"name":"Uncertain creation"}`, id), "", 503)
	m85AssertProblem(t, raw, "outcome_unknown", "unknown")
	if durable, err := s.durable(id); err != nil || durable.Board.ID != id {
		t.Fatal("creation did not reach the durable snapshot", err)
	}
	human.call("GET", "/snapshots/"+id, "", "", 404)
	human.call("GET", "/verifications/"+id, "", "", 404)
	raw, _ = human.call("PATCH", "/cards/"+item.ID, `{"title":"Fenced neighbor"}`, item.entityTag(), 503)
	m85AssertProblem(t, raw, "unavailable", "not_applied")
	if m85PendingNotice(worker, neighbor.ID) != nil {
		t.Fatal("the workspace fence did not cancel the neighbor's pending notice")
	}
	if _, err := s.captureBoardPublication(neighbor.ID); err == nil {
		t.Fatal("the workspace fence did not refuse automatic capture")
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	_, human = restartBoardPublication(t, s.root, human.token)
	human.call("GET", "/boards/"+id, "", "", 200)
	human.call("GET", "/snapshots/"+id, "", "", 200)
	human.call("PATCH", "/cards/"+item.ID, `{"title":"Resumed neighbor"}`, item.entityTag(), 200)
}

func TestM85UnresolvedPairUsesM84Recovery(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	s := app.store
	target, neighbor := m85Board(t, human, "Affected board"), m85Board(t, human, "Healthy board")
	damaged := []byte("damaged native snapshot")
	captured := make(chan []byte, 1)
	s.mu.Lock()
	s.beforeRecoverySettlement = func(id string) error {
		if id != target.ID {
			return nil
		}
		path := s.snapshotPath(id)
		file, err := s.readDurableFileLocked(path)
		if err != nil {
			return err
		}
		if err := writeAtomic(path, damaged); err != nil {
			return err
		}
		captured <- file.data
		return errStale
	}
	s.mu.Unlock()
	raw, _ := human.call("PATCH", "/boards/"+target.ID, `{"name":"Candidate to recover"}`, target.entityTag(), 503)
	m85AssertProblem(t, raw, "outcome_unknown", "unknown")
	var source []byte
	select {
	case source = <-captured:
	default:
		t.Fatal("fixture did not capture the replaced snapshot before damaging it")
	}
	raw, _ = human.call("PATCH", "/boards/"+neighbor.ID, `{"name":"Unverified outcome blocks this edit"}`, neighbor.entityTag(), 503)
	m85AssertProblem(t, raw, "unavailable", "not_applied")
	reference, err := os.ReadFile(s.recoveryReferencePath(target.ID))
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	app, human = restartBoardPublication(t, s.root, human.token)
	raw, headers := human.call("GET", "/verifications/"+target.ID, "", "", 200)
	report := turnOneDecode[verification](t, raw)
	if report.Valid || report.Recovery == nil || report.Recovery.Mode != "fresh_only" {
		t.Fatal("neither a damaged snapshot nor its unsettled reference proves continuity")
	}
	human.call("PATCH", "/boards/"+neighbor.ID, `{"name":"Neighbor after quarantine"}`, neighbor.entityTag(), 200)
	failedRecoveryPost(t, human, target.ID, "same_id", headers.Get("ETag"), "application/json", source, 403)
	raw, _ = failedRecoveryPost(t, human, target.ID, "fresh", headers.Get("ETag"), "application/json", source, 201)
	recovered := turnOneDecode[board](t, raw)
	if recovered.ID == target.ID || recovered.Name != "Candidate to recover" || !app.store.isFailedBoard(target.ID) {
		t.Fatal("explicit fresh recovery did not preserve content and source isolation")
	}
	current, err := os.ReadFile(s.recoveryReferencePath(target.ID))
	if err != nil || !bytes.Equal(current, reference) {
		t.Fatal("recovery changed the unresolved source reference", err)
	}
	if current, err := os.ReadFile(s.snapshotPath(target.ID)); err != nil || !bytes.Equal(current, damaged) {
		t.Fatal("recovery changed the unresolved source snapshot", err)
	}
	human.call("PATCH", "/boards/"+recovered.ID, `{"name":"Recovered and writable"}`, recovered.entityTag(), 200)
}

func TestM85RestorationReceiptSkipsUnrelatedDamagedFence(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	s := app.store
	raw, _ := human.call("POST", "/boards", `{"id":"00000000-0000-4000-8000-000000000001","name":"First board"}`, "", 201)
	first := turnOneDecode[board](t, raw)
	raw, _ = human.call("POST", "/boards", `{"id":"00000000-0000-4000-8000-000000000002","name":"Receipt board"}`, "", 201)
	neighbor := turnOneDecode[board](t, raw)
	state, _ := s.accepted(neighbor.ID)
	item, _ := captureRecoveryBackup(t, app, state)
	restore := func(id, expected string, status int) []byte {
		body := fmt.Sprintf(`{"id":%q,"board_id":%q,"backup_id":%q,"expected_board_etag":%q}`, id, neighbor.ID, item.ID, expected)
		raw, _ := human.call("POST", "/restorations", body, "", status)
		return raw
	}
	knownID := newOperationID()
	known := turnOneDecode[restoration](t, restore(knownID, neighbor.entityTag(), 201))
	m85FailSettlement(s, first.ID)
	raw, _ = human.call("PATCH", "/boards/"+first.ID, `{"name":"Locally fenced"}`, first.entityTag(), 503)
	m85AssertProblem(t, raw, "outcome_unknown", "unknown")
	if err := writeAtomic(s.snapshotPath(first.ID), []byte("invalid")); err != nil {
		t.Fatal(err)
	}
	raw, _ = human.call("GET", "/restorations/"+knownID, "", "", 200)
	if got := turnOneDecode[restoration](t, raw); got != known {
		t.Fatal("the unrelated damaged board affected receipt lookup")
	}
	tentativeID := newOperationID()
	m85FailSettlement(s, neighbor.ID)
	m85AssertProblem(t, restore(tentativeID, etag(neighbor.ID, known.Revision), 503), "outcome_unknown", "unknown")
	raw, _ = human.call("GET", "/restorations/"+tentativeID, "", "", 200)
	if got := turnOneDecode[restoration](t, raw); got.ID != tentativeID || got.BoardID != neighbor.ID || got.Revision != known.Revision+1 {
		t.Fatal("the affected board's durable receipt did not resolve by its original ID")
	}
}
