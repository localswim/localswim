package v2

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	ownerNoticeQuietPeriod = 15 * time.Second
	ownerNoticeTimeout     = 5 * time.Second
	maxOwnerNoticeCards    = 128
)

type activityOperationSet uint8

func (group activityOperationGroup) bit() activityOperationSet {
	return 1 << group
}

type ownerNoticeCard struct {
	activityCard
	operations activityOperationSet
}

type ownerNoticeEvent struct {
	boardID   string
	label     string
	writerID  string
	since     time.Time
	until     time.Time
	cards     []ownerNoticeCard
	truncated bool
}

func (event *ownerNoticeEvent) addCard(card ownerNoticeCard) {
	index, present := slices.BinarySearchFunc(event.cards, card, func(a, b ownerNoticeCard) int {
		return compareActivityCards(a.activityCard, b.activityCard)
	})
	if present {
		event.cards[index].operations |= card.operations
		return
	}
	if len(event.cards) == maxOwnerNoticeCards {
		event.truncated = true
		if index == maxOwnerNoticeCards {
			return
		}
		event.cards = event.cards[:maxOwnerNoticeCards-1]
	}
	event.cards = slices.Insert(event.cards, index, card)
}

func (event ownerNoticeEvent) message() string {
	var message strings.Builder
	message.WriteString("localswim board-change notice\n\n")
	fmt.Fprintf(&message, "- Board: %q (`%s`)\n", event.label, event.boardID)
	fmt.Fprintf(&message, "- Writer: `%s`\n", event.writerID)
	fmt.Fprintf(&message, "- Start: %s\n- End: %s\n",
		canonicalTime(event.since).Format(timestampLayout), canonicalTime(event.until).Format(timestampLayout))
	message.WriteString("- Cards: ")
	var groups activityOperationSet
	for index, card := range event.cards {
		groups |= card.operations
		if index > 0 {
			message.WriteString(", ")
		}
		fmt.Fprintf(&message, "%04d (`%s`)", card.Ticket, card.CardID)
	}
	if event.truncated {
		message.WriteString(" (additional card and ticket pairs omitted)")
	}
	message.WriteString("\n- Operations: ")
	for group, label := range activityOperationLabels {
		bit := activityOperationGroup(group).bit()
		if groups&bit == 0 {
			continue
		}
		fmt.Fprintf(&message, "%s (", label)
		event.writeTickets(&message, bit)
		message.WriteByte(')')
		groups &^= bit
		if groups != 0 {
			message.WriteString(", ")
		}
	}
	message.WriteString("\n- Tickets: ")
	event.writeTickets(&message, ^activityOperationSet(0))
	return message.String()
}

func (event ownerNoticeEvent) writeTickets(message *strings.Builder, groups activityOperationSet) {
	var previous uint64
	for _, card := range event.cards {
		if card.Ticket == previous || card.operations&groups == 0 {
			continue
		}
		if previous != 0 {
			message.WriteString(", ")
		}
		fmt.Fprintf(message, "%04d", card.Ticket)
		previous = card.Ticket
	}
}

type pendingOwnerNotice struct {
	event    ownerNoticeEvent
	deadline time.Time
}

// The sole writer supplies metadata after durable commit. Queue work and
// settings reads run outside the worker's pending lock and command admission.
type ownerNoticeWorker struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	wake    chan struct{}
	quiet   time.Duration
	load    func(string) (notifications, bool)
	deliver func(context.Context, string, string) bool

	writerID string
	mu       sync.Mutex
	pending  map[string]*pendingOwnerNotice
}

func newOwnerNoticeWorker(quiet time.Duration, load func(string) (notifications, bool), deliver func(context.Context, string, string) bool) *ownerNoticeWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &ownerNoticeWorker{
		ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
		quiet: quiet, load: load, deliver: deliver, pending: make(map[string]*pendingOwnerNotice),
	}
	go worker.run()
	return worker
}

func (worker *ownerNoticeWorker) close() {
	worker.cancel()
	<-worker.done
	worker.mu.Lock()
	defer worker.mu.Unlock()
	clear(worker.pending)
}

func ownerNoticeEnabled(settings notifications) bool {
	return settings.Enabled && settings.TargetThreadID != nil
}

func (worker *ownerNoticeWorker) submitCommitted(previous, committed snapshot) {
	if worker.ctx.Err() != nil || previous.Board.ID != committed.Board.ID {
		return
	}
	before, after := previous.Board.Notifications, committed.Board.Notifications
	enabled := ownerNoticeEnabled(after)
	if before.Enabled != after.Enabled || !sameText(before.TargetThreadID, after.TargetThreadID) {
		worker.settingsChanged(committed.Board.ID, enabled)
	}
	if !enabled {
		return
	}
	event := committedOwnerNotice(previous, committed)
	if len(event.cards) == 0 {
		return
	}
	event.writerID = worker.writerID
	worker.submit(event)
}

