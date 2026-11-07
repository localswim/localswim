package transport

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	P "github.com/localswim/localswim/internal/contract/noticetext"
)

func seamResponse(payload *P.NoticeText) NoticeResponse {
	target := UUIDv4("10000000-0000-4000-8000-000000000003")
	upper, count, checkpoint := A.Number(9007199254740991), A.Number(1000), A.Number(9007199254740991)
	digest := A.Digest(strings.Repeat("f", 64))
	cut := NoticeCut{target, "unknown-actor", upper - count, &upper, &count, &digest}
	response := NoticeResponse{APIVersion: 1, Scope: "notices", Result: NoticeView{
		Identity:          A.Identity{BoardID: "10000000-0000-4000-8000-000000000001", Incarnation: "10000000-0000-4000-8000-000000000002"},
		ControlGeneration: "10000000-0000-4000-8000-000000000004", ConfigurationRevision: 9007199254740991,
		ControlOperation: func() *NoticeOperation { x := NoticeOperation("configure"); return &x }(),
		TargetID:         &target, State: "partial-drain", Reason: func() *NoticeReason { x := NoticeReason("unavailable"); return &x }(), Checkpoint: &checkpoint, GapCount: 64, Last: &cut, Pending: &cut, Entries: []NoticePair{},
	}}
	for i := 0; i < NoticeInboxLimit; i++ {
		response.Result.Entries = append(response.Result.Entries, NoticePair{
			TargetID: target, DeliveryID: A.Digest(fmt.Sprintf("%064x", i+1)), AcceptedAt: "9999-12-31T23:59:59Z", Class: "unknown-actor", LowerEvent: upper - count, UpperEvent: &upper, EventCount: &count, Payload: P.Clone(payload),
		})
	}
	pair := response.Result.Entries[0]
	response.Result.Lookup = &NoticeLookup{State: "accepted", Receipt: &pair}
	return response
}

