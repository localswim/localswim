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
	after := time.Now()
	if !p.dirty || !p.firstDirty.Equal(started) {
		t.Fatalf("overtaken capture: dirty=%t firstDirty=%s", p.dirty, p.firstDirty)
	}
	// One quiet window from the latest write, and never sooner than one quiet
	// window from the end of the attempt.
	if p.quietUntil.Before(now.Add(timing.quiet)) || p.quietUntil.After(after.Add(timing.quiet)) {
		t.Fatalf("next deadline %s, want one quiet window after %s", p.quietUntil, now)
	}

	// A slow attempt that outlived the maximum still leaves a quiet window.
	p.dirty, p.lastWrite = true, time.Now()
	p.recordAttempt(publicationTicket{generation: 6, started: time.Now().Add(-2 * timing.maxDelay)}, true, nil)
	if p.quietUntil.Before(time.Now().Add(timing.quiet - time.Second)) {
		t.Fatalf("attempts would run back to back: next deadline %s", p.quietUntil)
	}

	p.recordAttempt(publicationTicket{generation: 7, started: now}, true, nil)
	if p.dirty || !p.firstDirty.IsZero() {
		t.Fatalf("current capture left dirty=%t firstDirty=%s", p.dirty, p.firstDirty)
	}
}

// An ordinary edit never cancels an attempt under way. A new destination does,
// as disabling does, so no attempt pushes to a remote the owner switched away
// from. Disabling also clears a failure that no longer applies.
func TestAutomaticPublicationEpochFollowsDestinationAndDisable(t *testing.T) {
	origin := "origin"
	p := &automaticPublisher{timing: automaticPublicationTiming, started: true, wake: make(chan struct{}, 1)}
	p.schedule(publishing{Enabled: true})
	enabled := p.epoch
	if enabled == 0 {
		t.Fatal("enabling did not start a new epoch")
	}
	p.schedule(publishing{Enabled: true})
	if p.epoch != enabled || !p.current(enabled) {
		t.Fatal("an ordinary edit cancelled the attempt under way")
	}
	p.schedule(publishing{Enabled: true, Repository: &origin})
	if p.epoch == enabled || p.current(enabled) {
		t.Fatal("a destination change left the old attempt current")
	}
	retargeted := p.epoch
	code := "git_push_failed"
	p.errorCode = &code
	p.disable()
	if p.current(retargeted) || p.errorCode != nil || p.dirty {
		t.Fatalf("disable: current=%t error=%v dirty=%t", p.current(retargeted), p.errorCode, p.dirty)
	}
	p.schedule(publishing{Enabled: true, Repository: &origin})
	if p.current(retargeted) || !p.dirty {
		t.Fatal("re-enabling revived an attempt from before the disable")
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
