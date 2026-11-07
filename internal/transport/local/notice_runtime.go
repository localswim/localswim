package local

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/noticeinterval"
	"github.com/localswim/localswim/internal/board/noticemonitor"
	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/boardapi"
)

const noticeStateName = "notice-runtime.json"

func noticeRuntimeEntry(name string) bool {
	if name == noticeStateName {
		return true
	}
	tail, ok := strings.CutPrefix(name, noticeStateName+".")
	staging, suffix := strings.CutSuffix(tail, ".pending")
	return ok && suffix && uuidV4(staging)
}

func (r *Runtime) noticeAuthority(mode string, token T.SecretToken) noticemonitor.Authority {
	actor, grant, kind := r.id.BotID, r.id.BotGrant, "automation"
	switch mode {
	case "browser":
		actor, grant, kind = r.id.HumanID, r.id.HumanGrant, "human"
	case "cli":
	default:
		return nil
	}
	admission, err := grants.New(r.id.InstallationID, r.Origin(), func(ctx context.Context, rd writer.Read, now time.Time) (grants.Credential, error) {
		return r.auth.verifyCredential(mode, token, now, false)
	})
	if err != nil {
		return nil
	}
	return func(ctx context.Context, rd writer.Read) error {
		if err := admission.RequireCurrent(ctx, rd, r.id.BoardID, r.id.Incarnation, actor, mode, grant, "read"); err != nil {
			return err
		}
		var installed int
		if err := rd.Scan("SELECT count(*) FROM actor WHERE incarnation=? AND actor_id=? AND kind=? AND origin_board=? AND origin_incarnation=?",
			[]any{r.id.Incarnation, actor, kind, r.id.BoardID, r.id.Incarnation}, &installed); err != nil {
			return err
		}
		if installed != 1 {
			return grants.ErrDenied
		}
		return nil
	}
}

