package v2

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	automaticPublicationQuiet    = 5 * time.Second
	automaticPublicationMaxDelay = 60 * time.Second
	publicationFirstRetry        = time.Second
	publicationMaxRetry          = 30 * time.Second
)

// publicationTiming bounds when automatic publication captures a change. Each
// write restarts the quiet window, but the capture is never later than
// maxDelay after the first unpublished change, so a board written more often
// than the quiet window still publishes.
type publicationTiming struct {
	quiet    time.Duration
	maxDelay time.Duration
}

// Tests MAY shorten these timings. A publisher copies them when it is created.
var automaticPublicationTiming = publicationTiming{automaticPublicationQuiet, automaticPublicationMaxDelay}

// due returns the capture deadline for a write at now, given the time of the
// first change not yet captured.
func (timing publicationTiming) due(firstDirty, now time.Time) time.Time {
	return earliest(now.Add(timing.quiet), firstDirty.Add(timing.maxDelay))
}

func earliest(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// publicationTicket identifies one attempt. Generation advances on every
// write and decides whether a capture covered the latest change. Epoch advances
// only when publishing is disabled, so a newer write never abandons an attempt
// already under way; only disabling or closing the publisher does.
type publicationTicket struct {
	generation uint64
	epoch      uint64
	started    time.Time
}

func samePublishingSettings(a, b publishing) bool {
	return a.Enabled == b.Enabled && sameText(a.Repository, b.Repository)
}

func patchPublishing(current publishing, raw json.RawMessage) (publishing, error) {
	fields, err := decodeObject(raw)
	if err != nil {
		return publishing{}, invalid("publishing", "Expected a publishing settings object.")
	}
	if len(fields) == 0 {
		return publishing{}, invalid("publishing", "Expected enabled or repository.")
	}
	if err := rejectFields(fields, []string{"enabled", "repository"}, true); err != nil {
		return publishing{}, err
	}
	if raw, exists := fields["enabled"]; exists {
		switch string(raw) {
		case "true":
			current.Enabled = true
		case "false":
			current.Enabled = false
		default:
			return publishing{}, invalid("publishing.enabled", "Expected a Boolean.")
		}
	}
	if raw, exists := fields["repository"]; exists {
		if string(raw) == "null" {
			current.Repository = nil
		} else {
			alias, err := textField(raw, "publishing.repository", 1, 2048)
			if err != nil || alias != "origin" {
				return publishing{}, invalid("publishing.repository", "Expected origin or null.")
			}
			current.Repository = &alias
		}
	}
	return current, nil
}

type automaticPublisher struct {
	root    string
	ref     string
	capture func() (publicationCapture, error)
	timing  publicationTiming

	mu         sync.Mutex
	wake       chan struct{}
	done       chan struct{}
	flushes    chan chan error
	started    bool
	closed     bool
	enabled    bool
	repository string
	generation uint64
	epoch      uint64
	dirty      bool
	firstDirty time.Time
	lastWrite  time.Time
	quietUntil time.Time
	retryAt    time.Time
	backoff    time.Duration
	checkpoint publicationCheckpoint
	errorCode  *string
}

func newAutomaticPublisher(root, key string, capture func() (publicationCapture, error)) *automaticPublisher {
	return &automaticPublisher{root: root, ref: publicationCheckpointRef(root, key), capture: capture,
		timing: automaticPublicationTiming, wake: make(chan struct{}, 1), done: make(chan struct{}), flushes: make(chan chan error)}
}

func (p *automaticPublisher) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *automaticPublisher) schedule(config publishing) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.enabled = true
	p.repository = snapshotString(config.Repository)
	p.generation++
	now := time.Now()
	if !p.dirty {
		p.firstDirty = now
	}
	p.dirty = true
	p.lastWrite = now
	p.quietUntil = p.timing.due(p.firstDirty, now)
	if !p.started {
		p.started = true
		go p.run()
	}
	p.signal()
}

func (p *automaticPublisher) disable() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.enabled = false
	p.generation++
	p.epoch++
	p.dirty = false
	p.firstDirty = time.Time{}
	p.retryAt = time.Time{}
	p.backoff = 0
	p.signal()
}

func (p *automaticPublisher) stop() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.signal()
	if p.started {
		return p.done
	}
	return nil
}

func (p *automaticPublisher) closedCheckpoint() (publicationCheckpoint, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.checkpoint, p.closed
}

func (p *automaticPublisher) close() {
	if done := p.stop(); done != nil {
		<-done
	}
}

func (p *automaticPublisher) status(config publishing) publishing {
	p.mu.Lock()
	defer p.mu.Unlock()
	config.LastSuccessAt = cloneSnapshotPointer(p.checkpoint.LastSuccessAt)
	config.Pending = p.checkpoint.Pending != nil || (config.Enabled && p.enabled && (p.dirty || !p.retryAt.IsZero()))
	config.ErrorCode = cloneSnapshotPointer(p.errorCode)
	return config
}

func (p *automaticPublisher) enabledSelection() (publicationSelection, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return publicationSelection{remote: p.repository}, p.enabled && !p.closed
}

func (p *automaticPublisher) current(epoch uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled && !p.closed && epoch == p.epoch
}