func seamComplete() P.NoticeText {
	f := P.NoticeTextFacts{Label: strings.Repeat(`\`, 96), From: "0001-01-01T00:00:00Z", Through: "9999-12-31T23:59:59Z", Tickets: []A.Positive{}}
	// Ascending tickets with a mix of decimal widths make a 512-byte canonical
	// complete original while retaining all 32 facts and worst-case label escaping.
	for i := 0; i < 32; i++ {
		f.Tickets = append(f.Tickets, A.Positive(i+1))
	}
	for i := 31; i >= 0; i-- {
		for digits := 16; digits >= 1; digits-- {
			n := A.Positive(1)
			for d := 1; d < digits; d++ {
				n *= 10
			}
			n += A.Positive(i)
			if i < 31 && n >= f.Tickets[i+1] {
				continue
			}
			old := f.Tickets[i]
			f.Tickets[i] = n
			p := P.Build(&f)
			if p.Kind == "complete" {
				break
			}
			f.Tickets[i] = old
		}
	}
	p := P.Build(&f)
	// Fill any residual byte budget by changing the smallest ticket's width.
	return p
}

func TestNoticePayloadStrictBoundaryAndEncoding(t *testing.T) {
	complete := P.Build(&P.NoticeTextFacts{Label: "Original", From: "2026-11-07T00:00:00Z", Through: "2026-11-07T00:00:01Z", Tickets: []A.Positive{1, 2}})
	response := seamResponse(&complete)
	wire, err := Readable(response)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := A.Canonical(response)
	if !bytes.Equal(wire, canonical) || bytes.HasSuffix(wire, []byte("\n")) {
		t.Fatal("notice emission must be exact D02 C")
	}
	pretty, _ := A.Readable(response)
	if _, err := Decode[NoticeResponse](pretty); err != nil {
		t.Fatal("R decoding changed", err)
	}
	if _, err := Decode[NoticeResponse](wire); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(string) string{
		"unknown": func(s string) string {
			return strings.Replace(s, `"kind":"complete"`, `"extra":null,"kind":"complete"`, 1)
		},
		"duplicate": func(s string) string {
			return strings.Replace(s, `"kind":"complete"`, `"kind":"complete","kind":"complete"`, 1)
		},
		"version":       func(s string) string { return strings.Replace(s, `"version":1`, `"version":2`, 1) },
		"fraction":      func(s string) string { return strings.Replace(s, `"version":1`, `"version":1.0`, 1) },
		"missing-facts": func(s string) string { return strings.Replace(s, `"facts":{`, `"unrecognized":{`, 1) },
		"changed-text":  func(s string) string { return strings.Replace(s, `Board: Original`, `Board: Changed`, 1) },
		"surrogate":     func(s string) string { return strings.Replace(s, `Original`, `\ud800`, 1) },
		"bad-tickets":   func(s string) string { return strings.Replace(s, `"tickets":[1,2]`, `"tickets":[2,1]`, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := []byte(mutate(string(wire)))
			if bytes.Equal(bad, wire) {
				t.Fatal("fixture unchanged")
			}
			if _, err := Decode[NoticeResponse](bad); err == nil {
				t.Fatal("invalid payload admitted")
			}
		})
	}
	for _, class := range []NoticeClass{"expired", "gapped", "observed-empty", "unavailable"} {
		pair := response.Result.Entries[0]
		pair.Class = class
		pair.EventCount = nil
		if class == "observed-empty" {
			zero := A.Number(0)
			pair.EventCount = &zero
			pair.LowerEvent = *pair.UpperEvent
		}
		if Validate(pair) == nil {
			t.Fatal("complete loss/empty admitted", class)
		}
		fallback := P.Build(nil)
		pair.Payload = &fallback
		if err := Validate(pair); err != nil {
			t.Fatal(class, err)
		}
		pair.Payload = nil
		if err := Validate(pair); err != nil {
			t.Fatal("legacy refused", class, err)
		}
	}
	legacy := seamResponse(nil)
	b, err := Readable(legacy)
	if err != nil {
		t.Fatal(err)
	}
	null := bytes.Replace(b, []byte(`"acceptedAt":`), []byte(`"payload":null,"acceptedAt":`), 1)
	if _, err := Decode[NoticeResponse](null); err != nil {
		t.Fatal("legacy null refused", err)
	}
	// Only NoticeResponse changes emission; requests retain final-LF R bytes.
	request := NoticeControlRequest{Incarnation: response.Result.Identity.Incarnation, ExpectedControlGeneration: response.Result.ControlGeneration, ExpectedConfigurationRevision: 1}
	got, err := Readable(request)
	want, _ := A.Readable(request)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("other R policy changed", err)
	}
}

func TestNoticeResponseMaximumCompactCapacity(t *testing.T) {
	complete, fallback := seamComplete(), P.Build(nil)
	if complete.Kind != "complete" || len(complete.Facts.Tickets) != 32 || len(complete.Facts.Label) != 96 || len(complete.Text) != 512 {
		t.Fatalf("maximum complete fixture: kind=%s text=%d", complete.Kind, len(complete.Text))
	}
	for name, p := range map[string]*P.NoticeText{"complete": &complete, "fallback": &fallback, "legacy": nil} {
		t.Run(name, func(t *testing.T) {
			response := seamResponse(p)
			wire, err := Readable(response)
			if err != nil {
				t.Fatal(err)
			}
			if len(wire) > NoticeResponseLimit {
				t.Fatal("cap exceeded")
			}
			decoded, err := Decode[NoticeResponse](wire)
			if err != nil || len(decoded.Result.Entries) != 64 || decoded.Result.Lookup.Receipt == nil {
				t.Fatal("full response lost pairs", err)
			}
			t.Logf("%s compact bytes=%d", name, len(wire))
			// Explicit opt-in exports synthetic Go-encoded seams for production TS checks.
			if root := os.Getenv("M15_SEAM_FIXTURE_DIR"); root != "" {
				if err := os.MkdirAll(root, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "go-maximum-"+name+".json"), wire, 0600); err != nil {
					t.Fatal(err)
				}
				for _, action := range []string{"inbox", "lookup", "status"} {
					shaped := response
					if action != "lookup" {
						shaped.Result.Lookup = nil
					}
					if action != "inbox" {
						shaped.Result.Entries = []NoticePair{}
					}
					encoded, err := Readable(shaped)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(root, "go-maximum-"+name+"-"+action+".json"), encoded, 0600); err != nil {
						t.Fatal(err)
					}
					t.Logf("%s %s bytes=%d", name, action, len(encoded))
				}
			}
		})
	}
	// Independent-field upper bound: real complete facts may not combine 32
	// sixteen-digit tickets and 512-byte text, but each field is individually at
	// its largest allowed JSON representation. Even that overestimate fits 128KiB.
	upper := P.NoticeText{Version: 1, Kind: "complete", Facts: &P.NoticeTextFacts{Label: strings.Repeat(`\`, 96), From: "0001-01-01T00:00:00Z", Through: "9999-12-31T23:59:59Z", Tickets: []A.Positive{}}, Text: strings.Repeat(`\`, 96) + "\n\n" + strings.Repeat("x", 414)}
	for i := 0; i < 32; i++ {
		upper.Facts.Tickets = append(upper.Facts.Tickets, A.Positive(9007199254740960+i))
	}
	bound, err := A.Canonical(seamResponse(&upper))
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) > NoticeResponseLimit {
		t.Fatalf("conservative bound=%d", len(bound))
	}
	t.Logf("independent field upper bound bytes=%d", len(bound))
}
