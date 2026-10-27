package nativequalification

import (
	"bufio"
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

//go:embed inventory.json
var inventoryBytes []byte

type inventory struct {
	Transaction      []string `json:"transaction"`
	TransactionChild []string `json:"transactionChild"`
	Archive          []string `json:"archive"`
	ArchiveChild     []string `json:"archiveChild"`
	ArchiveRows      []string `json:"archiveRows"`
}

type coverage struct {
	Required []string `json:"required"`
	Passed   []string `json:"passed"`
}

// parseReport accepts Go's JSON event stream only when the exact mandatory test
// set and package each finish successfully. Any skip, failure, duplicate terminal
// event, malformed record or unexpected test fails closed.
func parseReport(raw []byte, required []string) (coverage, error) {
	result := coverage{Required: required}
	want := map[string]bool{}
	for _, id := range required {
		if id == "" || want[id] {
			return result, fmt.Errorf("invalid required inventory")
		}
		want[id] = true
	}
	if len(want) == 0 {
		return result, fmt.Errorf("empty required inventory")
	}
	passed := map[string]bool{}
	packagePass := 0
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 4096), 16<<20)
	for s.Scan() {
		var event struct{ Action, Test string }
		if json.Unmarshal(s.Bytes(), &event) != nil {
			return result, fmt.Errorf("malformed report")
		}
		if event.Action == "" {
			return result, fmt.Errorf("missing report action")
		}
		if event.Action == "fail" || event.Action == "skip" {
			return result, fmt.Errorf("failed or skipped mandatory execution")
		}
		if event.Action != "pass" {
			continue
		}
		if event.Test == "" {
			packagePass++
			continue
		}
		if !want[event.Test] || passed[event.Test] {
			return result, fmt.Errorf("unexpected or duplicate test terminal event")
		}
		passed[event.Test] = true
	}
	if s.Err() != nil {
		return result, s.Err()
	}
	if packagePass != 1 || len(passed) != len(want) {
		return result, fmt.Errorf("incomplete execution: passed %d of %d", len(passed), len(want))
	}
	for _, id := range required {
		result.Passed = append(result.Passed, id)
	}
	return result, nil
}

// V01's executable emits testing's verbose text, retained by the real suite.
func parseVerbose(raw []byte, required []string) (coverage, error) {
	var events strings.Builder
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		for _, terminal := range []struct{ prefix, action string }{{"--- PASS: ", "pass"}, {"--- FAIL: ", "fail"}, {"--- SKIP: ", "skip"}} {
			if strings.HasPrefix(line, terminal.prefix) {
				id := strings.Fields(strings.TrimPrefix(line, terminal.prefix))[0]
				b, _ := json.Marshal(map[string]string{"Action": terminal.action, "Test": id})
				events.Write(b)
				events.WriteByte('\n')
			}
		}
	}
	if s.Err() != nil {
		return coverage{}, s.Err()
	}
	// The enclosing suite independently requires the child's zero exit status.
	io.WriteString(&events, "{\"Action\":\"pass\"}\n")
	return parseReport([]byte(events.String()), required)
}

func dispatch(run func() ([]byte, error), required []string) (coverage, error) {
	raw, err := run()
	if err != nil {
		return coverage{Required: required}, err
	}
	return parseReport(raw, required)
}
