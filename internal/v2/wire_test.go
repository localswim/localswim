package v2

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestCanonicalTimestampRoundTrip(t *testing.T) {
	input := time.Date(2026, 11, 10, 9, 44, 56, 987654321, time.FixedZone("offset", -5*60*60))
	data, err := marshalCanonical(struct {
		At time.Time `json:"at"`
	}{input})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"at":"2026-11-10 14:44:56+00:00"}` {
		t.Fatalf("unexpected timestamp: %s", data)
	}
	var got struct {
		At time.Time `json:"at"`
	}
	if err := unmarshalCanonical(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.At != canonicalTime(input) {
		t.Fatalf("decoded time = %v", got.At)
	}
	if !validSnapshotTime(got.At) || validSnapshotTime(input) {
		t.Fatal("domain time validation must require UTC seconds")
	}
	if validSnapshotTime(got.At.Add(time.Nanosecond)) {
		t.Fatal("fractional UTC timestamp accepted")
	}
	report := snapshotVerification(m01BoardID, input, snapshot{}, errors.New("unavailable"))
	if report.At != "2026-11-10 14:44:56+00:00" {
		t.Fatalf("verification time = %s", report.At)
	}
}

func TestNativeSnapshotTimestampContract(t *testing.T) {
	value := nativeVerificationFixture()
	data := marshalVerificationFixture(t, value)
	loaded, err := parseSnapshot(data, value.Board.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Board.CreatedAt != value.Board.CreatedAt || !validSnapshotTime(value.Activity[0].At) {
		t.Fatal("snapshot timestamps did not round-trip as UTC seconds")
	}
	old := value.Board.CreatedAt.Format(timestampLayout)
	for _, invalid := range []string{
		"2026-11-10T16:00:00Z", "2026-11-10T16:00:00.123456Z",
		"2026-11-10 16:00:00Z", "2026-11-10 16:00:00.1+00:00",
		"2026-11-10 16:00:00+01:00", "2026-11-10 16:00:00-00:00",
		"2026-11-10 6:00:00+00:00", "2026-02-30 16:00:00+00:00",
	} {
		t.Run(invalid, func(t *testing.T) {
			damaged := bytes.Replace(data, []byte(old), []byte(invalid), 1)
			if _, err := parseSnapshot(damaged, value.Board.ID); !errors.Is(err, errInvalidSnapshot) {
				t.Fatalf("invalid timestamp error = %v", err)
			}
		})
	}
}

func TestSameSecondMutationsAdvanceRevisions(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	at := canonicalTime(time.Now())
	current := initial
	for i := range 3 {
		next, err := s.update(m01BoardID, current.Board.ETag, func(value *snapshot) error {
			field, before, after := "name", value.Board.Name, fmt.Sprintf("Rename %d", i)
			value.Board.Name = after
			value.Activity = append(value.Activity, activity{
				ID: newOperationID(), BoardID: m01BoardID, Revision: value.Board.Revision + 1,
				At: at, ActorID: m01HumanID, Operation: "patch", ResourceType: "board", ResourceID: m01BoardID,
				Field: &field, Before: &before, After: &after,
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if next.Board.Revision != current.Board.Revision+1 || next.Board.ETag == current.Board.ETag {
			t.Fatal("same-second mutation did not advance revision and ETag")
		}
		if next.Board.UpdatedAt != at {
			t.Fatalf("update time = %v, want %v", next.Board.UpdatedAt, at)
		}
		current = next
	}
	loaded, err := readSnapshot(s.snapshotPath(m01BoardID), m01BoardID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Board.Revision != current.Board.Revision {
		t.Fatal("durable revision did not survive round-trip")
	}
}
