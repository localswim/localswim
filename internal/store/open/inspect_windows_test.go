//go:build windows && amd64

package open

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestInspectExistingReadOnlyAndPinned(t *testing.T) {
	s, p := newStore(t)
	withWriter(t, s, func(c *sql.Conn) { execute(t, c, "INSERT INTO blob VALUES(?,?,0,?)", incarnation, hash, []byte{}) })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := legacyFiles(t, p)
	var retained InspectionRead
	var retainedRows *sql.Rows
	err := InspectExisting(ctx(t), p, identity, func(r InspectionRead) error {
		retained = r
		var revision, blobs, queryOnly int
		if err := r.Scan("SELECT revision,(SELECT count(*) FROM blob),(SELECT query_only FROM pragma_query_only) FROM board", nil, &revision, &blobs, &queryOnly); err != nil {
			return err
		}
		if revision != 0 || blobs != 1 || queryOnly != 1 {
			t.Fatal("inspection altered state or settings")
		}
		for _, q := range []string{"UPDATE board SET revision=1 RETURNING revision", "DELETE FROM blob RETURNING sha256", "PRAGMA query_only=OFF", "SELECT 1; ATTACH DATABASE 'must-not-exist' AS x"} {
			if rows, err := r.Query(q); err == nil {
				rows.Close()
				t.Fatal("inspection admitted mutation")
			}
		}
		if other, err := Existing(ctx(t), p, identity); err == nil {
			other.Close()
			t.Fatal("ordinary owner entered inspection")
		}
		if err := InspectExisting(ctx(t), p, identity, func(InspectionRead) error { return nil }); err == nil {
			t.Fatal("second inspector entered")
		}
		for _, name := range []string{"board.sqlite", "board.lock"} {
			path := filepath.Join(p, name)
			if err := windows.DeleteFile(windows.StringToUTF16Ptr(path)); err == nil {
				t.Fatal("inspection did not pin file against actual deletion", name)
			}
			if err := windows.MoveFileEx(windows.StringToUTF16Ptr(path), windows.StringToUTF16Ptr(path+".moved"), 0); err == nil {
				t.Fatal("inspection did not pin file against rename", name)
			}
		}
		var err error
		retainedRows, err = r.Query("SELECT sha256 FROM blob")
		return err // The facade owns cleanup even if a caller leaves rows open.
	})
	if err != nil {
		t.Fatal(err)
	}
	if retainedRows == nil || retainedRows.Next() {
		t.Fatal("rows escaped inspection lifetime")
	}
	if err := retained.Scan("SELECT 1", nil, new(int)); !errors.Is(err, ErrClosed) {
		t.Fatal("reader escaped inspection lifetime", err)
	}
	if !reflect.DeepEqual(before, legacyFiles(t, p)) {
		t.Fatal("inspection mutated a file or inventory")
	}
	// Releasing inspection also releases real exclusion and file pins.
	if err := InspectExisting(ctx(t), p, identity, func(r InspectionRead) error { return r.Scan("SELECT count(*) FROM blob", nil, new(int)) }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, legacyFiles(t, p)) {
		t.Fatal("repeated inspection changed orphan bytes")
	}
}

func TestInspectExistingMissingFilesNeverCreated(t *testing.T) {
	for _, missing := range []string{"board.lock", "board.sqlite"} {
		t.Run(missing, func(t *testing.T) {
			s, p := newStore(t)
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(p, missing)); err != nil {
				t.Fatal(err)
			}
			before := legacyFiles(t, p)
			for i := 0; i < 3; i++ {
				if err := InspectExisting(ctx(t), p, identity, func(InspectionRead) error { t.Fatal("missing file reached callback"); return nil }); err == nil {
					t.Fatal("missing file accepted")
				}
				if !reflect.DeepEqual(before, legacyFiles(t, p)) {
					t.Fatal("inspection created or mutated a file")
				}
			}
			// Concurrent inspectors must not turn an absent exclusion/data file
			// into a newly created one while racing for native ownership.
			inspectionCtx := ctx(t)
			var callbacks atomic.Int32
			var workers sync.WaitGroup
			results := make(chan error, 8)
			start := make(chan struct{})
			for i := 0; i < cap(results); i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					results <- InspectExisting(inspectionCtx, p, identity, func(InspectionRead) error { callbacks.Add(1); return nil })
				}()
			}
			close(start)
			workers.Wait()
			close(results)
			for err := range results {
				if err == nil {
					t.Fatal("racing missing-file inspector admitted")
				}
			}
			if callbacks.Load() != 0 || !reflect.DeepEqual(before, legacyFiles(t, p)) {
				t.Fatal("racing inspectors created/mutated a file or called back")
			}
		})
	}
}

func TestInspectExistingErrorsAndCancellationPreserveFiles(t *testing.T) {
	for _, mode := range []string{"callback-error", "cancel-before", "cancel-during", "audit-error", "unknown-file", "unknown-version"} {
		t.Run(mode, func(t *testing.T) {
			s, p := newStore(t)
			if mode == "audit-error" {
				withWriter(t, s, func(c *sql.Conn) { execute(t, c, "DROP INDEX coverage_gap_complete_tuple") })
			}
			if mode == "unknown-version" {
				withWriter(t, s, func(c *sql.Conn) { execute(t, c, "PRAGMA user_version=3") })
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if mode == "unknown-file" {
				if err := os.WriteFile(filepath.Join(p, "board.sqlite-journal"), []byte("synthetic refused sidecar"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := legacyFiles(t, p)
			inspectionCtx, cancel := context.WithCancel(ctx(t))
			defer cancel()
			if mode == "cancel-before" {
				cancel()
			}
			called := false
			err := InspectExisting(inspectionCtx, p, identity, func(r InspectionRead) error {
				called = true
				if mode == "callback-error" {
					return errors.New("synthetic callback refusal")
				}
				if mode == "cancel-during" {
					cancel()
					return r.Scan("SELECT revision FROM board", nil, new(int))
				}
				t.Fatal("failed audit/cancellation reached callback")
				return nil
			})
			if err == nil {
				t.Fatal("inspection failure was lost")
			}
			if called != (mode == "callback-error" || mode == "cancel-during") {
				t.Fatal("callback ran before successful audit")
			}
			if !reflect.DeepEqual(before, legacyFiles(t, p)) {
				t.Fatal("failed inspection changed bytes or inventory")
			}
		})
	}
}

func TestInspectExistingCancellationExpiresPendingCallback(t *testing.T) {
	s, p := newStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before := legacyFiles(t, p)
	inspectionCtx, cancel := context.WithCancel(ctx(t))
	defer cancel()
	err := InspectExisting(inspectionCtx, p, identity, func(r InspectionRead) error {
		cancel()
		until := time.Now().Add(time.Second)
		for time.Now().Before(until) {
			if err := r.Scan("SELECT revision FROM board", nil, new(int)); errors.Is(err, ErrClosed) {
				return nil
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("pending callback retained a live inspection facet after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation result lost", err)
	}
	if !reflect.DeepEqual(before, legacyFiles(t, p)) {
		t.Fatal("canceled pending callback changed files")
	}
}
