// Package recipient owns a fixed private preference. It creates no delivery work.
package recipient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sync"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const MaxInputBytes = 1024
const MaxRecordBytes = 4096

var (
	ErrBusy       = errors.New("recipient busy")
	ErrClosed     = errors.New("recipient closed")
	ErrStale      = errors.New("recipient stale")
	ErrFixed      = errors.New("recipient fixed")
	ErrInvalid    = errors.New("recipient invalid")
	ErrStorage    = errors.New("recipient storage")
	ErrForeign    = errors.New("recipient foreign")
	ErrUnknown    = errors.New("recipient outcome unknown")
	threadPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	v4Pattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	filePattern   = regexp.MustCompile(`^[0-9a-f]{8}:[0-9a-f]{16}$`)
)

type Reference struct {
	Kind     string `json:"kind"`
	ThreadID string `json:"threadId"`
}
type Input struct {
	Version   A.Version `json:"version"`
	Recipient Reference `json:"recipient"`
	Policy    string    `json:"policy"`
}

func ValidInput(v Input) bool {
	b, e := json.Marshal(v)
	return e == nil && len(b) <= MaxInputBytes && v.Version == 1 && v.Recipient.Kind == "codex-thread" && threadPattern.MatchString(v.Recipient.ThreadID) && v.Policy == "conservative-metadata-v1"
}
func DecodeInput(b []byte) (Input, error) {
	v, e := A.Decode[Input](b)
	if e != nil || len(b) > MaxInputBytes || !ValidInput(v) {
		return Input{}, ErrInvalid
	}
	return v, nil
}
func ValidV4(v string) bool { return v4Pattern.MatchString(v) }

type Binding struct {
	RootKey        string `json:"rootKey"`
	DatabaseKey    string `json:"databaseKey"`
	InstallationID A.UUID `json:"installationId"`
	BoardID        A.UUID `json:"boardId"`
	Incarnation    A.UUID `json:"incarnation"`
}

func (b Binding) valid() bool {
	return filePattern.MatchString(b.RootKey) && filePattern.MatchString(b.DatabaseKey) && b.RootKey[:8] == b.DatabaseKey[:8] && b.RootKey != b.DatabaseKey && b.RootKey[9:] != "0000000000000000" && b.DatabaseKey[9:] != "0000000000000000" && ValidV4(string(b.InstallationID)) && A.Validate(b.BoardID) == nil && A.Validate(b.Incarnation) == nil && b.InstallationID != b.BoardID && b.InstallationID != b.Incarnation && b.BoardID != b.Incarnation
}

type Record struct {
	Version                 A.Version `json:"version"`
	Binding                 Binding   `json:"binding"`
	ConfigurationRevision   A.Number  `json:"configurationRevision"`
	ConfigurationGeneration A.UUID    `json:"configurationGeneration"`
	RequestID               A.UUID    `json:"requestId"`
	Input                   Input     `json:"input"`
}

func decode(b []byte, binding Binding) (*Record, error) {
	d, e := A.Decode[Record](b)
	if e != nil || len(b) > MaxRecordBytes || d.Version != 1 || !d.Binding.valid() || d.ConfigurationRevision != 1 || !ValidV4(string(d.ConfigurationGeneration)) || !ValidV4(string(d.RequestID)) || !ValidInput(d.Input) {
		return nil, ErrStorage
	}
	if d.Binding != binding {
		return nil, ErrForeign
	}
	return &d, nil
}

type View struct {
	RuntimeGeneration A.UUID
	State             string
	Reason            string
	Record            *Record
}

// Backend callbacks execute only while the operation reservation is owned.
// Check includes fresh authority AND physical identity; Write must create once.
type Backend struct {
	Read  func(int64) ([]byte, error)
	Write func([]byte) error
	Close func() error
}
type Store struct {
	closeMu               sync.Mutex
	mu                    sync.Mutex
	busy, closing, closed bool
	idle                  chan struct{}
	binding               Binding
	backend               Backend
	generation            A.UUID
	seen                  bool
	observed              bool
	digest                []byte
	attempted             []byte
	guarded               bool
	failed                string
}

// RuntimeGeneration is immutable for this store's lifetime and discloses no policy.
func (s *Store) RuntimeGeneration() A.UUID { return s.generation }

