package v2

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestM94SameMetadataChangesAndRepair(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	path := s.snapshotPath(initial.Board.ID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	external := cloneSnapshot(initial)
	external.Board.Name = "Modified board"
	changed := marshalVerificationFixture(t, external)
	damaged := bytes.Clone(original)
	damaged[0] = '!'
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"initial", original, nil},
		{"damaged", damaged, errInvalidSnapshot},
		{"repaired", original, nil},
		{"diverged", changed, errDurableStateChanged},
		{"repaired again", original, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.data) != len(original) {
				t.Fatal("fixture must retain the snapshot size")
			}
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				s.mu.RLock()
				err = s.checkAcceptedDurableLocked(initial)
				s.mu.RUnlock()
				if !errors.Is(err, tc.want) {
					t.Fatalf("durable result = %v, want %v", err, tc.want)
				}
				result, found := s.durableValidation.get(initial.Board.ID, initial.Board.Revision, sha256.Sum256(tc.data))
				if !found || !errors.Is(result, tc.want) {
					t.Fatalf("validation result was not retained: %v, %v", found, result)
				}
			}
			row, visible := s.boardSummary(initial.Board.ID, m01HumanID)
			if !visible || (row.Availability == "unhealthy") != (tc.want != nil) {
				t.Fatalf("catalog did not reconcile health: %+v", row)
			}
		})
	}
}

func TestM94CachedValidationRechecksNamedFile(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	path := s.snapshotPath(initial.Board.ID)
	observed, err := readSnapshotFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.checkAcceptedFileCached(path, initial, observed); err != nil {
		t.Fatal(err)
	}
	// Preserve bytes, size and timestamp, but replace the file identity after
	// reading. The cached success cannot validate that old opened file's name.
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, observed.data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, observed.info.ModTime(), observed.info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := replaceAtomic(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := s.checkAcceptedFileCached(path, initial, observed); !errors.Is(err, errDurableStateChanged) {
		t.Fatalf("cached success ignored replaced file: %v", err)
	}
}

func TestM94WarmValidationDoesNotBypassFinalWriteCheck(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	path := s.snapshotPath(initial.Board.ID)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	damaged := bytes.Clone(original)
	damaged[0] = '!'
	_, err = s.update(initial.Board.ID, initial.Board.Revision, func(next *snapshot) error {
		if err := os.WriteFile(path, damaged, 0o600); err != nil {
			return err
		}
		if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
			return err
		}
		return m01Rename(next)
	})
	if !errors.Is(err, errInvalidSnapshot) || errors.Is(err, errWriteUncertain) {
		t.Fatalf("expected known final-check refusal, got %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, damaged) {
		t.Fatalf("refused write replaced external bytes: %v", err)
	}
	current, exists := s.accepted(initial.Board.ID)
	if !exists || current.Board.Revision != initial.Board.Revision || current.Board.Name != initial.Board.Name {
		t.Fatal("refused write changed accepted state")
	}
}

func TestM94ValidationCapacityAndRevision(t *testing.T) {
	var cache durableValidationCache
	digest := sha256.Sum256([]byte("snapshot"))
	for i := range durableValidationEntries + 1 {
		cache.put(durableValidation{fmt.Sprint(i), 1, digest, nil})
	}
	if _, found := cache.get("0", 1, digest); found {
		t.Fatal("oldest validation exceeded the fixed entry budget")
	}
	for range durableValidationEntries * 2 {
		cache.put(durableValidation{"1", 2, digest, errDurableStateChanged})
	}
	if _, found := cache.get("1", 1, digest); found {
		t.Fatal("accepted revision change reused an old validation")
	}
	if _, found := cache.get("2", 1, digest); !found {
		t.Fatal("repeated changes to one board displaced another entry")
	}
}

func TestM94ConcurrentCatalogReadsAndCommit(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 20 {
				row, visible := s.boardSummary(initial.Board.ID, m01HumanID)
				if !visible || row.Availability != "open" || row.ErrorCode != nil {
					t.Error("concurrent catalog read lost a healthy board")
				}
			}
		})
	}
	next, err := s.update(initial.Board.ID, initial.Board.Revision, m01Rename)
	readers.Wait()
	if err != nil {
		t.Fatal(err)
	}
	row, visible := s.boardSummary(initial.Board.ID, m01HumanID)
	if !visible || row.Availability != "open" || row.Revision == nil || *row.Revision != next.Board.Revision {
		t.Fatal("catalog did not validate the newly committed revision")
	}
	data, err := os.ReadFile(s.snapshotPath(initial.Board.ID))
	if err != nil {
		t.Fatal(err)
	}
	if result, found := s.durableValidation.get(initial.Board.ID, next.Board.Revision, sha256.Sum256(data)); !found || result != nil {
		t.Fatal("catalog did not retain its validated current representation")
	}
}

func TestM94CommitRequiresDurableComparison(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	id := initial.Board.ID
	_, err := s.update(id, initial.Board.Revision, func(next *snapshot) error {
		if err := m01Rename(next); err != nil {
			return err
		}
		// Use a historical event so the new board timestamp and recovery
		// reference remain canonical. Native encoding normalizes the event's
		// structurally valid zero-offset location before durable comparison.
		event := &next.Activity[0]
		event.At = event.At.In(time.FixedZone("named zero offset", 0))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		row, visible := s.boardSummary(id, m01HumanID)
		if !visible || row.Availability != "unhealthy" || row.ErrorCode == nil || *row.ErrorCode != "durable_state_changed" {
			t.Fatal("commit preparation bypassed the accepted-state comparison")
		}
	}
}

func BenchmarkM94DurableValidation(b *testing.B) {
	state, _ := m01Fixture()
	for i := range 60 {
		actor := m01HumanID
		if i%2 == 1 {
			actor = m01BotID
		}
		in := cardCreation{id: newID(), boardID: state.Board.ID, laneID: state.Lanes[0].ID,
			title: fmt.Sprintf("Representative card %d", i), description: string(bytes.Repeat([]byte("work "), 200))}
		if err := in.apply(&state, actor); err != nil {
			b.Fatal(err)
		}
		state.Board.Revision++
		state.Board.UpdatedAt = state.Activity[len(state.Activity)-1].At
	}
	state.Board.Attention, state.Board.LastCardEditAt = deriveAttention(state)
	data, err := marshalCanonical(state)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := parseSnapshot(data, state.Board.ID); err != nil {
		b.Fatal(err)
	}
	path := filepath.Join(b.TempDir(), state.Board.ID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.Fatal(err)
	}
	for _, cached := range []bool{false, true} {
		b.Run(fmt.Sprintf("cached=%v", cached), func(b *testing.B) {
			var s store
			file, err := readSnapshotFile(path)
			if err != nil {
				b.Fatal(err)
			}
			if err := s.checkAcceptedFileCached(path, state, file); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for b.Loop() {
				file, err := readSnapshotFile(path)
				if err == nil && cached {
					err = s.checkAcceptedFileCached(path, state, file)
				} else if err == nil {
					err = checkAcceptedSnapshotFile(path, state, file)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