// checkNoticeAuthority uses the same selected read admission as the interval
// source while remaining independent of cut metadata or sidecar readability.
// The engine request or monitor borrow must remain held across this check.
func (r *Runtime) checkNoticeAuthority(ctx context.Context, authority noticemonitor.Authority, retained *notices.State) error {
	if ctx == nil || ctx.Err() != nil || authority == nil || !r.validateIdentity() {
		return grants.ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lease, err := r.store.Reader(ctx)
	if err != nil {
		return boardapi.ErrUnavailable
	}
	err = lease.Do(ctx, func(c *sql.Conn) error {
		if _, err := c.ExecContext(ctx, "BEGIN"); err != nil {
			return err
		}
		rd := snapshot{ctx, c}
		var revision A.Number
		var next A.Positive
		if retained != nil {
			if err := rd.Scan("SELECT revision,next_event FROM board WHERE singleton=1 AND board_id=? AND incarnation=?",
				[]any{r.id.BoardID, r.id.Incarnation}, &revision, &next); err != nil {
				return err
			}
		}
		if err := authority(ctx, rd); err != nil {
			return err
		}
		if retained == nil {
			return nil
		}
		if A.Validate(revision) != nil || A.Validate(next) != nil {
			return boardapi.ErrUnavailable
		}
		lower := retained.Checkpoint
		if retained.Pending != nil && retained.Pending.Upper != nil {
			lower = *retained.Pending.Upper
		}
		if A.Number(next-1) < lower.Event || revision < lower.Revision {
			return notices.ErrForeign
		}
		// Only valid current coverage facts can establish a regression.
		// Broken coverage remains an evidence failure of the interval source
		// after admission, so it can still report an unavailable interval.
		var floor sql.NullString
		if err := rd.Scan("SELECT activity_floor FROM board WHERE singleton=1", nil, &floor); err == nil {
			at := A.Timestamp(floor.String)
			if (!floor.Valid || A.Validate(at) == nil) && at < lower.Floor {
				return notices.ErrForeign
			}
		}
		var gap A.Number
		var minimum A.Positive
		if err := rd.Scan("SELECT coalesce(max(gap_id),0),coalesce(min(gap_id),1) FROM coverage_gap WHERE incarnation=? AND origin_board=? AND origin_incarnation=? AND class='activity'",
			[]any{r.id.Incarnation, r.id.BoardID, r.id.Incarnation}, &gap, &minimum); err == nil &&
			A.Validate(gap) == nil && A.Validate(minimum) == nil && gap < lower.Gap {
			return notices.ErrForeign
		}
		return nil
	})
	closeErr := lease.Close()
	if errors.Is(err, grants.ErrDenied) || errors.Is(err, grants.ErrUnauthenticated) {
		return err
	}
	if closeErr != nil || ctx.Err() != nil {
		return boardapi.ErrUnavailable
	}
	if !r.validateIdentity() {
		return grants.ErrDenied
	}
	if errors.Is(err, notices.ErrForeign) {
		return notices.ErrForeign
	}
	if err != nil {
		return boardapi.ErrUnavailable
	}
	return nil
}

func (r *Runtime) openNotices(rootKey func() (string, error), boardDir string) (*noticemonitor.Monitor, error) {
	binding := notices.Binding{
		InstallationID: A.UUID(r.id.InstallationID),
		BoardID:        A.UUID(r.id.BoardID),
		Incarnation:    A.UUID(r.id.Incarnation),
	}
	nativeKeys := func() (root, db string, err error) {
		root, err = rootKey()
		if err != nil {
			return "", "", notices.ErrStorage
		}
		db, err = r.store.DatabaseFileKey()
		if err != nil {
			return "", "", notices.ErrStorage
		}
		return root, db, nil
	}
	root, db, nativeErr := nativeKeys()
	binding.RootKey, binding.DatabaseKey = root, db
	verify := func() error {
		root, db, err := nativeKeys()
		if err != nil {
			return err
		}
		if root != binding.RootKey || db != binding.DatabaseKey || !r.validateIdentity() {
			return notices.ErrForeign
		}
		return nil
	}
	selected := A.Identity{BoardID: binding.BoardID, Incarnation: binding.Incarnation}
	var store *notices.Store
	source := noticemonitor.Source{
		Check: func(ctx context.Context, auth noticemonitor.Authority) error {
			// Sample retained evidence before opening the reader. A later
			// monitor publication may rely on a newer board snapshot.
			if err := r.checkNoticeAuthority(ctx, auth, store.Snapshot()); err != nil {
				return err
			}
			// A monitor whose state could not open still permits current
			// admission for the scoped unavailable status.
			if nativeErr == nil {
				return verify()
			}
			return nil
		},
		Observe: func(ctx context.Context, auth noticemonitor.Authority) (noticeinterval.Cut, error) {
			if err := verify(); err != nil {
				return noticeinterval.Cut{}, err
			}
			cut, err := boardapi.ObserveNoticeCut(ctx, r.store, selected, auth)
			if err == nil {
				err = verify()
			}
			if err != nil {
				return noticeinterval.Cut{}, noticeSourceError(err)
			}
			return cut, nil
		},
		Capture: func(ctx context.Context, lower noticeinterval.Cut, auth noticemonitor.Authority) (noticeinterval.Evidence, error) {
			if err := verify(); err != nil {
				return noticeinterval.Evidence{}, err
			}
			evidence, err := boardapi.CaptureNoticeIntervalWithLabel(ctx, r.store, selected, A.UUID(r.id.BotID), lower, r.noticeLabel, auth)
			if err == nil {
				err = verify()
			}
			if err != nil {
				return noticeinterval.Evidence{}, noticeSourceError(err)
			}
			return evidence, nil
		},
		Signal: func() (string, error) {
			return noticeNativeSignal(boardDir, binding.DatabaseKey)
		},
	}
	if nativeErr != nil {
		return noticemonitor.NewUnavailable(source, nativeErr)
	}
	state := &noticeNativeState{
		path:    filepath.Join(r.root, noticeStateName),
		binding: binding,
		verify:  verify,
	}
	r.noticeState = state
	var err error
	store, err = notices.Open(binding, state.read, state.write)
	if err != nil {
		return noticemonitor.NewUnavailable(source, err)
	}
	return noticemonitor.New(store, source)
}

func noticeSourceError(err error) error {
	if errors.Is(err, boardapi.ErrNoticeForeign) {
		return notices.ErrForeign
	}
	return err
}

// Access is serialized by notices.Store after startup. Keep the last valid
// native publication, allowing a later exact lookup to inspect the bytes of
// the one attempted publication whose result was uncertain.
type noticeNativeState struct {
	path      string
	binding   notices.Binding
	verify    func() error
	seen      bool
	present   bool
	digest    [sha256.Size]byte
	attempted *[sha256.Size]byte
	cleanup   []WorkspaceCleanup
}

func (s *noticeNativeState) read(limit int64) ([]byte, error) {
	if err := s.verify(); err != nil {
		return nil, err
	}
	data, err := workspaceRead(s.path, limit)
	if errors.Is(err, fs.ErrNotExist) {
		if s.present {
			return nil, notices.ErrStorage
		}
		s.seen = true
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, s.retain(err)
	}
	if err := s.verify(); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(data)
	if s.seen && (!s.present || digest != s.digest) && (s.attempted == nil || digest != *s.attempted) {
		return nil, notices.ErrStorage
	}
	s.seen, s.present, s.digest = true, true, digest
	return data, nil
}

func (s *noticeNativeState) write(binding notices.Binding, data []byte) error {
	if binding != s.binding {
		return notices.ErrForeign
	}
	if len(data) == 0 || len(data) > notices.MaxStateBytes {
		return notices.ErrStorage
	}
	if _, err := s.read(notices.MaxStateBytes); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	digest := sha256.Sum256(data)
	if err := workspacePublish(s.path, data, s.present); err != nil {
		s.attempted = &digest
		return s.retain(err)
	}
	s.seen, s.present, s.digest, s.attempted = true, true, digest, nil
	return nil
}

func (s *noticeNativeState) retain(err error) error {
	var cleanup WorkspaceCleanup
	if errors.As(err, &cleanup) {
		s.cleanup = append(s.cleanup, cleanup)
	}
	return notices.ErrStorage
}

// close runs after the monitor has stopped calling the state backend.
func (s *noticeNativeState) close() error {
	remaining := s.cleanup[:0]
	for _, pending := range s.cleanup {
		if pending.Close() != nil {
			remaining = append(remaining, pending)
		}
	}
	s.cleanup = remaining
	if len(remaining) != 0 {
		return notices.ErrStorage
	}
	return nil
}
