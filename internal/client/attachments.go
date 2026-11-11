package client

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

var attachmentIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var attachmentDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func pathWithoutQuery(resource string) string {
	path, _, _ := strings.Cut(resource, "?")
	return path
}

func validAttachmentResponse(resource string, data json.RawMessage) bool {
	path := pathWithoutQuery(resource)
	if path == "/attachments" {
		parsed, err := url.ParseRequestURI(resource)
		if err != nil {
			return false
		}
		if parsed.Query().Has("card_id") {
			var page struct {
				Attachments []json.RawMessage `json:"attachments"`
			}
			if json.Unmarshal(data, &page) != nil || page.Attachments == nil {
				return false
			}
			for _, item := range page.Attachments {
				if !validAttachment(item, "", parsed.Query().Get("card_id")) {
					return false
				}
			}
			return true
		}
		return validAttachment(data, "", "")
	}
	return validAttachment(data, strings.TrimPrefix(path, "/attachments/"), "")
}

func validAttachment(data json.RawMessage, id, cardID string) bool {
	var fields map[string]json.RawMessage
	var item struct {
		ID           string  `json:"id"`
		BoardID      string  `json:"board_id"`
		CardID       string  `json:"card_id"`
		Revision     uint64  `json:"revision"`
		CreatedAt    string  `json:"created_at"`
		UpdatedAt    string  `json:"updated_at"`
		OriginalName string  `json:"original_name"`
		Size         *uint64 `json:"size"`
		Digest       string  `json:"sha2_256"`
		MediaType    string  `json:"media_type"`
		Active       *bool   `json:"active"`
		Previewable  *bool   `json:"previewable"`
		AddedBy      string  `json:"added_by"`
	}
	if json.Unmarshal(data, &fields) != nil || json.Unmarshal(data, &item) != nil || len(fields) != 13 ||
		fields["etag"] != nil || fields["sha256"] != nil ||
		!attachmentIDPattern.MatchString(item.ID) || !attachmentIDPattern.MatchString(item.BoardID) ||
		!attachmentIDPattern.MatchString(item.CardID) || !attachmentIDPattern.MatchString(item.AddedBy) ||
		(id != "" && !strings.EqualFold(item.ID, id)) || (cardID != "" && !strings.EqualFold(item.CardID, cardID)) ||
		item.Revision == 0 || item.Revision > 9007199254740991 || item.CreatedAt == "" || item.UpdatedAt == "" || item.OriginalName == "" ||
		item.Size == nil || *item.Size > MaxAttachmentBytes || !attachmentDigestPattern.MatchString(item.Digest) ||
		!strings.Contains(item.MediaType, "/") || item.Active == nil || item.Previewable == nil {
		return false
	}
	return true
}
