package credentials

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
)

// nativeSnapshotRead exposes only single SELECTs on S01's query-only reader.
type nativeSnapshotRead struct{ access }

func nativeSelect(q string) bool {
	f := strings.Fields(q)
	return len(f) != 0 && strings.EqualFold(f[0], "SELECT") && !strings.ContainsAny(q, ";\x00")
}
func (r nativeSnapshotRead) Scan(q string, args []any, dest ...any) error {
	if !nativeSelect(q) {
		return errors.New("single SELECT required")
	}
	return r.access.Scan(q, args, dest...)
}
func (r nativeSnapshotRead) Query(q string, args ...any) (*sql.Rows, error) {
	if !nativeSelect(q) {
		return nil, errors.New("single SELECT required")
	}
	return r.access.Query(q, args...)
}

// WithNativeSnapshot freshly verifies the actual native owner before opening a
// bounded, query-only S01 snapshot. The callback may freeze input and resolve
// stored grant scope; it receives no command rights. It must close Rows, perform
// only bounded internal reads, and retain neither the seam nor snapshot. No
// external IO, other leases, nested lifecycle admission or goroutines are allowed.
// Callback errors survive successful cleanup; cleanup failure is unavailable.
func (s *Service) WithNativeSnapshot(ctx context.Context, token T.SecretToken, fn func(actorID string, r writer.Read) error) error {
	if s == nil || ctx == nil || ctx.Err() != nil || fn == nil || !s.mu.TryRLock() {
		return ErrUnavailable
	}
	defer s.mu.RUnlock()
	if s.closed || s.lease == nil || s.provider == nil || s.store == nil || s.now == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	n, err := s.native(token, s.now().UTC())
	if err != nil {
		return ErrDenied
	}
	l, err := s.store.Reader(ctx)
	if err != nil {
		return ErrUnavailable
	}
	var callbackErr error
	err = l.Do(ctx, func(c *sql.Conn) (e error) {
		defer func() {
			if recover() != nil {
				e = ErrUnavailable
			}
		}()
		if _, e = c.ExecContext(ctx, "BEGIN"); e != nil {
			return e
		}
		callbackErr = fn(n.ActorID, nativeSnapshotRead{access{ctx, c}})
		return ctx.Err()
	})
	closeErr := l.Close()
	if err != nil || closeErr != nil {
		return ErrUnavailable
	}
	return callbackErr
}

// SubmitNativeObserved uses the same attribution, admission and S02 composition
// as SubmitNative. Fresh is true only when THIS invocation's successful Apply
// produced the grant identified by the final full committed receipt. Retained
// receipts and a different grant committed during reconciliation are not fresh.
// Revoke always returns false. The owned native lifetime spans all S02 cleanup.
func (s *Service) SubmitNativeObserved(ctx context.Context, token T.SecretToken, i contract.Intent, route grants.Route) (NativeSubmission, bool) {
	if s == nil {
		return NativeSubmission{Preparation: Unavailable}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, err := nativeEnvelope(i, route)
	if err != nil {
		return NativeSubmission{Preparation: Refused}, false
	}
	if s.closed || s.lease == nil || s.provider == nil || s.store == nil || s.now == nil {
		return NativeSubmission{Preparation: Unavailable}, false
	}
	status := s.prepare(ctx, token, e)
	if status != Confirmed {
		return NativeSubmission{Preparation: status}, false
	}
	admission, err := s.admission("native-owner", token, false)
	if err != nil {
		return NativeSubmission{Preparation: Confirmed}, false
	}
	cb := admission.GrantCallbacks(route)
	apply := cb.Apply
	var captured writer.Effect
	var createdGrantID string
	cb.Apply = func(ctx context.Context, w writer.Write, intent contract.Intent, at A.Timestamp) (writer.Effect, error) {
		effect, err := apply(ctx, w, intent, at)
		if err == nil && intent.Command() == "grant.create" {
			var result struct {
				GrantID string `json:"grantId"`
			}
			if json.Unmarshal(effect.Result, &result) == nil && schema.ValidUUID(result.GrantID) {
				captured = effect
				createdGrantID = result.GrantID
			}
		}
		return effect, err
	}
	result := writer.New(s.store).Submit(ctx, i, cb)
	return NativeSubmission{Preparation: Confirmed, Result: &result}, nativeFreshCreate(result, captured, createdGrantID)
}

func nativeFreshCreate(result writer.Result, captured writer.Effect, createdGrantID string) bool {
	t := result.Terminal
	if createdGrantID == "" || t == nil || result.Unknown != nil || result.Refused != nil || t.Command != "grant.create" || t.Outcome != "committed" || t.Retention != "full" || t.Result == nil || !bytes.Equal(*t.Result, captured.Result) || len(captured.References) != 1 || len(t.ResultRefs) != 1 {
		return false
	}
	a, b := captured.References[0], t.ResultRefs[0]
	return a.Kind == "grant" && string(a.ID) == createdGrantID && a.Revision == nil && a.Kind == b.Kind && a.ID == b.ID && b.Revision == nil
}