// Called with p.mu held.
func (p *automaticPublisher) ticket() publicationTicket {
	return publicationTicket{generation: p.generation, epoch: p.epoch, started: time.Now()}
}

func (p *automaticPublisher) flushEnabled() error {
	p.mu.Lock()
	enabled := p.enabled && !p.closed
	p.mu.Unlock()
	if enabled {
		return p.flush()
	}
	return nil
}

// Flush runs on the publisher's worker, after any in-flight attempt. Its
// caller must hold the corresponding command admission closed.
func (p *automaticPublisher) flush() error {
	p.mu.Lock()
	ready := p.started && p.enabled && !p.closed
	p.mu.Unlock()
	if !ready {
		return gitPublicationError("publication_failed")
	}
	result := make(chan error, 1)
	select {
	case p.flushes <- result:
		return <-result
	case <-p.done:
		return gitPublicationError("publication_failed")
	}
}

func (p *automaticPublisher) run() {
	defer close(p.done)
	for {
		select {
		case result := <-p.flushes:
			result <- p.flushCurrent()
			continue
		default:
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		if !p.enabled || (!p.dirty && p.retryAt.IsZero()) {
			p.mu.Unlock()
			p.wait(nil)
			continue
		}
		now := time.Now()
		due := p.retryAt
		if due.IsZero() {
			due = p.quietUntil
		}
		if now.Before(due) {
			p.mu.Unlock()
			timer := time.NewTimer(time.Until(due))
			p.wait(timer.C)
			timer.Stop()
			continue
		}
		ticket := p.ticket()
		capture := p.dirty && !now.Before(p.quietUntil)
		p.mu.Unlock()
		_ = p.runAttempt(ticket, capture, false)
	}
}

func (p *automaticPublisher) wait(timer <-chan time.Time) {
	select {
	case <-timer:
	case <-p.wake:
	case result := <-p.flushes:
		result <- p.flushCurrent()
	}
}

func (p *automaticPublisher) flushCurrent() error {
	p.mu.Lock()
	ticket, dirty := p.ticket(), p.dirty
	pending := p.checkpoint.Pending != nil || !p.retryAt.IsZero()
	enabled := p.enabled && !p.closed
	p.mu.Unlock()
	if !enabled {
		return gitPublicationError("publication_failed")
	}
	if !dirty && !pending {
		return nil
	}
	return p.runAttempt(ticket, dirty, true)
}

func (p *automaticPublisher) runAttempt(ticket publicationTicket, capture, flush bool) error {
	checkpoint, loaded, captured, err := p.attempt(ticket.epoch, capture, flush)
	p.mu.Lock()
	defer p.mu.Unlock()
	if loaded {
		p.checkpoint = checkpoint
	}
	if p.enabled && !p.closed && p.epoch == ticket.epoch {
		p.recordAttempt(ticket, captured, err)
	}
	if flush && (!p.enabled || p.closed || p.generation != ticket.generation) {
		return gitPublicationError("publication_failed")
	}
	return err
}

func (p *automaticPublisher) recordAttempt(ticket publicationTicket, captured bool, err error) {
	if err == nil {
		if captured && ticket.generation == p.generation {
			p.dirty = false
			p.firstDirty = time.Time{}
		} else if captured && p.dirty {
			// Writes during the attempt are newer than its capture. Bound their
			// wait from the attempt's start, which precedes the capture.
			p.firstDirty = ticket.started
			p.quietUntil = p.timing.due(p.firstDirty, p.lastWrite)
		}
		p.backoff = 0
		p.retryAt = time.Time{}
		p.errorCode = nil
		return
	}
	code := "publication_failed"
	var failure publicationGitError
	if errors.As(err, &failure) {
		code = string(failure)
	}
	p.errorCode = &code
	p.backoff = min(max(p.backoff*2, publicationFirstRetry), publicationMaxRetry)
	p.retryAt = time.Now().Add(p.backoff)
}

func (p *automaticPublisher) attempt(epoch uint64, capture, flush bool) (_ publicationCheckpoint, _, _ bool, resultErr error) {
	repo, err := lockPublication(p.root)
	if err != nil {
		return publicationCheckpoint{}, false, false, err
	}
	defer func() {
		resultErr = repo.processError(resultErr)
		repo.release()
	}()
	checkpoint, oid, err := repo.checkpoint(p.ref)
	if err != nil {
		return publicationCheckpoint{}, false, false, err
	}
	selection, enabled := p.enabledSelection()
	if !enabled {
		return checkpoint, true, false, nil
	}
	if checkpoint.Pending != nil {
		checkpoint, err = repo.finishPending(p.ref, oid, checkpoint, selection)
		if err != nil || !flush || !capture {
			return checkpoint, true, false, err
		}
		checkpoint, oid, err = repo.checkpoint(p.ref)
		if err != nil {
			return publicationCheckpoint{}, false, false, err
		}
	}
	if !capture || !p.current(epoch) {
		return checkpoint, true, false, nil
	}
	value, err := p.capture()
	if err != nil {
		return checkpoint, true, false, err
	}
	if value.close != nil {
		defer value.close()
	}
	current := func() bool { return p.current(epoch) }
	if !current() {
		return checkpoint, true, false, nil
	}
	checkpoint, err = repo.acceptPublication(p.ref, oid, checkpoint, value, selection, current)
	return checkpoint, true, true, err
}
