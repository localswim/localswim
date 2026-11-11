package v2

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"sync"
)

var (
	errServiceDraining = errors.New("service is draining")
	errFinalPublish    = errors.New("final publication failed")
	errActivationBuild = errors.New("build is not validated for activation")
	errActivationID    = errors.New("activation identifier already exists")
	errActivationStart = errors.New("candidate could not prepare for activation")
)

// PreparedActivation is a native replacement waiting to take ownership. The
// caller must either complete the handoff or abort and reap this process.
type PreparedActivation interface {
	Activate() error
	Abort() error
}

// ServiceExit is sent only after command admission has drained and all enabled
// publishers have flushed. A nil Replacement requests a graceful stop.
type ServiceExit struct {
	ActivationID string
	Replacement  PreparedActivation
}

type serviceLifecycle struct {
	mu       sync.Mutex
	commands sync.WaitGroup
	revision uint64
	draining bool
	activeID string
	failure  *string
	exit     chan ServiceExit
	prepare  func(image []byte, activationID string) (PreparedActivation, error)
}

func (l *serviceLifecycle) join() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.draining {
		return false
	}
	l.commands.Add(1)
	return true
}

func (l *serviceLifecycle) snapshot(running serviceStatus) serviceStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.statusLocked(running)
}

func (l *serviceLifecycle) statusLocked(running serviceStatus) serviceStatus {
	running.revision = l.revision
	running.RestartErrorCode = l.failure
	if l.draining {
		running.State = "draining"
		running.RestartErrorCode = nil
	}
	return running
}

func (l *serviceLifecycle) begin(running serviceStatus, matches func(serviceStatus) bool, activationID string) (serviceStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.draining {
		return serviceStatus{}, errServiceDraining
	}
	if !matches(l.statusLocked(running)) {
		return serviceStatus{}, errStale
	}
	l.revision++
	l.draining, l.activeID = true, activationID
	return l.statusLocked(running), nil
}

func (l *serviceLifecycle) activating(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.draining && l.activeID == id
}

func (l *serviceLifecycle) resume(code string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.revision++
	l.draining, l.activeID = false, ""
	if code != "" {
		l.failure = &code
	}
}

// ExitRequests belongs to the native HTTP server. Prepare is configured
// before its handlers start and is never modified by an API request.
func (s *Service) ExitRequests(prepare func([]byte, string) (PreparedActivation, error)) <-chan ServiceExit {
	s.lifecycle.prepare = prepare
	return s.lifecycle.exit
}

func (s *Service) flushPublishers() error {
	// No commands can change publisher settings or captured revisions once
	// admission is closed and every admitted command has returned.
	if err := s.workspace.publisher.flushEnabled(); err != nil {
		return err
	}
	s.store.mu.RLock()
	publishers := make([]*automaticPublisher, 0, len(s.store.publishers))
	for _, id := range slices.Sorted(maps.Keys(s.store.publishers)) {
		publishers = append(publishers, s.store.publishers[id])
	}
	s.store.mu.RUnlock()
	for _, p := range publishers {
		if err := p.flushEnabled(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) drain() error {
	s.lifecycle.commands.Wait()
	return s.flushPublishers()
}

func publicationFailureCode(err error) string {
	var failure publicationGitError
	if errors.As(err, &failure) {
		return string(failure)
	}
	return "publication_failed"
}

func (x *request) stopService() {
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err := rejectFields(fields, []string{"state"}, true); err != nil {
		x.fail(err)
		return
	}
	var state string
	if json.Unmarshal(fields["state"], &state) != nil || state != "stopped" {
		x.fail(invalid("state", "Expected stopped."))
		return
	}
	status, err := x.service.lifecycle.begin(x.service.builds.service(), matchingServiceTag(expected), "")
	if err != nil {
		x.fail(err)
		return
	}
	if err := x.service.drain(); err != nil {
		x.service.lifecycle.resume(publicationFailureCode(err))
		x.fail(errFinalPublish)
		return
	}
	status.Port = x.port
	x.respond(http.StatusAccepted, status, status.entityTag())
	x.service.lifecycle.exit <- ServiceExit{}
}
