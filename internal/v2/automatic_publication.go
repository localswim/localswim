package v2

import (
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const (
	automaticPublicationQuiet = 5 * time.Second
	publicationFirstRetry     = time.Second
	publicationMaxRetry       = 30 * time.Second
)

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

	mu         sync.Mutex
	wake       chan struct{}
	done       chan struct{}
	started    bool
	closed     bool
	enabled    bool
	generation uint64
	dirty      bool
	quietUntil time.Time
	retryAt    time.Time
	backoff    time.Duration
	checkpoint publicationCheckpoint
	errorCode  *string
}

func newAutomaticPublisher(root, key string, capture func() (publicationCapture, error)) *automaticPublisher {
	return &automaticPublisher{root: root, ref: publicationCheckpointRef(root, key), capture: capture,
		wake: make(chan struct{}, 1), done: make(chan struct{})}
}

func (p *automaticPublisher) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *automaticPublisher) schedule() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.enabled = true
	p.generation++
	p.dirty = true
	p.quietUntil = time.Now().Add(automaticPublicationQuiet)
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
	p.dirty = false
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

func (p *automaticPublisher) allowed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled && !p.closed
}

func (p *automaticPublisher) current(generation uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.enabled && !p.closed && generation == p.generation
}

func (p *automaticPublisher) run() {
	defer close(p.done)
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		if !p.enabled || (!p.dirty && p.retryAt.IsZero()) {
			p.mu.Unlock()
			<-p.wake
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
			select {
			case <-timer.C:
			case <-p.wake:
			}
			timer.Stop()
			continue
		}
		generation := p.generation
		capture := p.dirty && !now.Before(p.quietUntil)
		p.mu.Unlock()

		checkpoint, loaded, captured, err := p.attempt(generation, capture)
		p.mu.Lock()
		if loaded {
			p.checkpoint = checkpoint
		}
		if p.enabled && !p.closed {
			p.recordAttempt(generation, captured, err)
		}
		p.mu.Unlock()
	}
}

func (p *automaticPublisher) recordAttempt(generation uint64, captured bool, err error) {
	if err == nil {
		if captured && generation == p.generation {
			p.dirty = false
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

func (p *automaticPublisher) attempt(generation uint64, capture bool) (publicationCheckpoint, bool, bool, error) {
	repo, err := lockPublication(p.root)
	if err != nil {
		return publicationCheckpoint{}, false, false, err
	}
	defer releasePublicationRepository(repo.lease)
	checkpoint, oid, err := repo.checkpoint(p.ref)
	if err != nil {
		return publicationCheckpoint{}, false, false, err
	}
	if !p.allowed() {
		return checkpoint, true, false, nil
	}
	if checkpoint.Pending != nil {
		checkpoint, err = repo.finishPending(p.ref, oid, checkpoint)
		return checkpoint, true, false, err
	}
	if !capture || !p.current(generation) {
		return checkpoint, true, false, nil
	}
	value, err := p.capture()
	if err != nil {
		return checkpoint, true, false, err
	}
	if value.close != nil {
		defer value.close()
	}
	current := func() bool { return p.current(generation) }
	if !current() {
		return checkpoint, true, false, nil
	}
	checkpoint, err = repo.acceptPublication(p.ref, oid, checkpoint, value, current)
	return checkpoint, true, true, err
}
