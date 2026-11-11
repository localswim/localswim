package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// BackupContent streams no more than the advertised immutable archive size plus
// one detection byte. The caller verifies the digest before publishing the file.
func (c *Client) BackupContent(resource string, size uint64, output io.Writer) error {
	if size > 9007199254740991 {
		return errors.New("invalid backup size")
	}
	request, err := c.newRequest(http.MethodGet, resource, nil, "", "", "application/zip")
	if err != nil {
		return err
	}
	reply, err := c.http.Do(request)
	if err != nil {
		return errors.New("could not reach the local service or read its response")
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return fmt.Errorf("service returned HTTP %d", reply.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/zip" || len(reply.Header.Values("Content-Type")) != 1 ||
		len(reply.Header.Values("Content-Encoding")) != 0 {
		return errors.New("service returned invalid backup content headers")
	}
	written, err := io.Copy(output, io.LimitReader(reply.Body, int64(size)+1))
	if err != nil || uint64(written) != size {
		return errors.New("backup content does not match its advertised size or could not be written")
	}
	return nil
}

func validT06Response(resource string, data json.RawMessage) bool {
	path := pathWithoutQuery(resource)
	if path == "/cards" {
		var result struct {
			BoardID       string            `json:"board_id"`
			BoardRevision uint64            `json:"board_revision"`
			Cards         []json.RawMessage `json:"cards"`
			Changes       []json.RawMessage `json:"changes"`
		}
		return json.Unmarshal(data, &result) == nil && attachmentIDPattern.MatchString(result.BoardID) &&
			result.BoardRevision > 0 && result.BoardRevision <= 9007199254740991 && result.Cards != nil && result.Changes != nil
	}
	if strings.HasPrefix(path, "/ticket-orders/") {
		var result struct {
			BoardID       string            `json:"board_id"`
			LaneID        string            `json:"lane_id"`
			BoardRevision uint64            `json:"board_revision"`
			Selected      []string          `json:"selected_card_ids"`
			Current       []json.RawMessage `json:"current"`
			Proposed      []json.RawMessage `json:"proposed"`
			Changed       *bool             `json:"changed"`
		}
		return json.Unmarshal(data, &result) == nil && attachmentIDPattern.MatchString(result.BoardID) &&
			strings.EqualFold(result.LaneID, strings.TrimPrefix(path, "/ticket-orders/")) &&
			result.BoardRevision > 0 && result.BoardRevision <= 9007199254740991 && result.Selected != nil &&
			result.Current != nil && result.Proposed != nil && result.Changed != nil
	}
	var result struct {
		ID            string   `json:"id"`
		BoardID       string   `json:"board_id"`
		BoardRevision uint64   `json:"board_revision"`
		CreatedAt     string   `json:"created_at"`
		Digest        string   `json:"sha2_256"`
		Size          *uint64  `json:"size"`
		AttachmentIDs []string `json:"attachment_ids"`
	}
	return json.Unmarshal(data, &result) == nil && attachmentIDPattern.MatchString(result.ID) &&
		(path == "/backups" || strings.EqualFold(result.ID, strings.TrimPrefix(path, "/backups/"))) &&
		attachmentIDPattern.MatchString(result.BoardID) && result.BoardRevision > 0 &&
		result.BoardRevision <= 9007199254740991 && result.CreatedAt != "" &&
		attachmentDigestPattern.MatchString(result.Digest) && result.Size != nil && *result.Size <= 9007199254740991 && result.AttachmentIDs != nil
}
