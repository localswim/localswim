package v2

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	eventHeartbeat    = 15 * time.Second
	eventWriteTimeout = 2 * time.Second
	maxWatchedBoards  = 100
)

// The generation channel is an invalidation signal, not a queue of edits.
// Closing it wakes every subscriber, including those watching other boards.
type eventHub struct {
	mu         sync.Mutex
	generation chan struct{}
	stopped    chan struct{}
	drainUntil time.Time // published once, before stopped closes
	active     sync.WaitGroup
}

func newEventHub() *eventHub {
	return &eventHub{generation: make(chan struct{}), stopped: make(chan struct{})}
}

func (h *eventHub) join() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.stopped:
		return false
	default:
		h.active.Add(1)
		return true
	}
}

func (h *eventHub) changed() {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.stopped:
	default:
		close(h.generation)
		h.generation = make(chan struct{})
	}
}

func (h *eventHub) observe() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.generation
}

// BeginEventShutdown stops event delivery when the native service is interrupted.
func (s *Service) BeginEventShutdown() {
	s.beginEventShutdown(time.Time{})
}

// BeginEventDrain lets existing streams deliver the final committed changes.
// The final write budget is shared across frames, including the closing chunk.
func (s *Service) BeginEventDrain() {
	s.beginEventShutdown(time.Now().Add(eventWriteTimeout))
}

func (s *Service) beginEventShutdown(deadline time.Time) {
	h := s.events
	h.mu.Lock()
	select {
	case <-h.stopped:
	default:
		h.drainUntil = deadline
		close(h.stopped)
	}
	h.mu.Unlock()
}

// StopEvents signals and joins event handlers before releasing durable state.
func (s *Service) StopEvents() {
	s.BeginEventShutdown()
	s.events.active.Wait()
}

type eventFrame struct {
	Type              string `json:"type"`
	Resource          string `json:"resource"`
	BoardID           string `json:"board_id,omitempty"`
	BoardRevision     uint64 `json:"board_revision,omitempty"`
	WorkspaceRevision uint64 `json:"workspace_revision,omitempty"`
}

type eventState struct {
	workspace uint64
	catalog   string
	boards    []uint64
}

