package recipient

import (
	"bytes"
	"context"
	"errors"
	A "github.com/localswim/localswim/internal/contract/archive"
	"io/fs"
	"sync/atomic"
	"testing"
	"time"
)

var testBinding = Binding{"12345678:0000000000000001", "12345678:0000000000000002", "00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"}
var testInput = Input{1, Reference{"codex-thread", "00000000-0000-7000-8000-000000000016"}, "conservative-metadata-v1"}

const testRequest A.UUID = "00000000-0000-4000-8000-000000000004"

func allow(context.Context) error { return nil }
func testStore(t *testing.T, b Backend) *Store {
	t.Helper()
	s, e := New(testBinding, b)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestRecipientFixedReopenConservation(t *testing.T) {
	var saved []byte
	writes := 0
	b := Backend{Read: func(int64) ([]byte, error) {
		if saved == nil {
			return nil, fs.ErrNotExist
		}
		return bytes.Clone(saved), nil
	}, Write: func(v []byte) error { writes++; saved = bytes.Clone(v); return nil }}
	s := testStore(t, b)
	initial, e := s.Status(context.Background(), allow)
	if e != nil || initial.State != "unconfigured" {
		t.Fatal("initial")
	}
	got, e := s.Configure(context.Background(), initial.RuntimeGeneration, 0, testRequest, testInput, allow)
	if e != nil || got.Record == nil || got.Record.ConfigurationRevision != 1 {
		t.Fatal("save", e)
	}
	for _, in := range []Input{testInput, {1, Reference{"codex-thread", "00000000-0000-4000-8000-000000000017"}, testInput.Policy}} {
		if _, e = s.Configure(context.Background(), initial.RuntimeGeneration, 0, testRequest, in, allow); !errors.Is(e, ErrFixed) {
			t.Fatal("fresh request not fixed", e)
		}
	}
	reopened := testStore(t, b)
	v, e := reopened.Status(context.Background(), allow)
	if e != nil || v.Record == nil || *v.Record != *got.Record || v.RuntimeGeneration == got.RuntimeGeneration || writes != 1 {
		t.Fatal("reopen/correlation")
	}
}
func TestRecipientAllReadWriteReservationsAndClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var reads atomic.Int32
	s := testStore(t, Backend{Read: func(int64) ([]byte, error) { reads.Add(1); close(entered); <-release; return nil, fs.ErrNotExist }, Write: func([]byte) error { return nil }})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.Status(context.Background(), allow) }()
	<-entered
	start := time.Now()
	if _, e := s.Status(context.Background(), allow); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if _, e := s.Configure(context.Background(), s.generation, 0, testRequest, testInput, allow); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	if time.Since(start) > time.Second || reads.Load() != 1 {
		t.Fatal("queued admission")
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-closed:
		t.Fatal("close released active backend")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-done
	if e := <-closed; e != nil {
		t.Fatal(e)
	}
	if _, e := s.Status(context.Background(), allow); !errors.Is(e, ErrClosed) {
		t.Fatal(e)
	}
}
func TestRecipientWriteBoundaryUnknownAndExactInspection(t *testing.T) {
	for _, publish := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-publication", true: "after-publication"}[publish], func(t *testing.T) {
			var data []byte
			writes := 0
			s := testStore(t, Backend{Read: func(int64) ([]byte, error) {
				if data == nil {
					return nil, fs.ErrNotExist
				}
				return data, nil
			}, Write: func(b []byte) error {
				writes++
				if publish {
					data = bytes.Clone(b)
				}
				return ErrStorage
			}})
			_, e := s.Configure(context.Background(), s.generation, 0, testRequest, testInput, allow)
			if !errors.Is(e, ErrUnknown) || !s.guarded || len(s.attempted) == 0 {
				t.Fatal("not guarded unknown")
			}
			v, e := s.Status(context.Background(), allow)
			if e != nil || publish && v.State != "configured" || !publish && v.State != "unknown" {
				t.Fatal("inspection", v.State, e)
			}
			if _, e = s.Configure(context.Background(), s.generation, 0, testRequest, testInput, allow); e == nil || writes != 1 {
				t.Fatal("replayed")
			}
		})
	}
}
func TestRecipientPreAndPostAdmissionAuthority(t *testing.T) {
	for _, denyAt := range []int{1, 2, 3} {
		t.Run(string(rune('0'+denyAt)), func(t *testing.T) {
			calls, writes := 0, 0
			s := testStore(t, Backend{Read: func(int64) ([]byte, error) { return nil, fs.ErrNotExist }, Write: func([]byte) error { writes++; return nil }})
			denied := errors.New("authority")
			check := func(context.Context) error {
				calls++
				if calls == denyAt {
					return denied
				}
				return nil
			}
			_, e := s.Configure(context.Background(), s.generation, 0, testRequest, testInput, check)
			if denyAt < 3 {
				if !errors.Is(e, denied) || writes != 0 || s.guarded {
					t.Fatal("preadmission")
				}
			} else if !errors.Is(e, ErrUnknown) || writes != 1 || !s.guarded {
				t.Fatal("late authority")
			}
		})
	}
}
func TestRecipientObservedChangeAndUnavailable(t *testing.T) {
	var data []byte
	s := testStore(t, Backend{Read: func(int64) ([]byte, error) {
		if data == nil {
			return nil, fs.ErrNotExist
		}
		return data, nil
	}, Write: func(b []byte) error { data = bytes.Clone(b); return nil }})
	v, e := s.Configure(context.Background(), s.generation, 0, testRequest, testInput, allow)
	if e != nil {
		t.Fatal(e)
	}
	data = nil
	v, e = s.Status(context.Background(), allow)
	if e != nil || v.State != "unavailable" || v.Record != nil {
		t.Fatal("lost observed became empty")
	}
	for _, b := range [][]byte{[]byte(`{}`), bytes.Repeat([]byte("x"), 4097)} {
		s = testStore(t, Backend{Read: func(int64) ([]byte, error) { return b, nil }, Write: func([]byte) error { t.Fatal("repair"); return nil }})
		v, e = s.Status(context.Background(), allow)
		if e != nil || v.State != "unavailable" || v.Record != nil {
			t.Fatal("bad state")
		}
	}
}
func TestRecipientCleanupRetainsFence(t *testing.T) {
	fail := true
	s := testStore(t, Backend{Read: func(int64) ([]byte, error) { return nil, fs.ErrNotExist }, Write: func([]byte) error { return nil }, Close: func() error {
		if fail {
			return ErrStorage
		}
		return nil
	}})
	if s.Close() == nil {
		t.Fatal("false joined close")
	}
	if _, e := s.Status(context.Background(), allow); !errors.Is(e, ErrClosed) {
		t.Fatal("admission after failed close")
	}
	fail = false
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
}
