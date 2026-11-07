//go:build windows && amd64

package local

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/store/writer"
	"github.com/localswim/localswim/internal/transport/boardapi"
)

func TestNoticeLabelEngineLifetimeAndStandaloneDefault(t *testing.T) {
	standalone, _ := startNativeNoticeTestBoard(t)
	if standalone.noticeLabel != "Board "+standalone.id.BoardID {
		t.Fatal("standalone notice label did not use exact physical identity")
	}
	for _, label := range []string{"  Harbor 😀  ", "", "unsafe\nlabel"} {
		t.Run(label, func(t *testing.T) {
			root := filepath.Join(nativeNoticeTestParent(t), "local")
			options := EngineOptions{DataDir: root, Port: freePort(t), NoticeLabel: label, WorkspaceID: uuid(), Origin: "http://127.0.0.1:8991"}
			engine, err := OpenEngine(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = engine.Close() })
			expected := label
			if label == "" || label == "unsafe\nlabel" {
				expected = "Board " + engine.id.BoardID
			}
			options.NoticeLabel = "Later configuration"
			if engine.noticeLabel != expected {
				t.Fatal("configured label was changed, cropped or not frozen")
			}
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			options.ExpectedIdentity = new(engine.Identity())
			reopened, err := OpenEngine(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if reopened.noticeLabel != options.NoticeLabel || engine.noticeLabel != expected {
				t.Fatal("reconstruction did not preserve original lifetime label")
			}
		})
	}
}

func TestNoticeFactsNativeCreationCompactionAndAuthority(t *testing.T) {
	r, root := startNativeNoticeTestBoard(t)
	info, err := ReadClient(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	authorize := r.noticeAuthority("cli", info.Token)
	identity := A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}
	lower, err := boardapi.ObserveNoticeCut(ctx, r.store, identity, authorize)
	if err != nil {
		t.Fatal(err)
	}
	cookie := bootstrapTest(t, r)
	body := mutationWire(t, r, "", 0, 0, map[string]any{"title": "PRIVATE-TITLE", "description": "PRIVATE-DESCRIPTION"})
	term(t, request(t, r, "POST", r.boardPath()+"cards", body, "", cookie, nil), "committed")
	capture := func() {
		t.Helper()
		got, err := boardapi.CaptureNoticeIntervalWithLabel(ctx, r.store, identity, A.UUID(r.id.BotID), lower, r.noticeLabel, authorize)
		if err != nil || got.Class != "human" || got.Facts == nil || got.Facts.Label != "Board "+info.BoardID || !reflect.DeepEqual(got.Facts.Tickets, []A.Positive{1}) {
			t.Fatalf("native untouched/compacted facts unavailable: %+v %v", got, err)
		}
	}
	capture()
	// A newly created untouched card has no prior-revision snapshot. Removing
	// prose-bearing full outcomes models already compacted retained operations.
	configuredGrantSQL(t, r, `DELETE FROM operation_full`)
	capture()
	denied, err := boardapi.CaptureNoticeIntervalWithLabel(ctx, r.store, identity, A.UUID(r.id.BotID), lower, r.noticeLabel,
		func(context.Context, writer.Read) error { return grants.ErrDenied })
	if !errors.Is(err, grants.ErrDenied) || denied.Facts != nil || denied.Upper != nil {
		t.Fatal("refused authority produced fallback or text facts")
	}
	foreign := identity
	foreign.Incarnation = A.UUID(uuid())
	denied, err = boardapi.CaptureNoticeIntervalWithLabel(ctx, r.store, foreign, A.UUID(r.id.BotID), lower, r.noticeLabel, authorize)
	if !errors.Is(err, boardapi.ErrNoticeForeign) || denied.Facts != nil || denied.Upper != nil {
		t.Fatal("foreign source produced fallback or text facts")
	}
}
