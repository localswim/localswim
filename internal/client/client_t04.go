package client

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
)

const MaxAttachmentBytes = 10 << 20

func (c *Client) AttachmentContent(resource string) ([]byte, error) {
	const accept = "application/octet-stream, image/png, image/jpeg, image/gif, image/webp"
	request, err := c.newRequest(http.MethodGet, resource, nil, "", "", accept)
	if err != nil {
		return nil, err
	}
	reply, err := c.http.Do(request)
	if err != nil {
		return nil, errors.New("could not reach the local service or read its response")
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service returned HTTP %d", reply.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
	if err != nil || len(reply.Header.Values("Content-Type")) != 1 ||
		len(reply.Header.Values("Content-Encoding")) != 0 {
		return nil, errors.New("service returned invalid attachment content headers")
	}
	switch mediaType {
	case "application/octet-stream", "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return nil, errors.New("service returned an unsupported attachment content type")
	}
	data, err := io.ReadAll(io.LimitReader(reply.Body, MaxAttachmentBytes+1))
	if err != nil || len(data) > MaxAttachmentBytes {
		return nil, errors.New("service returned unreadable or oversized attachment content")
	}
	return data, nil
}

func (c *Client) RemoveRelationship(resource, ifMatch, boardTag string) error {
	if !IsEntityTag(ifMatch) || !IsEntityTag(boardTag) {
		return errors.New("relationship removal requires strong relationship and board ETags")
	}
	return c.deleteBoardResource(resource, ifMatch, boardTag)
}

func (c *Client) DetachAttachment(resource, ifMatch, boardTag string) error {
	if !IsEntityTag(ifMatch) || !IsEntityTag(boardTag) {
		return errors.New("attachment detachment requires strong attachment and board ETags")
	}
	return c.deleteBoardResource(resource, ifMatch, boardTag)
}

func (c *Client) deleteBoardResource(resource, ifMatch, boardTag string) error {
	request, err := c.newRequest(http.MethodDelete, resource, nil, ifMatch, "", "application/json")
	if err != nil {
		return err
	}
	request.Header.Set("X-Expected-Board-ETag", boardTag)
	reply, err := c.http.Do(request)
	if err != nil {
		return responseError("could not reach the local service or read its response", true)
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusNoContent {
		uncertainStatus := reply.StatusCode < 400 || reply.StatusCode >= 500
		return responseError(fmt.Sprintf("service returned HTTP %d", reply.StatusCode), uncertainStatus)
	}
	tags := reply.Header.Values("X-Board-ETag")
	if len(tags) != 1 || !IsEntityTag(tags[0]) {
		return responseError("service did not return one canonical strong X-Board-ETag", true)
	}
	return nil
}
