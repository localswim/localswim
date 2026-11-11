package v2

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func TestM82RefusesSnapshotChangedDuringWritePreparation(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	path := s.snapshotPath(initial.Board.ID)
	external := cloneSnapshot(initial)
	external.Board.Name = "Changed after admission"
	replacement := marshalVerificationFixture(t, external)
	if _, err := parseSnapshot(replacement, initial.Board.ID); err != nil {
		t.Fatalf("expected a valid same-revision replacement: %v", err)
	}

	_, err := s.update(initial.Board.ID, initial.Board.ETag, func(next *snapshot) error {
		if err := writeAtomic(path, replacement); err != nil {
			return err
		}
		return m01Rename(next)
	})
	if !errors.Is(err, errDurableStateChanged) {
		t.Fatalf("expected a known refusal for a change during preparation, got %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, replacement) {
		t.Fatalf("refused write changed the replacement bytes: %v", err)
	}
	current, exists := s.accepted(initial.Board.ID)
	if !exists || !reflect.DeepEqual(current, initial) || s.fencedBoard() != "" {
		t.Fatal("refused write changed or fenced the accepted snapshot")
	}
	pending, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".pending-*.tmp"))
	if err != nil || len(pending) != 0 {
		t.Fatalf("refused write left a staged snapshot: count %d, error %v", len(pending), err)
	}
}

func TestM82OrdinaryWritesRefuseChangedDurableSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, healthCode, detail string
		valid                    bool
	}{
		{"corrupt", "durable_state_unavailable", "Durable state is unavailable.", false},
		{"same revision", "durable_state_changed", "Saved board state differs from the accepted snapshot.", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, client := attachmentHTTPServer(t)
			raw, _ := client.call("POST", "/boards", `{"name":"Accepted board"}`, "", 201)
			accepted := turnOneDecode[board](t, raw)
			boardPath := "/boards/" + accepted.ID
			snapshotPath := "/snapshots/" + accepted.ID
			savedPath := app.store.snapshotPath(accepted.ID)
			raw, _ = client.call("POST", "/boards", `{"name":"Second board"}`, "", 201)
			second := turnOneDecode[board](t, raw)

			source, headers := client.call("GET", snapshotPath, "", "", 200)
			sourceState := turnOneDecode[snapshot](t, source)
			recoveryTag := headers.Get("ETag")
			replacement := []byte("{")
			if tc.valid {
				external := cloneSnapshot(sourceState)
				// The creation audit does not record the initial board name.
				external.Board.Name = "Independently changed board"
				replacement = marshalVerificationFixture(t, external)
				saved, err := parseSnapshot(replacement, accepted.ID)
				if err != nil || saved.Board.ETag != accepted.ETag || saved.Board.Revision != accepted.Revision {
					t.Fatalf("replacement must be valid at the accepted revision: %v", err)
				}
			}
			if err := writeAtomic(savedPath, replacement); err != nil {
				t.Fatal(err)
			}

			raw, _ = client.call("GET", "/boards", "", "", 200)
			rows := turnOnePage[boardSummary](t, raw, "boards").Values
			i := slices.IndexFunc(rows, func(row boardSummary) bool { return row.ID == accepted.ID })
			if i < 0 || rows[i].Availability != "unhealthy" || rows[i].ErrorCode == nil ||
				*rows[i].ErrorCode != tc.healthCode || rows[i].Attention != nil {
				t.Fatalf("catalog did not report %s for the affected board", tc.healthCode)
			}
			raw, _ = client.call("GET", "/verifications/"+accepted.ID, "", "", 200)
			if turnOneDecode[verification](t, raw).Valid != tc.valid {
				t.Fatal("native verification did not report the durable file's structural validity")
			}

			for _, body := range []string{`{"name":"Attempted rename"}`, `{"name":"Accepted board"}`} {
				raw, _ = client.call("PATCH", boardPath, body, accepted.ETag, 503)
				refusal := turnOneDecode[problemBody](t, raw)
				if refusal.Code != "unavailable" || refusal.Outcome != "not_applied" || refusal.Detail != tc.detail {
					t.Fatalf("unexpected write refusal: %+v", refusal)
				}
			}
			raw, _ = client.call("PATCH", "/boards/"+second.ID, `{"name":"Second board renamed"}`, second.ETag, 200)
			if got := turnOneDecode[board](t, raw); got.Name != "Second board renamed" || got.Revision != second.Revision+1 {
				t.Fatal("the second board did not commit its rename")
			}
			saved, err := os.ReadFile(savedPath)
			if err != nil || !bytes.Equal(saved, replacement) {
				t.Fatalf("refused writes changed the durable snapshot: %v", err)
			}
			current, exists := app.store.accepted(accepted.ID)
			if !exists || !reflect.DeepEqual(current, sourceState) {
				t.Fatal("refused writes changed the accepted snapshot")
			}

			if tc.valid {
				_, headers = client.call("GET", snapshotPath, "", "", 200)
				if headers.Get("ETag") == recoveryTag {
					t.Fatal("different native representations shared a validator")
				}
				recoveryTag = headers.Get("ETag")
			}
			raw, _ = recoveryPut(t, client, accepted.ID, recoveryTag, "application/json", source, 200)
			recovered := turnOneDecode[board](t, raw)
			client.call("PATCH", boardPath, `{"name":"Renamed after recovery"}`, recovered.ETag, 200)

			raw, _ = client.call("GET", "/boards", "", "", 200)
			rows = turnOnePage[boardSummary](t, raw, "boards").Values
			i = slices.IndexFunc(rows, func(row boardSummary) bool { return row.ID == accepted.ID })
			if i < 0 || rows[i].Availability != "open" || rows[i].ErrorCode != nil {
				t.Fatal("the repaired board remained unhealthy")
			}
		})
	}
}
