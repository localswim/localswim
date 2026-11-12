package v2

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

const ownerNoticeTestTarget = "f02f1e36-c5de-7e85-9e95-9159e83ae10a"
const ownerNoticeNextTarget = "f02f1e36-c5de-7e85-9e95-9159e83ae10b"

func ownerNoticeTestSnapshot(id string) snapshot {
	return snapshot{
		Board: board{entity: entity{ID: id, Revision: 1}, Name: "Synthetic\nboard"},
		Users: []user{{ID: humanID, Kind: "human"}, {ID: botID, Kind: "bot"}},
		Cards: []card{
			{ID: "one", Ticket: 1, Title: "private card title"},
			{ID: "two", Ticket: 2},
			{ID: "three", Ticket: 3},
			{ID: "four", Ticket: 4},
			{ID: "five", Ticket: 5},
		},
	}
}

func ownerNoticeTestEdit(previous snapshot, actor, resourceType, resourceID string, at time.Time) snapshot {
	next := previous
	next.Board.Revision++
	next.Activity = append(slices.Clone(previous.Activity), activity{
		BoardID: next.Board.ID, ActorID: actor, ResourceType: resourceType, ResourceID: resourceID,
		Revision: next.Board.Revision, At: at,
	})
	return next
}

func TestOwnerNoticeCommittedMetadata(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	previous := ownerNoticeTestSnapshot("3b21b22e-3538-49ee-af0a-8fae47ca7ff7")
	previous.Activity = []activity{{Revision: 1, ActorID: humanID, ResourceType: "card", ResourceID: "four", At: at}}
	next := ownerNoticeTestEdit(previous, humanID, "card", "one", at.Add(time.Second))
	next.Comments = []comment{{ID: "comment", CardID: "two", Text: "private comment text"}}
	next.Attachments = []attachment{{ID: "attachment", CardID: "five", OriginalName: "private filename"}}
	for _, changed := range []struct {
		kind, id string
		offset   time.Duration
	}{
		{"comment", "comment", 2 * time.Second},
		{"relationship", "link", 3 * time.Second},
		{"attachment", "attachment", 4 * time.Second},
		{"board", previous.Board.ID, 5 * time.Second},
	} {
		next.Activity = append(next.Activity, activity{
			ActorID: humanID, ResourceType: changed.kind, ResourceID: changed.id,
			Revision: next.Board.Revision, At: at.Add(changed.offset),
		})
	}
	next.Activity[3].Before = new(relationshipAuditValue(relationshipLink{SourceCardID: "one", TargetCardID: "three"}))
	private := "private audit value"
	next.Activity[1].After = &private

	event := committedOwnerNotice(previous, next)
	wantCards := []ownerNoticeCard{
		{activityCard{"one", 1}, activityContentsModified.bit()},
		{activityCard{"two", 2}, activityCommentAdded.bit()},
		{activityCard{"three", 3}, activityContentsModified.bit()},
		{activityCard{"five", 5}, activityContentsModified.bit()},
	}
	if !slices.Equal(event.cards, wantCards) {
		t.Fatalf("attributed cards = %v", event.cards)
	}
	message := event.message()
	for _, want := range []string{previous.Board.ID, "Synthetic\\nboard", "12:00:01+00:00", "12:00:04+00:00", "0001, 0002, 0003, 0005"} {
		if !strings.Contains(message, want) {
			t.Errorf("notice omitted %q", want)
		}
	}
	if strings.Count(message, "\n") != 8 || strings.Contains(message, "private") {
		t.Error("notice contains content outside the bounded metadata lines")
	}
	if bot := ownerNoticeTestEdit(next, botID, "card", "four", at.Add(time.Minute)); len(committedOwnerNotice(next, bot).cards) != 0 {
		t.Error("Bot edit produced notice metadata")
	}
}

func TestOwnerNoticeCardBound(t *testing.T) {
	event := ownerNoticeEvent{}
	for ticket := uint64(maxOwnerNoticeCards + 20); ticket > 0; ticket-- {
		card := ownerNoticeCard{activityCard{CardID: fmt.Sprint(ticket), Ticket: ticket}, activityCreated.bit()}
		event.addCard(card)
		event.addCard(card)
	}
	if len(event.cards) != maxOwnerNoticeCards || !event.truncated {
		t.Fatalf("notice retained %d card and ticket pairs, truncated = %t", len(event.cards), event.truncated)
	}
	for index, card := range event.cards {
		if card.Ticket != uint64(index+1) {
			t.Fatalf("ticket %d = %d", index, card.Ticket)
		}
	}
}

