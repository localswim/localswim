package client

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// PatchBoardSettings sends a focused availability or notification change.
func (c *Client) PatchBoardSettings(resource string, body json.RawMessage, ifMatch string) (Response, error) {
	return c.doWithContentType(http.MethodPatch, resource, body, ifMatch, "", "", true)
}

var canonicalUUID4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func isUnavailableRefusal(reply *http.Response) bool {
	if reply.StatusCode != http.StatusServiceUnavailable {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/problem+json" || len(reply.Header.Values("Content-Type")) != 1 {
		return false
	}
	const maxProblemBytes = 64 << 10
	data, err := io.ReadAll(io.LimitReader(reply.Body, maxProblemBytes+1))
	if err != nil || len(data) > maxProblemBytes {
		return false
	}
	var problem struct {
		Type      string       `json:"type"`
		Title     string       `json:"title"`
		Status    int          `json:"status"`
		Code      string       `json:"code"`
		Detail    *string      `json:"detail"`
		RequestID string       `json:"request_id"`
		Outcome   string       `json:"outcome"`
		Errors    []fieldError `json:"errors"`
	}
	if jsonv2.Unmarshal(data, &problem, jsonv2.RejectUnknownMembers(true)) != nil ||
		problem.Type != "about:blank" || problem.Title == "" || utf8.RuneCountInString(problem.Title) > 120 ||
		problem.Status != http.StatusServiceUnavailable || problem.Code != "unavailable" ||
		problem.Detail == nil || utf8.RuneCountInString(*problem.Detail) > 512 || !canonicalUUID4.MatchString(problem.RequestID) ||
		problem.Outcome != "not_applied" || problem.Errors == nil {
		return false
	}
	for _, issue := range problem.Errors {
		if !issue.valid() {
			return false
		}
	}
	return true
}

type fieldError struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

func (e fieldError) valid() bool {
	if e.Field == "" || utf8.RuneCountInString(e.Field) > 128 {
		return false
	}
	switch e.Code {
	case "required", "invalid_type", "invalid_value", "duplicate", "unknown_reference",
		"forbidden_field", "incompatible_history", "integrity_failure":
		return true
	default:
		return false
	}
}

// BoardAvailability verifies the response identifies the addressed board and
// contains its availability. Callers retain the original JSON for output.
func (r Response) BoardAvailability(boardID string) (string, error) {
	var board struct {
		ID           string `json:"id"`
		Revision     uint64 `json:"revision"`
		Availability string `json:"availability"`
	}
	if json.Unmarshal(r.JSON, &board) != nil || !strings.EqualFold(board.ID, boardID) ||
		board.Revision == 0 || board.Revision > 1<<53-1 {
		return "", errors.New("service did not return the addressed board and its revision")
	}
	if !r.MatchesBoardRevision(board.ID, board.Revision) {
		return "", errors.New("service board revision did not match its HTTP validator")
	}
	if board.Availability != "open" && board.Availability != "closed" {
		return "", errors.New("service did not return valid board availability")
	}
	return board.Availability, nil
}

// MatchesBoardRevision binds the HTTP validator to the addressed board and its numeric revision.
func (r Response) MatchesBoardRevision(boardID string, revision uint64) bool {
	return r.ETag == fmt.Sprintf("\"%s-%d\"", strings.ToLower(boardID), revision)
}
