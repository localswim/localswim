package client

import (
	"encoding/json"
	"errors"
	"net/http"
)

// PatchTicketCard uses both validators from a single board-and-ticket lookup.
func (c *Client) PatchTicketCard(target TicketTarget, body json.RawMessage) (Response, error) {
	if !IsEntityTag(target.CardETag) || !IsEntityTag(target.BoardETag) {
		return Response{}, errors.New("ticket-based card edits require strong card and board ETags")
	}
	return c.doWithContentType(http.MethodPatch, "/cards/"+target.CardID, body, target.CardETag, "", target.BoardETag, false)
}
