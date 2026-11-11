package client

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"strings"
	"time"
)

var errVerificationReport = errors.New("service did not return a complete verification report for the requested board")

// BoardVerification validates the report and returns its verdict. The caller can
// write the original JSON document to preserve the complete report.
func (r Response) BoardVerification(boardID string) (bool, error) {
	var report struct {
		BoardID  string         `json:"board_id"`
		At       string         `json:"at"`
		Valid    *bool          `json:"valid"`
		Revision jsontext.Value `json:"revision"`
		Issues   []fieldError   `json:"issues"`
	}
	if jsonv2.Unmarshal(r.JSON, &report, jsonv2.RejectUnknownMembers(true)) != nil ||
		!canonicalUUID4.MatchString(report.BoardID) || !strings.EqualFold(report.BoardID, boardID) ||
		report.Valid == nil || len(report.Revision) == 0 || report.Issues == nil {
		return false, errVerificationReport
	}
	const layout = "2006-01-02 15:04:05+00:00"
	at, err := time.Parse(layout, report.At)
	if err != nil || at.Format(layout) != report.At {
		return false, errVerificationReport
	}
	var revision *uint64
	if jsonv2.Unmarshal(report.Revision, &revision) != nil || (revision != nil && *revision > 1<<53-1) {
		return false, errVerificationReport
	}
	for _, issue := range report.Issues {
		if !issue.valid() {
			return false, errVerificationReport
		}
	}
	return *report.Valid, nil
}
