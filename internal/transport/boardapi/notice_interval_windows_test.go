//go:build windows && amd64

package boardapi

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"github.com/localswim/localswim/internal/board/grants"
	"github.com/localswim/localswim/internal/board/noticeinterval"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
	"modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"
)

func privateNoticeTestRoot(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("S01_TEST_ROOT")
	if parent == "" {
		t.Skip("requires explicit private native test parent")
	}
	if !filepath.IsAbs(parent) {
		t.Fatal("native test parent must be absolute")
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "m12-notice-"+id.String()[:8])
	attr := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(windows.StringToUTF16Ptr(path), &attr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, name := range []string{"board.sqlite", "board.lock"} {
			if err := os.Remove(filepath.Join(path, name)); err != nil && !os.IsNotExist(err) {
				t.Error(err)
			}
		}
		if err := os.Remove(path); err != nil {
			t.Error(err)
		}
	})
	return path
}

func TestNoticeNativeLeaseAndReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := privateNoticeTestRoot(t)
	identity := schema.Identity{BoardID: testBoard, Incarnation: testInc}
	store, err := S.Create(ctx, path, identity)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if store != nil {
			if err := store.Close(); err != nil {
				t.Error(err)
			}
		}
	})
	write := func(run func(*sql.Conn) error) error {
		t.Helper()
		lease, err := store.Writer(ctx)
		if err != nil {
			return err
		}
		err = lease.Do(ctx, run)
		return errors.Join(err, lease.Close())
	}
	const grantID = "a0000000-0000-0000-0000-000000000001"
	const installationID = "b0000000-0000-0000-0000-000000000001"
	const origin = "http://127.0.0.1:53199"
	mustWrite := func(query string, args ...any) {
		t.Helper()
		if err := write(func(c *sql.Conn) error {
			_, err := c.ExecContext(ctx, query, args...)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("INSERT INTO actor VALUES(?,?,'automation','Synthetic bot',?,?)", testInc, testCard, testBoard, testInc)
	mustWrite("INSERT INTO grant_fact VALUES(?,?)", testInc, grantID)
	mustWrite("INSERT INTO grant_record VALUES(?,?,?,1)", testInc, grantID, testCard)
	mustWrite("INSERT INTO grant_right VALUES(?,?,'read')", testInc, grantID)
	service, err := grants.New(installationID, origin, func(_ context.Context, _ writer.Read, now time.Time) (grants.Credential, error) {
		// Credential verification is controlled here. The ordinary current
		// selected actor, grant, workflow and rights checks still run in SQL.
		return grants.Credential{InstallationID: installationID, Origin: origin, BoardID: testBoard, Incarnation: testInc,
			ActorID: testCard, GrantID: grantID, Mode: "cli", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	authorize := func(ctx context.Context, r writer.Read) error {
		return service.RequireCurrent(ctx, r, testBoard, testInc, testCard, "cli", grantID, "read")
	}
	lower, err := ObserveNoticeCut(ctx, store, activityIdentity, func(ctx context.Context, r writer.Read) error {
		// In native DELETE mode a separate writer can stage a change, but a
		// pinned reader prevents it committing before admission.
		if err := write(func(c *sql.Conn) error {
			if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE; UPDATE board SET revision=1"); err != nil {
				return err
			}
			var busy *sqlite.Error
			if _, err := c.ExecContext(ctx, "COMMIT"); !errors.As(err, &busy) || busy.Code() != sqlitelib.SQLITE_BUSY {
				return errors.New("native writer was not blocked by the pinned reader")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return authorize(ctx, r)
	})
	if err != nil || lower.Event != 0 || lower.Revision != 0 {
		t.Fatalf("native lower: %+v %v", lower, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = S.Existing(ctx, path, identity)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(`BEGIN IMMEDIATE;
		INSERT INTO activity(incarnation,origin_board,origin_incarnation,event_id,actor_id,type,board_revision,at,affected)
		VALUES(?,?,?,1,?,'recovery.expire',1,?,'[]'); UPDATE board SET next_event=2,revision=1; COMMIT;`, testInc, testBoard, testInc, testCard, activityTie)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = S.Existing(ctx, path, identity)
	if err != nil {
		t.Fatal(err)
	}
	readDB := func() []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(path, "board.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	before := readDB()
	catchUp, err := CaptureNoticeInterval(ctx, store, activityIdentity, testCard, lower, authorize)
	if err != nil {
		t.Fatal(err)
	}
	requireNoticeEvidence(t, catchUp, "all-automation", 1, 1)
	cut, err := ObserveNoticeCut(ctx, store, activityIdentity, authorize)
	if err != nil || cut.Changed(*catchUp.Upper) {
		t.Fatalf("read changed board or lost native upper: %+v %v", cut, err)
	}
	if _, err := readNoticeSource(ctx, store, activityIdentity, authorize, func(r writer.Read) error {
		page, err := (&Handler{}).activity(r, activityIdentity, "same-grant", activityInterval)
		if err == nil && (len(page.Result.Events) != 1 || page.Result.Events[0].EventID != 1) {
			return errors.New("M11 projection lost the native event")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readDB(), before) {
		t.Fatal("notice or M11 read changed board bytes")
	}
	foreign := A.Identity{BoardID: testBoard, Incarnation: sourceInc}
	if evidence, err := CaptureNoticeInterval(ctx, store, foreign, testCard, lower, authorize); !errors.Is(err, ErrNoticeForeign) || evidence != (noticeinterval.Evidence{}) {
		t.Fatalf("wrong board returned evidence: %+v %v", evidence, err)
	}
	interrupted, interrupt := context.WithCancel(ctx)
	defer interrupt()
	if admitted, err := readNoticeSource(interrupted, store, activityIdentity, authorize, func(writer.Read) error {
		interrupt()
		return ErrNoticeForeign
	}); admitted || !errors.Is(err, ErrNoticeForeign) {
		t.Fatalf("late cancellation converted definite scope refusal into admitted evidence: %v %v", admitted, err)
	}
	if evidence, err := CaptureNoticeInterval(ctx, store, activityIdentity, testCard, lower, func(_ context.Context, r writer.Read) error {
		var ignored int
		_ = r.Scan("SELECT missing_column FROM board", nil, &ignored)
		return grants.ErrDenied
	}); !errors.Is(err, ErrUnavailable) || evidence != (noticeinterval.Evidence{}) {
		t.Fatalf("broken admission returned evidence or masked failure: %+v %v", evidence, err)
	}
	mustWrite("UPDATE board SET next_event=1003")
	budget, err := CaptureNoticeInterval(ctx, store, activityIdentity, testCard, *catchUp.Upper, authorize)
	if err != nil {
		t.Fatal(err)
	}
	requireNoticeEvidence(t, budget, "unavailable", 1002, -1)
	mustWrite("UPDATE grant_record SET enabled=0 WHERE incarnation=? AND grant_id=?", testInc, grantID)
	if evidence, err := CaptureNoticeInterval(ctx, store, activityIdentity, testCard, lower, authorize); !errors.Is(err, grants.ErrDenied) || evidence != (noticeinterval.Evidence{}) {
		t.Fatalf("revoked grant returned evidence: %+v %v", evidence, err)
	}
	if cut, err := ObserveNoticeCut(ctx, store, activityIdentity, authorize); !errors.Is(err, grants.ErrDenied) || cut != (noticeinterval.Cut{}) {
		t.Fatalf("revoked grant returned a wake signal: %+v %v", cut, err)
	}
}