func TestOwnerNoticeQuietBurstUsesCurrentDestination(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		type attempt struct{ target, message string }
		var attempts []attempt
		target, nextTarget := ownerNoticeTestTarget, ownerNoticeNextTarget
		enabled := notifications{Enabled: true, TargetThreadID: &target}
		retargeted := notifications{Enabled: true, TargetThreadID: &nextTarget}
		current := enabled
		state := ownerNoticeTestSnapshot("primary")
		other := ownerNoticeTestSnapshot("other")
		worker := newOwnerNoticeWorker(ownerNoticeQuietPeriod, func(id string) (notifications, bool) {
			if id == other.Board.ID {
				return enabled, true
			}
			return current, true
		}, func(_ context.Context, target, message string) bool {
			attempts = append(attempts, attempt{target, message})
			return false
		})
		defer worker.close()

		disabledEdit := ownerNoticeTestEdit(state, humanID, "card", "three", time.Now())
		worker.submitCommitted(state, disabledEdit, false)
		state = disabledEdit
		worker.settingsChanged(state.Board.ID, true)

		first := ownerNoticeTestEdit(state, humanID, "card", "five", time.Now())
		worker.submitCommitted(state, first, true)
		otherEdit := ownerNoticeTestEdit(other, humanID, "card", "one", time.Now())
		worker.submitCommitted(other, otherEdit, true)

		time.Sleep(10 * time.Second)
		second := ownerNoticeTestEdit(first, humanID, "card", "two", time.Now())
		worker.submitCommitted(first, second, true)

		time.Sleep(4 * time.Second)
		synctest.Wait()
		current = retargeted
		worker.settingsChanged(state.Board.ID, true)
		botEdit := ownerNoticeTestEdit(second, botID, "card", "four", time.Now())
		worker.submitCommitted(second, botEdit, true)
		synctest.Wait()
		if len(attempts) != 0 {
			t.Fatalf("queue attempts before first deadline = %d", len(attempts))
		}

		time.Sleep(time.Second)
		synctest.Wait()
		if len(attempts) != 1 || attempts[0].target != target || !strings.Contains(attempts[0].message, "- Tickets: 0001") {
			t.Fatal("other board lost its independent quiet deadline")
		}

		time.Sleep(10*time.Second - time.Nanosecond)
		synctest.Wait()
		if len(attempts) != 1 {
			t.Fatalf("queue attempts before second deadline = %d", len(attempts))
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if len(attempts) != 2 || attempts[1].target != nextTarget ||
			!strings.HasSuffix(attempts[1].message, "- Tickets: 0002, 0005") {
			t.Fatal("committed Human tickets were not coalesced for the current target")
		}

		time.Sleep(2 * ownerNoticeQuietPeriod)
		synctest.Wait()
		if len(attempts) != 2 {
			t.Fatalf("failed deliveries were retried: %d attempts", len(attempts))
		}
	})
}

func TestOwnerNoticeDisableAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := ownerNoticeTestTarget
		enabled := notifications{Enabled: true, TargetThreadID: &target}
		state := ownerNoticeTestSnapshot("board")
		started := make(chan struct{}, 2)
		stopped := make(chan struct{}, 2)
		worker := newOwnerNoticeWorker(ownerNoticeQuietPeriod, func(string) (notifications, bool) {
			return enabled, true
		}, func(ctx context.Context, _, _ string) bool {
			started <- struct{}{}
			<-ctx.Done()
			stopped <- struct{}{}
			return false
		})
		defer worker.close()

		first := ownerNoticeTestEdit(state, humanID, "card", "one", time.Now())
		worker.submitCommitted(state, first, true)
		worker.settingsChanged(state.Board.ID, false)
		disabled := ownerNoticeTestEdit(first, humanID, "card", "two", time.Now())
		worker.submitCommitted(first, disabled, false)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
		if len(started) != 0 {
			t.Fatal("disabling left a pending delivery")
		}

		worker.settingsChanged(state.Board.ID, true)
		second := ownerNoticeTestEdit(disabled, humanID, "card", "one", time.Now())
		worker.submitCommitted(disabled, second, true)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
		if len(started) != 1 {
			t.Fatal("eligible delivery did not start")
		}

		worker.submitCommitted(second, ownerNoticeTestEdit(second, humanID, "card", "three", time.Now()), true)
		worker.close()
		time.Sleep(ownerNoticeQuietPeriod)
		if len(started) != 1 || len(stopped) != 1 {
			t.Fatal("shutdown did not cancel in-flight delivery and discard pending metadata")
		}
	})
}

