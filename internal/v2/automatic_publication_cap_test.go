package v2

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func shortenPublicationTiming(t *testing.T, timing publicationTiming) {
	t.Helper()
	saved := automaticPublicationTiming
	automaticPublicationTiming = timing
	t.Cleanup(func() { automaticPublicationTiming = saved })
}

// Writes arrive faster than the quiet window, so only the maximum delay can
// explain a publication while they continue.
func TestAutomaticPublicationCapsDelayUnderSteadyWrites(t *testing.T) {
	timing := publicationTiming{quiet: time.Second, maxDelay: 3 * time.Second}
	shortenPublicationTiming(t, timing)
	fixture := m34PublicationFixture(t)
	path := "/boards/" + fixture.state.Board.ID
	editor := fixture.editor
	raw, _ := editor.call("GET", path, "", "", 200)
	current := turnOneDecode[board](t, raw)
	raw, _ = editor.call("PATCH", path, `{"publishing":{"enabled":true,"repository":"origin"}}`, current.entityTag(), 200)
	current = turnOneDecode[board](t, raw)
	remoteHead := func() string {
		return strings.TrimSpace(string(m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")))
	}
	baseline := remoteHead()
	start := time.Now()
	limit := start.Add(timing.maxDelay + 10*time.Second)
	for i := 0; remoteHead() == baseline; i++ {
		if time.Now().After(limit) {
			t.Fatalf("no publication within %s while writes continued", time.Since(start))
		}
		raw, _ = editor.call("PATCH", path, fmt.Sprintf(`{"name":"Steady write %d"}`, i), current.entityTag(), 200)
		current = turnOneDecode[board](t, raw)
		time.Sleep(200 * time.Millisecond)
	}
	if elapsed := time.Since(start); elapsed < timing.maxDelay-timing.quiet {
		t.Fatalf("publication after %s came before the maximum delay could apply", elapsed)
	}
	published := m34Git(t, fixture.remote, "log", "-1", "--format=%s", "refs/heads/main")
	if !strings.Contains(string(published), "localswim board "+fixture.state.Board.ID) {
		t.Fatalf("remote head is not a board publication: %q", published)
	}
	editor.call("PATCH", path, `{"publishing":{"enabled":false}}`, current.entityTag(), 200)
}

// A capture that a newer write overtook leaves the board dirty. The next
// deadline counts from the attempt's start and the latest write, not from the
// earlier, already expired deadline.
func TestAutomaticPublicationRestartsBoundAfterOvertakenCapture(t *testing.T) {
	timing := publicationTiming{quiet: 5 * time.Second, maxDelay: 60 * time.Second}
	p := &automaticPublisher{timing: timing, enabled: true}
	now := time.Now()
	started := now.Add(-time.Second)
	p.generation = 7
	p.dirty = true
	p.firstDirty = now.Add(-2 * time.Minute)
	p.lastWrite = now
	p.quietUntil = p.firstDirty.Add(timing.maxDelay)
	p.recordAttempt(publicationTicket{generation: 6, started: started}, true, nil)
	if !p.dirty || !p.firstDirty.Equal(started) {
		t.Fatalf("overtaken capture: dirty=%t firstDirty=%s", p.dirty, p.firstDirty)
	}
	if want := now.Add(timing.quiet); !p.quietUntil.Equal(want) {
		t.Fatalf("next deadline %s, want %s", p.quietUntil, want)
	}

	p.recordAttempt(publicationTicket{generation: 7, started: now}, true, nil)
	if p.dirty || !p.firstDirty.IsZero() {
		t.Fatalf("current capture left dirty=%t firstDirty=%s", p.dirty, p.firstDirty)
	}
}

func TestPublicationTimingDueHonorsQuietAndMaximum(t *testing.T) {
	timing := publicationTiming{quiet: 5 * time.Second, maxDelay: 60 * time.Second}
	first := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if got := timing.due(first, first); !got.Equal(first.Add(5 * time.Second)) {
		t.Fatalf("first write due %s", got)
	}
	if got := timing.due(first, first.Add(58*time.Second)); !got.Equal(first.Add(60 * time.Second)) {
		t.Fatalf("late write due %s, want the maximum", got)
	}
}
