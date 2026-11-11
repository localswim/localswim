package client

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

func (c *Client) PatchBoardAvailability(resource string, body json.RawMessage, ifMatch string) (Response, error) {
	return c.doWithContentType(http.MethodPatch, resource, body, ifMatch, "", true)
}

var problemRequestID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

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
		Type      string  `json:"type"`
		Title     string  `json:"title"`
		Status    int     `json:"status"`
		Code      string  `json:"code"`
		Detail    *string `json:"detail"`
		RequestID string  `json:"request_id"`
		Outcome   string  `json:"outcome"`
		Errors    []struct {
			Field string `json:"field"`
			Code  string `json:"code"`
		} `json:"errors"`
	}
	if jsonv2.Unmarshal(data, &problem, jsonv2.RejectUnknownMembers(true)) != nil ||
		problem.Type != "about:blank" || problem.Title == "" || utf8.RuneCountInString(problem.Title) > 120 ||
		problem.Status != http.StatusServiceUnavailable || problem.Code != "unavailable" ||
		problem.Detail == nil || utf8.RuneCountInString(*problem.Detail) > 512 || !problemRequestID.MatchString(problem.RequestID) ||
		problem.Outcome != "not_applied" || problem.Errors == nil {
		return false
	}
	validCodes := []string{"required", "invalid_type", "invalid_value", "duplicate", "unknown_reference", "forbidden_field", "incompatible_history", "integrity_failure"}
	for _, issue := range problem.Errors {
		if issue.Field == "" || utf8.RuneCountInString(issue.Field) > 128 || !slices.Contains(validCodes, issue.Code) {
			return false
		}
	}
	return true
}

// BoardAvailability verifies the response identifies the addressed board and
// contains its availability. Callers retain the original JSON for output.
func (r Response) BoardAvailability(boardID string) (string, error) {
	var board struct {
		ID           string `json:"id"`
		ETag         string `json:"etag"`
		Revision     uint64 `json:"revision"`
		Availability string `json:"availability"`
	}
	if json.Unmarshal(r.JSON, &board) != nil || !strings.EqualFold(board.ID, boardID) ||
		board.Revision == 0 || board.Revision > 1<<53-1 {
		return "", errors.New("service did not return the addressed board and its revision")
	}
	if r.ETag == "" || board.ETag != r.ETag {
		return "", errors.New("service board JSON ETag did not match its HTTP ETag")
	}
	if board.Availability != "open" && board.Availability != "closed" {
		return "", errors.New("service did not return valid board availability")
	}
	return board.Availability, nil
}
