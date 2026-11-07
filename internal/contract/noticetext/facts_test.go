package noticetext

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func validFacts() NoticeTextFacts {
	return NoticeTextFacts{"Alpha", "2026-11-07T10:00:00Z", "2026-11-07T10:00:02Z", []A.Positive{1, 2}}
}

func TestFactsScalarPolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*NoticeTextFacts)
	}{
		{"null-tickets", func(f *NoticeTextFacts) { f.Tickets = nil }},
		{"duplicate", func(f *NoticeTextFacts) { f.Tickets = []A.Positive{1, 1} }},
		{"unsorted", func(f *NoticeTextFacts) { f.Tickets = []A.Positive{2, 1} }},
		{"zero", func(f *NoticeTextFacts) { f.Tickets = []A.Positive{0} }},
		{"unsafe-integer", func(f *NoticeTextFacts) { f.Tickets = []A.Positive{9007199254740992} }},
		{"too-many", func(f *NoticeTextFacts) {
			f.Tickets = make([]A.Positive, 33)
			for i := range f.Tickets {
				f.Tickets[i] = A.Positive(i + 1)
			}
		}},
		{"year-zero", func(f *NoticeTextFacts) { f.From = "0000-01-01T00:00:00Z" }},
		{"invalid-date", func(f *NoticeTextFacts) { f.From = "2026-02-30T00:00:00Z" }},
		{"fraction", func(f *NoticeTextFacts) { f.From = "2026-11-07T10:00:00.0Z" }},
		{"offset", func(f *NoticeTextFacts) { f.From = "2026-11-07T10:00:00+00:00" }},
		{"reversed", func(f *NoticeTextFacts) { f.From = "2026-11-07T10:00:03Z" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := validFacts()
			tc.edit(&f)
			if ValidateFacts(f) == nil {
				t.Fatal("accepted invalid facts")
			}
		})
	}
	f := validFacts()
	f.From, f.Through = "0001-01-01T00:00:00Z", "9999-12-31T23:59:59Z"
	f.Tickets = []A.Positive{9007199254740991}
	if err := ValidateFacts(f); err != nil {
		t.Fatal(err)
	}
	f.Tickets = []A.Positive{}
	if err := ValidateFacts(f); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenLabelPolicy(t *testing.T) {
	id := A.UUID("00000000-0000-4000-8000-000000000001")
	for _, label := range []string{" Alpha ", "多言語 🏊", strings.Repeat("é", 48), "a\u200bb"} {
		if got := SafeLabel(label, id); got != label {
			t.Fatalf("valid label altered: %q", got)
		}
	}
	unsafe := []string{"", " \u00a0\u1680\u2000\u202f\u205f\u3000", strings.Repeat("é", 49), string([]byte{0xff})}
	for _, r := range []rune{0, 9, 10, 31, 127, 128, 159, 0x2028, 0x2029, 0x61c, 0x200e, 0x200f, 0x202a, 0x202e, 0x2066, 0x2069, 0xfeff} {
		unsafe = append(unsafe, "a"+string(r)+"b")
	}
	for _, label := range unsafe {
		if SafeLabel(label, id) != "Board "+string(id) {
			t.Fatal("unsafe label did not select identity")
		}
	}
	if SafeLabel("Alpha", "invalid") != "" {
		t.Fatal("invalid identity became label")
	}
}

func TestCloneFactsOwnership(t *testing.T) {
	f := validFacts()
	clone := CloneFacts(&f)
	clone.Tickets[0] = 42
	if f.Tickets[0] != 1 {
		t.Fatal("slice alias")
	}
	f.Tickets = []A.Positive{}
	if CloneFacts(&f).Tickets == nil || CloneFacts(nil) != nil {
		t.Fatal("null/empty changed")
	}
}

func TestSharedCompleteFacts(t *testing.T) {
	b, err := os.ReadFile("../../../fixtures/contracts/notice-text/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		BuildCases []struct {
			ID       string           `json:"id"`
			Facts    *NoticeTextFacts `json:"facts"`
			Expected struct {
				Kind string `json:"kind"`
			} `json:"expected"`
		} `json:"buildCases"`
	}
	if err = json.Unmarshal(b, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors.BuildCases {
		if v.Expected.Kind == "complete" && (v.Facts == nil || ValidateFacts(*v.Facts) != nil) {
			t.Fatalf("complete vector rejected: %s", v.ID)
		}
	}
}
