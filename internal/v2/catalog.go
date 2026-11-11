package v2

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"
)

const durableStateUnavailable = "durable_state_unavailable"

type boardSummary struct {
	ID             string     `json:"id"`
	Name           *string    `json:"name"`
	ETag           *string    `json:"etag"`
	Availability   string     `json:"availability"`
	Attention      *attention `json:"attention"`
	LastCardEditAt *time.Time `json:"last_card_edit_at"`
	ErrorCode      *string    `json:"error_code"`
}

func (s *store) canSeeFailedBoard(id, actor string) bool {
	_, failed := s.failedBoards[id]
	return actor == humanID && failed
}

func (s *store) appendFailedBoardSummaries(rows []boardSummary, actor string) []boardSummary {
	// A failed snapshot cannot establish board membership. The diagnostic is
	// visible to the Human workspace administrator.
	if actor == humanID {
		for id := range s.failedBoards {
			code := durableStateUnavailable
			rows = append(rows, boardSummary{ID: id, Availability: "unhealthy", ErrorCode: &code})
		}
	}
	return rows
}

func deriveAttention(state snapshot) (attention, *time.Time) {
	roles := make(map[string]string, len(state.Lanes))
	for _, lane := range state.Lanes {
		roles[lane.ID] = lane.Role
	}

	var counts attention
	for _, card := range state.Cards {
		if card.Archived {
			continue
		}
		switch roles[card.LaneID] {
		case "blocked":
			counts.Blocked++
		case "needs_input":
			counts.NeedsInput++
		case "ready_for_review":
			counts.ReadyForReview++
		}
	}

	var latest *time.Time
	for i := range state.Activity {
		event := &state.Activity[i]
		// These child resources belong to cards, including when the audit records a removal.
		switch event.ResourceType {
		case "card", "comment", "attachment", "relationship":
		default:
			continue
		}
		if latest == nil || event.At.After(*latest) {
			latest = &event.At
		}
	}
	if latest == nil {
		return counts, nil
	}
	at := canonicalTime(*latest)
	return counts, &at
}

func (s *store) boardSummary(id, actor string) (boardSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	current, ok := s.boards[id]
	if !ok || !current.can(actor, "read") {
		return boardSummary{}, false
	}
	return summarize(current, s.checkAcceptedDurableLocked(current)), true
}

func summarize(state snapshot, durableErr error) boardSummary {
	board := state.Board
	result := boardSummary{ID: board.ID, Availability: board.Availability}
	if board.Name != "" {
		result.Name = &board.Name
	}
	if board.ETag != "" {
		result.ETag = &board.ETag
	}

	if durableErr != nil {
		code := durableStateUnavailable
		if errors.Is(durableErr, errDurableStateChanged) {
			code = "durable_state_changed"
		}
		result.Availability = "unhealthy"
		result.ErrorCode = &code
	} else if board.Availability == "open" {
		counts, latest := deriveAttention(state)
		result.Attention = &counts
		result.LastCardEditAt = latest
	}
	return result
}

func sortSummaries(boards []boardSummary) {
	slices.SortFunc(boards, func(a, b boardSummary) int {
		switch {
		case a.LastCardEditAt == nil && b.LastCardEditAt != nil:
			return 1
		case a.LastCardEditAt != nil && b.LastCardEditAt == nil:
			return -1
		case a.LastCardEditAt != nil && b.LastCardEditAt != nil:
			if order := b.LastCardEditAt.Compare(*a.LastCardEditAt); order != 0 {
				return order
			}
		}
		if order := cmp.Compare(attentionTotal(b.Attention), attentionTotal(a.Attention)); order != 0 {
			return order
		}
		if order := cmp.Compare(summaryName(a.Name), summaryName(b.Name)); order != 0 {
			return order
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

func attentionTotal(counts *attention) int {
	if counts == nil {
		return 0
	}
	return counts.Blocked + counts.NeedsInput + counts.ReadyForReview
}

func summaryName(name *string) string {
	if name == nil {
		return ""
	}
	return strings.ToLower(*name)
}
