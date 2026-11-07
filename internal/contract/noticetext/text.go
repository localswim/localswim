package noticetext

import (
	"slices"
	"strconv"
	"strings"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const FallbackText = "Inspect the selected board for changes."

// NoticeText is an original captured payload. Field order also forms part of
// the private version-2 intent identity's Go struct JSON encoding.
type NoticeText struct {
	Version A.Version        `json:"version"`
	Kind    string           `json:"kind"`
	Facts   *NoticeTextFacts `json:"facts"`
	Text    string           `json:"text"`
}

// Build formats once from owned admitted facts. Incomplete or oversized facts
// select the whole fallback, never a truncated claim of complete tickets.
func Build(facts *NoticeTextFacts) NoticeText {
	if facts != nil && ValidateFacts(*facts) == nil {
		text := completeText(*facts)
		if len(text) <= MaxTextBytes {
			return NoticeText{1, "complete", CloneFacts(facts), text}
		}
	}
	return NoticeText{1, "fallback", nil, FallbackText}
}

func completeText(f NoticeTextFacts) string {
	var b strings.Builder
	b.WriteString("Board: ")
	b.WriteString(f.Label)
	b.WriteString("\nObserved: ")
	b.WriteString(f.From)
	b.WriteString(" to ")
	b.WriteString(f.Through)
	b.WriteString("\nTickets: ")
	if len(f.Tickets) == 0 {
		b.WriteString("none")
	}
	for i, ticket := range f.Tickets {
		if i != 0 {
			b.WriteString(", ")
		}
		b.WriteByte('#')
		b.WriteString(strconv.FormatUint(uint64(ticket), 10))
	}
	return b.String()
}

// Validate refuses noncanonical saved text; it never repairs or regenerates a
// caller's payload. Legacy absence is handled by the enclosing record contract.
func Validate(p NoticeText) error {
	if p.Version != 1 || len(p.Text) > MaxTextBytes {
		return ErrInvalid
	}
	switch p.Kind {
	case "fallback":
		if p.Facts == nil && p.Text == FallbackText {
			return nil
		}
	case "complete":
		if p.Facts != nil && ValidateFacts(*p.Facts) == nil && p.Text == completeText(*p.Facts) {
			return nil
		}
	}
	return ErrInvalid
}

// Decode applies the shared strict JSON boundary before payload semantics.
func Decode(raw []byte) (NoticeText, error) {
	p, err := A.Decode[NoticeText](raw)
	if err != nil {
		return NoticeText{}, ErrInvalid
	}
	if err = Validate(p); err != nil {
		return NoticeText{}, err
	}
	return p, nil
}

// Clone preserves exact data while giving the caller ownership of all slices.
func Clone(p *NoticeText) *NoticeText {
	if p == nil {
		return nil
	}
	out := *p
	out.Facts = CloneFacts(p.Facts)
	return &out
}

// Equal compares complete captured content, including null versus empty facts.
func Equal(a, b *NoticeText) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Version != b.Version || a.Kind != b.Kind || a.Text != b.Text {
		return false
	}
	af, bf := a.Facts, b.Facts
	if af == nil || bf == nil {
		return af == bf
	}
	return af.Label == bf.Label && af.From == bf.From && af.Through == bf.Through &&
		(af.Tickets == nil) == (bf.Tickets == nil) && slices.Equal(af.Tickets, bf.Tickets)
}