func (worker *ownerNoticeWorker) settingsChanged(boardID string, enabled bool) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	previous := worker.pending[boardID]
	if previous == nil {
		return
	}
	if enabled {
		// Retargeting keeps the burst and deadline, but invalidates any settings
		// read already in progress for this generation.
		next := *previous
		worker.pending[boardID] = &next
	} else {
		delete(worker.pending, boardID)
	}
	worker.signal()
}

func committedOwnerNotice(previous, committed snapshot) ownerNoticeEvent {
	first := len(previous.Activity)
	if first >= len(committed.Activity) || committed.Board.Revision != previous.Board.Revision+1 {
		return ownerNoticeEvent{}
	}
	newActivity := committed.Activity[first:]
	// Snapshot validation guarantees a single actor per committed revision.
	actor := newActivity[0].ActorID
	if !slices.ContainsFunc(committed.Users, func(member user) bool {
		return member.ID == actor && member.Kind == "human"
	}) {
		return ownerNoticeEvent{}
	}
	index := indexActivity(committed)
	event := ownerNoticeEvent{boardID: committed.Board.ID, label: committed.Board.Name}
	for i, audit := range newActivity {
		if audit.Revision != committed.Board.Revision {
			continue
		}
		cards := index.cardReferences(audit)
		if len(cards) == 0 {
			continue
		}
		group := classifyActivityOperation(audit, i > 0 && isApprovalComment(newActivity[i-1], audit))
		for _, card := range cards {
			event.addCard(ownerNoticeCard{activityCard: card, operations: group.bit()})
		}
		at := canonicalTime(audit.At)
		if event.since.IsZero() || at.Before(event.since) {
			event.since = at
		}
		if event.until.IsZero() || at.After(event.until) {
			event.until = at
		}
	}
	return event
}

func (worker *ownerNoticeWorker) submit(event ownerNoticeEvent) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.ctx.Err() != nil {
		return
	}
	if previous, ok := worker.pending[event.boardID]; ok {
		event.truncated = event.truncated || previous.event.truncated
		for _, card := range previous.event.cards {
			event.addCard(card)
		}
		if previous.event.since.Before(event.since) {
			event.since = previous.event.since
		}
		if previous.event.until.After(event.until) {
			event.until = previous.event.until
		}
	}
	worker.pending[event.boardID] = &pendingOwnerNotice{event: event, deadline: time.Now().Add(worker.quiet)}
	worker.signal()
}

func (worker *ownerNoticeWorker) signal() {
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *ownerNoticeWorker) next() (*pendingOwnerNotice, time.Duration) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	var next *pendingOwnerNotice
	for _, item := range worker.pending {
		if next == nil || item.deadline.Before(next.deadline) {
			next = item
		}
	}
	if next == nil {
		return nil, 0
	}
	return next, time.Until(next.deadline)
}

func (worker *ownerNoticeWorker) claim(candidate *pendingOwnerNotice) bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	boardID := candidate.event.boardID
	if worker.ctx.Err() != nil || worker.pending[boardID] != candidate {
		return false
	}
	delete(worker.pending, boardID)
	return true
}

func (worker *ownerNoticeWorker) run() {
	defer close(worker.done)
	for worker.ctx.Err() == nil {
		next, wait := worker.next()
		var timer *time.Timer
		var elapsed <-chan time.Time
		if next != nil {
			if wait <= 0 {
				settings, ok := worker.load(next.event.boardID)
				// A concurrent edit may extend the quiet period, and settings may
				// discard or retarget this burst while the read is in progress.
				if worker.claim(next) && ok && ownerNoticeEnabled(settings) {
					worker.deliver(worker.ctx, *settings.TargetThreadID, next.event.message())
				}
				continue
			}
			timer = time.NewTimer(wait)
			elapsed = timer.C
		}
		select {
		case <-worker.ctx.Done():
		case <-worker.wake:
		case <-elapsed:
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

func deliverOwnerNotice(ctx context.Context, target, message string) (accepted bool) {
	if ctx.Err() != nil {
		return false
	}
	defer func() {
		if accepted {
			fmt.Fprintln(os.Stderr, "localswim owner notice: queue accepted")
		} else {
			fmt.Fprintln(os.Stderr, "localswim owner notice: queue failed")
		}
	}()
	if !threadIDPattern.MatchString(target) {
		return false
	}
	executable, err := exec.LookPath(ownerNoticeExecutable)
	if err != nil || !filepath.IsAbs(executable) ||
		!strings.EqualFold(filepath.Base(executable), ownerNoticeExecutable) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, ownerNoticeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "queue", "--thread", target, "--message", message)
	command.Env = slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return strings.EqualFold(key, "LOCALSWIM_HUMAN_TOKEN") || strings.EqualFold(key, "LOCALSWIM_BOT_TOKEN")
	})
	configureOwnerNoticeCommand(command)
	// The nil streams use the null device. Never surface CLI output or errors.
	return command.Run() == nil
}