func New(binding Binding, backend Backend) (*Store, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return nil, e
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	id := A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
	s := &Store{binding: binding, backend: backend, generation: id}
	if !binding.valid() || backend.Read == nil || backend.Write == nil {
		s.failed = "storage"
	}
	return s, nil
}
func (s *Store) reserve() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.closed {
		return ErrClosed
	}
	if s.busy {
		return ErrBusy
	}
	s.busy = true
	s.idle = make(chan struct{})
	return nil
}
func (s *Store) release() { s.mu.Lock(); s.busy = false; close(s.idle); s.mu.Unlock() }
func (s *Store) view(state, reason string, d *Record) View {
	var copy *Record
	if d != nil {
		x := *d
		copy = &x
	}
	return View{s.generation, state, reason, copy}
}
func (s *Store) inspect() View {
	if s.failed != "" {
		return s.view("unavailable", s.failed, nil)
	}
	b, e := s.backend.Read(MaxRecordBytes)
	wasSeen := s.seen
	s.seen = true
	if errors.Is(e, fs.ErrNotExist) {
		if s.observed {
			s.failed = "storage"
			return s.view("unavailable", "storage", nil)
		}
		if s.guarded {
			return s.view("unknown", "unknown", nil)
		}
		return s.view("unconfigured", "", nil)
	}
	if e != nil {
		s.failed = "storage"
		return s.view("unavailable", "storage", nil)
	}
	// Out-of-band changes never authorize a replacement, including appearance
	// after this engine previously observed an absent file.
	if wasSeen && s.digest == nil && !bytes.Equal(b, s.attempted) {
		s.failed = "storage"
		return s.view("unavailable", "storage", nil)
	}
	if s.digest != nil && !bytes.Equal(b, s.digest) && !bytes.Equal(b, s.attempted) {
		s.failed = "storage"
		return s.view("unavailable", "storage", nil)
	}
	d, e := decode(b, s.binding)
	if e != nil {
		s.failed = "storage"
		if errors.Is(e, ErrForeign) {
			s.failed = "foreign"
		}
		return s.view("unavailable", s.failed, nil)
	}
	if s.guarded && !bytes.Equal(b, s.attempted) {
		return s.view("unknown", "unknown", nil)
	}
	s.observed = true
	s.digest = bytes.Clone(b)
	return s.view("configured", "", d)
}
func (s *Store) Status(ctx context.Context, check func(context.Context) error) (View, error) {
	if ctx == nil || check == nil {
		return View{}, ErrInvalid
	}
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	if e := s.reserve(); e != nil {
		return View{}, e
	}
	defer s.release()
	if e := check(ctx); e != nil {
		return View{}, e
	}
	v := s.inspect()
	if e := check(ctx); e != nil {
		return View{}, e
	}
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	return v, nil
}
func (s *Store) Configure(ctx context.Context, generation A.UUID, revision A.Number, request A.UUID, input Input, check func(context.Context) error) (View, error) {
	if ctx == nil || check == nil {
		return View{}, ErrInvalid
	}
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	if e := s.reserve(); e != nil {
		return View{}, e
	}
	defer s.release()
	if e := check(ctx); e != nil {
		return View{}, e
	}
	if !ValidInput(input) || !ValidV4(string(request)) {
		return View{}, ErrInvalid
	}
	if generation != s.generation || revision != 0 {
		return View{}, ErrStale
	}
	v := s.inspect()
	if v.State == "configured" {
		return View{}, ErrFixed
	}
	if s.guarded || v.State != "unconfigured" {
		return View{}, ErrStorage
	}
	var random [16]byte
	if _, e := rand.Read(random[:]); e != nil {
		return View{}, ErrStorage
	}
	random[6] = random[6]&15 | 64
	random[8] = random[8]&63 | 128
	d := Record{1, s.binding, 1, A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", random[:4], random[4:6], random[6:8], random[8:10], random[10:])), request, input}
	b, e := json.Marshal(d)
	if e != nil || len(b) > MaxRecordBytes {
		return View{}, ErrStorage
	}
	// This is the last pre-admission check. Guard BEFORE any native staging.
	if e = check(ctx); e != nil {
		return View{}, e
	}
	if ctx.Err() != nil {
		return View{}, ctx.Err()
	}
	s.attempted = bytes.Clone(b)
	s.guarded = true
	if e = s.backend.Write(b); e != nil {
		return View{}, ErrUnknown
	}
	if e = check(ctx); e != nil || ctx.Err() != nil {
		return View{}, ErrUnknown
	}
	s.observed = true
	s.digest = bytes.Clone(b)
	return s.view("configured", "", &d), nil
}

// Close fences new operations and joins the current reservation before cleanup.
// A cleanup failure leaves the store fenced and native lease retained by host.
func (s *Store) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil
	}
	s.mu.Lock()
	s.closing = true
	wait := s.idle
	busy := s.busy
	s.mu.Unlock()
	if busy {
		<-wait
	}
	if s.backend.Close != nil {
		if e := s.backend.Close(); e != nil {
			return e
		}
	}
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}