func eventBoardIDs(rawQuery string) ([]string, error) {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, invalid("watch", "Malformed query string.")
	}
	if len(query["board_id"]) > 1 {
		return nil, invalid("board_id", "Repeated query parameter.")
	}
	if len(query["watch"]) > maxWatchedBoards {
		return nil, invalid("watch", "Too many watched boards.")
	}
	ids := make([]string, 0, len(query["board_id"])+len(query["watch"]))
	for _, field := range []string{"board_id", "watch"} {
		for _, value := range query[field] {
			id, ok := normalizeID(value)
			if !ok {
				return nil, invalid(field, "Expected a UUIDv4.")
			}
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

func (s *Service) eventState(actor string, ids []string) (eventState, error) {
	workspace, err := s.workspace.read()
	if err != nil {
		return eventState{}, err
	}
	state := eventState{workspace: workspace.Revision, boards: make([]uint64, len(ids))}
	boards := s.store
	boards.mu.RLock()
	for index, id := range ids {
		value, ok := boards.boards[id]
		if !ok || !slices.Contains(value.Board.UserIDs, actor) {
			boards.mu.RUnlock()
			return eventState{}, errMissing
		}
		if !value.can(actor, "read") {
			boards.mu.RUnlock()
			return eventState{}, errForbidden
		}
		state.boards[index] = value.Board.Revision
	}

	rows := make([]boardSummary, 0, len(boards.boards))
	for _, value := range boards.boards {
		if !value.can(actor, "read") {
			continue
		}
		// Board revisions travel in watched frames. Only catalog fields affect this tag.
		b := value.Board
		row := boardSummary{ID: b.ID, Name: &b.Name, Availability: b.Availability}
		if b.Availability == "open" {
			row.Attention = &b.Attention
			row.LastCardEditAt = b.LastCardEditAt
		}
		rows = append(rows, row)
	}
	boards.mu.RUnlock()
	rows = boards.appendFailedBoardSummaries(rows, actor)
	slices.SortFunc(rows, func(a, b boardSummary) int { return strings.Compare(a.ID, b.ID) })
	data, err := marshalCanonical(rows)
	if err != nil {
		return eventState{}, err
	}
	state.catalog = fmt.Sprintf("\"%x\"", sha256.Sum256(data))
	return state, nil
}

func (state eventState) send(write func(*eventFrame) error, kind string, ids []string, previous eventState) error {
	if kind == "snapshot" || state.workspace != previous.workspace {
		if err := write(&eventFrame{Type: kind, Resource: "workspace", WorkspaceRevision: state.workspace}); err != nil {
			return err
		}
	}
	if kind == "snapshot" || state.catalog != previous.catalog {
		if err := write(&eventFrame{Type: kind, Resource: "catalog"}); err != nil {
			return err
		}
	}
	for i, id := range ids {
		if kind != "snapshot" && previous.boards[i] == state.boards[i] {
			continue
		}
		if err := write(&eventFrame{Type: kind, Resource: "board", BoardID: id, BoardRevision: state.boards[i]}); err != nil {
			return err
		}
	}
	return nil
}

func writeEvent(w http.ResponseWriter, controller *http.ResponseController, frame *eventFrame, deadline time.Time) error {
	payload := ": heartbeat\n\n"
	if frame != nil {
		data, err := json.Marshal(frame)
		if err != nil {
			return err
		}
		payload = "data: " + string(data) + "\n\n"
	}
	if err := controller.SetWriteDeadline(deadline); err != nil {
		return err
	}
	if _, err := io.WriteString(w, payload); err != nil {
		return err
	}
	if err := controller.Flush(); err != nil {
		return err
	}
	// Clear the server's ordinary response deadline while the stream is idle.
	return controller.SetWriteDeadline(time.Time{})
}

func (x *request) eventsResource() {
	if x.r.ContentLength != 0 || len(x.r.TransferEncoding) != 0 {
		_ = http.NewResponseController(x.w).SetReadDeadline(time.Now().Add(eventWriteTimeout))
		x.w.Header().Set("Connection", "close")
		x.fail(invalid("body", "Event streams do not accept a request body."))
		return
	}
	ids, err := eventBoardIDs(x.r.URL.RawQuery)
	if err != nil {
		x.fail(err)
		return
	}
	h := x.service.events
	lifecycle := &x.service.lifecycle
	lifecycle.mu.Lock()
	joined := !lifecycle.draining && h.join()
	lifecycle.mu.Unlock()
	if !joined {
		x.fail(errServiceDraining)
		return
	}
	defer h.active.Done()

	changed := h.observe()
	state, err := x.service.eventState(x.actor, ids)
	if err != nil {
		x.fail(err)
		return
	}
	controller := http.NewResponseController(x.w)
	deadline := func() time.Time {
		select {
		case <-h.stopped:
			if !h.drainUntil.IsZero() {
				return h.drainUntil
			}
		default:
		}
		return time.Now().Add(eventWriteTimeout)
	}
	defer func() {
		// Bound net/http's final chunk flush after the handler returns.
		_ = controller.SetWriteDeadline(deadline())
	}()
	write := func(frame *eventFrame) error {
		if err := x.r.Context().Err(); err != nil {
			return err
		}
		select {
		case <-h.stopped:
			if h.drainUntil.IsZero() {
				return context.Canceled
			}
		default:
		}
		return writeEvent(x.w, controller, frame, deadline())
	}
	x.w.Header().Set("Content-Type", "text/event-stream")
	if err := state.send(write, "snapshot", ids, eventState{}); err != nil {
		return
	}
	heartbeat := time.NewTicker(eventHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-x.r.Context().Done():
			return
		case <-h.stopped:
			if !h.drainUntil.IsZero() {
				select {
				case <-changed:
					if next, err := x.service.eventState(x.actor, ids); err == nil {
						_ = next.send(write, "change", ids, state)
					}
				default:
				}
			}
			return
		case <-changed:
			changed = h.observe()
			next, err := x.service.eventState(x.actor, ids)
			if err != nil || next.send(write, "change", ids, state) != nil {
				return
			}
			state = next
		case <-heartbeat.C:
			if err := write(nil); err != nil {
				return
			}
		}
	}
}
