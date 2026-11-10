package v2

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestM01DurableReadRejectsReplacedDirectory(t *testing.T) {
	s, initial := m01CreateBoard(t, t.TempDir())
	id := initial.Board.ID
	path := s.snapshotPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(path)
	if err := os.Rename(directory, directory+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSnapshot(path, id); err != nil {
		t.Fatalf("replacement fixture must contain a valid native snapshot: %v", err)
	}
	if _, err := s.durable(id); !errors.Is(err, errInvalidSnapshot) {
		t.Fatalf("durable read should reject the replacement directory, got %v", err)
	}
}
