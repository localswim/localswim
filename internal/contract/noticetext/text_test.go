package noticetext

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
)

func TestSharedTextVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../fixtures/contracts/notice-text/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		BuildCases []struct {
			ID       string           `json:"id"`
			Facts    *NoticeTextFacts `json:"facts"`
			Expected NoticeText       `json:"expected"`
		} `json:"buildCases"`
		RejectPayloads []struct {
			ID      string          `json:"id"`
			Payload json.RawMessage `json:"payload"`
		} `json:"rejectPayloads"`
	}
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.BuildCases) < 24 || len(vectors.RejectPayloads) < 8 {
		t.Fatal("shared vector set lost required coverage")
	}
	for _, v := range vectors.BuildCases {
		t.Run(v.ID, func(t *testing.T) {
			got := Build(v.Facts)
			if !Equal(&got, &v.Expected) {
				t.Fatal("canonical output differs from shared vector")
			}
			if Validate(got) != nil || strings.HasSuffix(got.Text, "\n") {
				t.Fatal("invalid or newline-terminated output")
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(encoded)
			if err != nil || !Equal(&got, &decoded) {
				t.Fatal("strict round-trip changed payload")
			}
		})
	}
	for _, v := range vectors.RejectPayloads {
		t.Run(v.ID, func(t *testing.T) {
			if _, err := Decode(v.Payload); err == nil {
				t.Fatal("accepted rejecting shared vector")
			}
		})
	}
}

func TestTextExactByteBoundaryAndNoTruncation(t *testing.T) {
	f := validFacts()
	f.Label = strings.Repeat("A", 81)
	f.Tickets = make([]A.Positive, 19)
	for i := range f.Tickets {
		f.Tickets[i] = A.Positive(9007199254740973 + i)
	}
	p := Build(&f)
	if p.Kind != "complete" || len(p.Text) != 512 || Validate(p) != nil {
		t.Fatal("exact 512-byte payload refused")
	}
	f.Label += "A"
	p = Build(&f)
	if p.Kind != "fallback" || p.Facts != nil || p.Text != FallbackText {
		t.Fatal("over-budget complete text truncated or admitted")
	}
	f.Label = strings.Repeat("A", 80) + "é"
	if p = Build(&f); p.Kind != "fallback" {
		t.Fatal("text budget counted runes instead of UTF-8 bytes")
	}
}

func TestTextOwnershipAndExactComparison(t *testing.T) {
	f := validFacts()
	p := Build(&f)
	f.Tickets[0], f.Label = 99, "Renamed"
	if p.Facts.Tickets[0] != 1 || p.Facts.Label != "Alpha" {
		t.Fatal("Build retained input aliases")
	}
	clone := Clone(&p)
	if !Equal(&p, clone) {
		t.Fatal("equal values compared by pointer")
	}
	clone.Facts.Tickets[0] = 99
	if p.Facts.Tickets[0] != 1 || Equal(&p, clone) {
		t.Fatal("clone aliased or unequal tickets accepted")
	}
	if Clone(nil) != nil || !Equal(nil, nil) || Equal(&p, nil) {
		t.Fatal("nil semantics")
	}
	clone = Clone(&p)
	clone.Text += "\n"
	if Equal(&p, clone) || Validate(*clone) == nil {
		t.Fatal("changed bytes accepted")
	}
	clone = Clone(&p)
	clone.Facts.Label = "Renamed"
	if Equal(&p, clone) || Validate(*clone) == nil {
		t.Fatal("changed facts accepted")
	}
	empty := Build(&NoticeTextFacts{"Alpha", f.From, f.Through, []A.Positive{}})
	clone = Clone(&empty)
	clone.Facts.Tickets = nil
	if Equal(&empty, clone) || Validate(*clone) == nil {
		t.Fatal("null tickets equal proved empty")
	}
}

func TestTextStrictDecoderDoesNotRepair(t *testing.T) {
	p := Build(nil)
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		[]byte(strings.Replace(string(raw), `"version":1`, `"version":1,"version":1`, 1)),
		[]byte(strings.Replace(string(raw), `"version":1,`, ``, 1)),
		[]byte(strings.Replace(string(raw), `"version":1`, `"version":1.0`, 1)),
		[]byte(strings.Replace(string(raw), `"facts":null,`, ``, 1)),
		[]byte(strings.Replace(string(raw), `"kind":"fallback"`, `"kind":"other"`, 1)),
		[]byte(strings.Replace(string(raw), `"text":"`, `"text":"\ud800`, 1)),
		append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"extra":null}`)...),
		[]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
	} {
		if _, err := Decode(bad); err == nil {
			t.Fatal("decoder accepted malformed/noncanonical payload")
		}
	}
}
