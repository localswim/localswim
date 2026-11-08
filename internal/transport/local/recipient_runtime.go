package local

import (
	"context"
	"errors"
	"github.com/localswim/localswim/internal/board/noticemonitor"
	R "github.com/localswim/localswim/internal/board/recipient"
	A "github.com/localswim/localswim/internal/contract/archive"
	"io/fs"
	"path/filepath"
	"strings"
)

const recipientStateName = "recipient-policy.json"

func recipientRuntimeEntry(name string) bool {
	if name == recipientStateName {
		return true
	}
	tail, ok := strings.CutPrefix(name, recipientStateName+".")
	id, suffix := strings.CutSuffix(tail, ".pending")
	return ok && suffix && uuidV4(id)
}

type recipientNativeState struct {
	path    string
	verify  func() error
	cleanup []WorkspaceCleanup
}

func (s *recipientNativeState) retain(err error) error {
	var c WorkspaceCleanup
	if errors.As(err, &c) {
		s.cleanup = append(s.cleanup, c)
	}
	return R.ErrStorage
}
func (s *recipientNativeState) read(limit int64) ([]byte, error) {
	if e := s.verify(); e != nil {
		return nil, e
	}
	b, e := workspaceRead(s.path, limit)
	if e != nil {
		if errors.Is(e, fs.ErrNotExist) {
			return nil, e
		}
		return nil, s.retain(e)
	}
	if e = s.verify(); e != nil {
		return nil, e
	}
	return b, nil
}
func (s *recipientNativeState) write(b []byte) error {
	if e := s.verify(); e != nil {
		return e
	}
	if len(b) == 0 || len(b) > R.MaxRecordBytes {
		return R.ErrStorage
	}
	if e := recipientPublish(s.path, b); e != nil {
		return s.retain(e)
	}
	return s.verify()
}
func (s *recipientNativeState) close() error {
	remaining := s.cleanup[:0]
	for _, c := range s.cleanup {
		if c.Close() != nil {
			remaining = append(remaining, c)
		}
	}
	s.cleanup = remaining
	if len(remaining) > 0 {
		return R.ErrStorage
	}
	return nil
}
func (r *Runtime) openRecipient(rootKey func() (string, error)) (*R.Store, error) {
	root, e := rootKey()
	db, de := r.store.DatabaseFileKey()
	binding := R.Binding{RootKey: root, DatabaseKey: db, InstallationID: A.UUID(r.id.InstallationID), BoardID: A.UUID(r.id.BoardID), Incarnation: A.UUID(r.id.Incarnation)}
	verify := func() error {
		if e != nil || de != nil {
			return R.ErrStorage
		}
		rk, re := rootKey()
		dk, dbe := r.store.DatabaseFileKey()
		if re != nil || dbe != nil {
			return R.ErrStorage
		}
		if rk != root || dk != db || !r.validateIdentity() {
			return R.ErrForeign
		}
		return nil
	}
	s := &recipientNativeState{path: filepath.Join(r.root, recipientStateName), verify: verify}
	r.recipientState = s
	return R.New(binding, R.Backend{Read: s.read, Write: s.write, Close: s.close})
}
func (r *Runtime) recipientCheck(auth noticemonitor.Authority) func(context.Context) error {
	return func(ctx context.Context) error {
		if e := r.checkNoticeAuthority(ctx, auth, nil); e != nil {
			return e
		}
		return r.recipientState.verify()
	}
}
