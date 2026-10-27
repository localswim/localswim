package nativequalification

import (
	"encoding/json"
	"errors"
	"testing"
)

func testReport(ids []string) []byte {
	var b []byte
	for _, id := range ids {
		row, _ := json.Marshal(map[string]string{"Action": "pass", "Test": id})
		b = append(b, append(row, '\n')...)
	}
	return append(b, []byte("{\"Action\":\"pass\"}\n")...)
}
func TestRejectIncompleteReports(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte("{}\n"), []byte("not json\n"), testReport(nil), testReport([]string{"Other"}), testReport([]string{"Required", "Required"}), append([]byte("{\"Action\":\"skip\",\"Test\":\"Required\"}\n"), testReport([]string{"Required"})...), append([]byte("{\"Action\":\"fail\"}\n"), testReport([]string{"Required"})...)} {
		if _, err := parseReport(raw, []string{"Required"}); err == nil {
			t.Fatal("invalid report accepted")
		}
	}
	if _, err := parseReport(testReport([]string{"Required"}), []string{"Required"}); err != nil {
		t.Fatal(err)
	}
	if _, err := parseVerbose([]byte("--- SKIP: Required (0s)\n"), []string{"Required"}); err == nil {
		t.Fatal("verbose skip accepted")
	}
}
func TestDispatchFailurePropagation(t *testing.T) {
	called := 0
	_, err := dispatch(func() ([]byte, error) { called++; return testReport([]string{"Required"}), errors.New("child failed") }, []string{"Required"})
	if err == nil || called != 1 {
		t.Fatal("child failure lost")
	}
	_, err = dispatch(func() ([]byte, error) { return testReport(nil), nil }, []string{"Required"})
	if err == nil {
		t.Fatal("zero tests accepted")
	}
}
