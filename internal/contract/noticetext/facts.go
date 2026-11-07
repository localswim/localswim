// Package noticetext defines the bounded, state-free original notice format.
package noticetext

import (
	"errors"
	"unicode/utf8"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const (
	MaxLabelBytes = 96
	MaxTextBytes  = 512
	MaxTickets    = 32
)

var ErrInvalid = errors.New("invalid notice text")

// NoticeTextFacts contains only admitted metadata, never card prose. An empty
// non-nil ticket slice represents a proved non-card interval.
type NoticeTextFacts struct {
	Label   string       `json:"label"`
	From    string       `json:"from"`
	Through string       `json:"through"`
	Tickets []A.Positive `json:"tickets"`
}

// ValidateFacts checks the fixed scalar policy. Formatting additionally enforces
// the complete text byte budget; valid facts need not fit a complete payload.
func ValidateFacts(f NoticeTextFacts) error {
	if !validLabel(f.Label) || A.Validate(A.Timestamp(f.From)) != nil ||
		A.Validate(A.Timestamp(f.Through)) != nil || f.From > f.Through ||
		f.Tickets == nil || len(f.Tickets) > MaxTickets {
		return ErrInvalid
	}
	var prior A.Positive
	for _, ticket := range f.Tickets {
		if A.Validate(ticket) != nil || ticket <= prior {
			return ErrInvalid
		}
		prior = ticket
	}
	return nil
}

// SafeLabel freezes an allowed configured label exactly or chooses an explicit
// identity label. An invalid identity cannot manufacture a usable label.
func SafeLabel(label string, boardID A.UUID) string {
	if A.Validate(boardID) != nil {
		return ""
	}
	if validLabel(label) {
		return label
	}
	return "Board " + string(boardID)
}

func validLabel(s string) bool {
	if len(s) == 0 || len(s) > MaxLabelBytes || !utf8.ValidString(s) {
		return false
	}
	nonblank := false
	for _, r := range s {
		if r <= 0x1f || r >= 0x7f && r <= 0x9f || r == 0x2028 || r == 0x2029 ||
			r == 0x061c || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e ||
			r >= 0x2066 && r <= 0x2069 || r == 0xfeff {
			return false
		}
		if !fixedWhitespace(r) {
			nonblank = true
		}
	}
	return nonblank
}

func fixedWhitespace(r rune) bool {
	return r >= 0x09 && r <= 0x0d || r == 0x20 || r == 0x85 || r == 0xa0 ||
		r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 ||
		r == 0x202f || r == 0x205f || r == 0x3000
}

// CloneFacts returns owned slices, preserving the distinction between null and
// a proved empty set. No caller receives a mutable view into retained facts.
func CloneFacts(f *NoticeTextFacts) *NoticeTextFacts {
	if f == nil {
		return nil
	}
	out := *f
	if f.Tickets != nil {
		out.Tickets = make([]A.Positive, len(f.Tickets))
		copy(out.Tickets, f.Tickets)
	}
	return &out
}