func TestOwnerNoticeDeliveryRechecksEnabled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		target := ownerNoticeTestTarget
		state := ownerNoticeTestSnapshot("board")
		worker := newOwnerNoticeWorker(ownerNoticeQuietPeriod, func(string) (notifications, bool) {
			return notifications{TargetThreadID: &target}, true
		}, func(context.Context, string, string) bool {
			t.Error("delivery used stale enablement")
			return false
		})
		defer worker.close()
		worker.submitCommitted(state, ownerNoticeTestEdit(state, humanID, "card", "one", time.Now()), true)
		time.Sleep(ownerNoticeQuietPeriod)
		synctest.Wait()
	})
}

func TestOwnerNoticeRefusesWindowsWrappers(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command wrapper qualification")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	for _, suffix := range []string{".cmd", ".bat"} {
		if err := os.WriteFile(filepath.Join(dir, "codex"+suffix), []byte("@echo off\r\nexit /b 0\r\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if deliverOwnerNotice(t.Context(), ownerNoticeTestTarget, "synthetic") {
		t.Fatal("accepted delivery through a Windows command wrapper")
	}
}

func TestOwnerNoticeFailurePreservesDurableEdit(t *testing.T) {
	humanToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), humanToken, botToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	app.store.ownerNotices.close()
	attempts := make(chan string, 1)
	app.store.ownerNotices = newOwnerNoticeWorker(0, app.store.ownerNoticeSettings,
		func(_ context.Context, target, message string) bool {
			if target != ownerNoticeTestTarget {
				t.Error("delivery did not resolve the committed synthetic target")
			}
			attempts <- message
			return false
		})
	app.store.ownerNotices.writerID = app.store.activityWriterID

	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	client := turnOneClient{t, server.URL, humanToken, server.Client()}
	raw, _ := client.call("POST", "/boards", `{"name":"Synthetic notice"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	path := "/notifications/" + b.ID
	_, header := client.call("GET", path, "", "", 200)
	client.call("PATCH", path, fmt.Sprintf(`{"enabled":true,"target_thread_id":%q}`, ownerNoticeTestTarget), header.Get("ETag"), 200)
	raw, header = client.call("POST", "/cards", fmt.Sprintf(
		`{"board_id":%q,"expected_board_etag":%q,"title":"Private synthetic title","lane_id":%q}`,
		b.ID, b.entityTag(), b.LaneIDs[0]), "", 201)
	created := turnOneDecode[card](t, raw)
	select {
	case message := <-attempts:
		if !strings.HasSuffix(message, "- Tickets: 0001") || strings.Contains(message, created.Title) ||
			!strings.Contains(message, "- Writer: `"+app.store.activityWriterID+"`") ||
			!strings.Contains(message, "- Cards: 0001 (`"+created.ID+"`)") {
			t.Fatal("delivery did not contain only committed card metadata")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("committed edit did not trigger the injected failing delivery")
	}
	app.store.ownerNotices.close()
	durable, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if durable.Board.entityTag() != header.Get("X-Board-ETag") || len(durable.Cards) != 1 ||
		durable.Cards[0].ID != created.ID || durable.Cards[0].Title != created.Title {
		t.Fatal("failed owner notice changed the durable card commit")
	}
}

func TestOwnerNoticeRevalidatesPendingAfterSettingsRead(t *testing.T) {
	target, nextTarget := ownerNoticeTestTarget, ownerNoticeNextTarget
	enabled := notifications{Enabled: true, TargetThreadID: &target}
	retargeted := notifications{Enabled: true, TargetThreadID: &nextTarget}
	disabled := notifications{TargetThreadID: &target}
	cleared := notifications{Enabled: true}
	type edit struct {
		settings *notifications
		cardID   string
	}
	for _, test := range []struct {
		name           string
		edits          []edit
		wait           time.Duration
		target, suffix string
	}{
		{"Human edit resets quiet period", []edit{{cardID: "two"}},
			ownerNoticeQuietPeriod, target, "- Tickets: 0001, 0002"},
		{"Retarget preserves deadline", []edit{{settings: &retargeted}},
			0, nextTarget, "- Tickets: 0001"},
		{"Disable drops old burst", []edit{{settings: &disabled}, {settings: &enabled}, {cardID: "two"}},
			ownerNoticeQuietPeriod, target, "- Tickets: 0002"},
		{"Clearing drops old burst", []edit{{settings: &cleared}, {settings: &enabled}, {cardID: "two"}},
			ownerNoticeQuietPeriod, target, "- Tickets: 0002"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				type attempt struct{ target, message string }
				var attempts []attempt
				state := ownerNoticeTestSnapshot("board")
				current := enabled
				loadStarted := make(chan struct{})
				releaseLoad := make(chan struct{})
				worker := newOwnerNoticeWorker(ownerNoticeQuietPeriod, func(string) (notifications, bool) {
					settings := current
					select {
					case <-loadStarted:
					default:
						close(loadStarted)
						<-releaseLoad
					}
					return settings, true
				}, func(_ context.Context, target, message string) bool {
					attempts = append(attempts, attempt{target, message})
					return true
				})
				defer worker.close()
				defer func() {
					select {
					case <-releaseLoad:
					default:
						close(releaseLoad)
					}
				}()
				first := ownerNoticeTestEdit(state, humanID, "card", "one", time.Now())
				worker.submitCommitted(state, first, true)
				time.Sleep(ownerNoticeQuietPeriod)
				synctest.Wait()
				select {
				case <-loadStarted:
				default:
					t.Fatal("worker did not begin the settings read")
				}

				state = first
				for _, change := range test.edits {
					if change.settings != nil {
						current = *change.settings
						worker.settingsChanged(state.Board.ID, ownerNoticeEnabled(current))
					} else {
						next := ownerNoticeTestEdit(state, humanID, "card", change.cardID, time.Now())
						worker.submitCommitted(state, next, ownerNoticeEnabled(current))
						state = next
					}
				}
				close(releaseLoad)
				if test.wait > 0 {
					time.Sleep(test.wait - time.Nanosecond)
					synctest.Wait()
					if len(attempts) != 0 {
						t.Fatalf("queue attempted delivery during the quiet period: %d attempts", len(attempts))
					}
					time.Sleep(time.Nanosecond)
				}
				synctest.Wait()
				if len(attempts) != 1 || attempts[0].target != test.target ||
					!strings.HasSuffix(attempts[0].message, test.suffix) {
					t.Fatal("expected one delivery of the current burst to the current target")
				}
			})
		})
	}
}

func TestOwnerNoticeProjectTimestampFormat(t *testing.T) {
	start := time.Date(2026, 11, 11, 2, 3, 4, 123000000, time.FixedZone("source", 5*3600+30*60))
	for _, tc := range []struct {
		name     string
		duration time.Duration
		want     string
	}{
		{"single second", 100 * time.Millisecond, "- Start: 2026-11-10 20:33:04+00:00\n- End: 2026-11-10 20:33:04+00:00\n"},
		{"interval", 2200 * time.Millisecond, "- Start: 2026-11-10 20:33:04+00:00\n- End: 2026-11-10 20:33:06+00:00\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			event := ownerNoticeEvent{
				boardID: "board", label: "Test", writerID: "writer", since: start, until: start.Add(tc.duration),
				cards: []activityCard{{CardID: "card", Ticket: 1}},
			}
			want := "localswim board-change notice\n\n- Board: \"Test\" (`board`)\n- Writer: `writer`\n" +
				tc.want + "- Cards: 0001 (`card`)\n- Tickets: 0001"
			if got := event.message(); got != want {
				t.Fatalf("notice timestamp format mismatch\ngot: %s\nwant: %s", got, want)
			}
		})
	}
}
