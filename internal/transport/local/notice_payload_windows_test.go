//go:build windows && amd64

package local

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/localswim/localswim/internal/board/notices"
	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/contract/noticetext"
	"github.com/localswim/localswim/internal/transport/boardapi"
)

func TestNativeMalformedPayloadRefusesWithoutRewrite(t *testing.T) {
	for _, mutation := range [][2]string{
		{`"version":4`, `"version":3`},
		{`"payload":{"version":1`, `"payload":{"version":99`},
		{`"kind":"fallback"`, `"kind":"fallback","extra":true`},
		{`Inspect the selected board for changes.`, `Changed after capture.`},
	} {
		t.Run(mutation[1], func(t *testing.T) {
			r, path, _ := seedNativeInterruptedRecovery(t, true)
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			original := noticeTestBytes(t, path)
			malformed := bytes.Replace(original, []byte(mutation[0]), []byte(mutation[1]), 1)
			if bytes.Equal(malformed, original) {
				t.Fatal("fixture not changed")
			}
			if err := publish(path, malformed, true); err != nil {
				t.Fatal(err)
			}
			refused := reopenNoticeTestBoard(t, filepath.Dir(path))
			status := refused.notices.Snapshot()
			if status.Reason != "storage" || status.State != nil || len(refused.notices.Inbox()) != 0 {
				t.Fatal("malformed private text disclosed evidence")
			}
			if !bytes.Equal(malformed, noticeTestBytes(t, path)) {
				t.Fatal("malformed original silently repaired")
			}
		})
	}
}

// The injected return faults bound what this test proves: real native sidecar
// publication and reopen, with errors before/after publication. They are not
// a claim about physical power loss.
func TestNativeOriginalPayloadPublicationFaultsAndReopen(t *testing.T) {
	for _, phase := range []string{"prepare", "fence", "acceptance", "finalize"} {
		for _, land := range []bool{false, true} {
			name := phase + "/before"
			if land {
				name = phase + "/after"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				r, root := startNativeNoticeTestBoard(t)
				info, err := ReadClient(root)
				if err != nil {
					t.Fatal(err)
				}
				configureNativeNotices(t, r, info)
				if err := r.notices.Close(ctx); err != nil {
					t.Fatal(err)
				}
				cookie := bootstrapTest(t, r)
				term(t, request(t, r, "POST", r.boardPath()+"cards", mutationWire(t, r, "", 0, 0,
					map[string]any{"title": "Synthetic payload publication", "description": ""}), "", cookie, nil), "committed")
				fail := false
				writes := 0
				s, err := notices.Open(r.noticeState.binding, r.noticeState.read, func(binding notices.Binding, raw []byte) error {
					writes++
					if fail && !land {
						fail = false
						return notices.ErrStorage
					}
					if err := r.noticeState.write(binding, raw); err != nil {
						return err
					}
					if fail {
						fail = false
						return notices.ErrStorage
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if err := s.BeginRun(); err != nil {
					t.Fatal(err)
				}
				lower := s.Snapshot().Checkpoint
				e, err := boardapi.CaptureNoticeIntervalWithLabel(ctx, r.store,
					A.Identity{BoardID: A.UUID(info.BoardID), Incarnation: A.UUID(info.Incarnation)}, A.UUID(r.id.BotID), lower,
					"Frozen publication label", r.noticeAuthority("cli", info.Token))
				if err != nil || e.Facts == nil || e.Class != notices.Human {
					t.Fatal("admitted rich source unavailable", err)
				}
				original := noticetext.Build(e.Facts)
				if err := s.Observe(e.Upper.At); err != nil {
					t.Fatal(err)
				}
				fail = phase == "prepare"
				i, err := s.Prepare(e)
				if phase != "prepare" {
					if err != nil {
						t.Fatal(err)
					}
					fail = phase == "fence"
					invoke, fenceErr := s.Fence(i, e.Upper.At, false)
					err = fenceErr
					if phase != "fence" {
						if err != nil {
							t.Fatal(err)
						}
						fail = phase == "acceptance"
						_, err = invoke(e.Upper.At)
						if phase == "finalize" {
							if err != nil {
								t.Fatal(err)
							}
							fail = true
							err = s.Finalize(i.TargetID, i.DeliveryID)
						}
					}
				}
				wantErr := notices.ErrStorage
				if phase == "acceptance" {
					wantErr = notices.ErrUnknown
				}
				if !errors.Is(err, wantErr) || !s.Uncertain() {
					t.Fatal("publication fault did not retain uncertainty", err)
				}
				before := writes
				if err := s.BeginRun(); err == nil || writes != before {
					t.Fatal("uncertain owner resumed effects")
				}
				if err := r.Close(); err != nil {
					t.Fatal(err)
				}
				reopened := reopenNoticeTestBoard(t, root)
				state := reopened.notices.Snapshot().State
				if state == nil || state.Checkpoint.Event > e.Upper.Event {
					t.Fatal("reopen lost bounded original")
				}
				if state.Pending != nil && !noticetext.Equal(state.Pending.Payload, &original) {
					t.Fatal("pending payload changed on reopen")
				}
				if state.Last != nil && !noticetext.Equal(state.Last.Payload, &original) {
					t.Fatal("last payload changed on reopen")
				}
				for _, p := range reopened.notices.Inbox() {
					if !noticetext.Equal(p.Payload, &original) || p.AcceptedAt != e.Upper.At {
						t.Fatal("native receipt changed original")
					}
				}
				pending := phase == "prepare" && land || phase != "prepare" && !(phase == "finalize" && land)
				if (state.Pending != nil) != pending {
					t.Fatal("reopen invented/lost pending evidence")
				}
				count := 0
				if phase == "acceptance" && land || phase == "finalize" {
					count = 1
				}
				if len(reopened.notices.Inbox()) != count {
					t.Fatal("reopen invented/lost acceptance")
				}
				if phase == "fence" && land || phase == "acceptance" && !land {
					private, err := notices.Open(reopened.noticeState.binding, reopened.noticeState.read, reopened.noticeState.write)
					if err != nil {
						t.Fatal(err)
					}
					action, _, err := private.RecoveryPlan()
					if err != nil || action != notices.RecoveryUnknown {
						t.Fatal("attempt uncertainty became replay permission", err)
					}
					if permit, err := private.Fence(state.Pending, e.Upper.At, true); permit != nil || !errors.Is(err, notices.ErrGuarded) {
						t.Fatal("unknown original replayed")
					}
				}
				if len(noticeTestBytes(t, filepath.Join(root, noticeStateName))) > notices.MaxStateBytes {
					t.Fatal("native private bound exceeded")
				}
			})
		}
	}
}
