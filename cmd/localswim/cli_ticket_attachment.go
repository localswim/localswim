package main

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/localswim/localswim/internal/client"
)

func uploadTicketAttachment(api *client.Client, cmd cliCommand, stdin io.Reader, stdout io.Writer) error {
	request := cmd.ticketAttachment
	target, err := api.ResolveTicket(request.boardID, request.ticket)
	if err != nil {
		return err
	}
	if cmd.revision != nil && *cmd.revision != target.CardRevision {
		return fmt.Errorf("revision mismatch (expected %d, current %d)", *cmd.revision, target.CardRevision)
	}
	edit := cmd.edit
	if edit.uploadID == "" {
		edit.uploadID = newUploadID()
	}
	body, contentType, err := uploadBody(edit.file, edit.filename, edit.uploadID, target.CardID, target.CardETag, stdin)
	if err != nil {
		return err
	}
	reply, err := api.UploadTicketAttachment(target, body, contentType)
	if err != nil {
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w (attachment ID %s)", err, edit.uploadID)
		}
		return err
	}
	if !matchesTicketAttachment(reply.JSON, edit.uploadID, target) {
		return fmt.Errorf("service did not confirm the addressed attachment. %w. Read back with localswim attachment get %s",
			client.ErrMutationOutcomeUnknown, edit.uploadID)
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return fmt.Errorf("service accepted attachment %s, but its response could not be written", edit.uploadID)
	}
	return nil
}

func matchesTicketAttachment(body []byte, id string, target client.TicketTarget) bool {
	var item struct {
		ID      string `json:"id"`
		BoardID string `json:"board_id"`
		CardID  string `json:"card_id"`
		Active  bool   `json:"active"`
	}
	return jsonv2.Unmarshal(body, &item) == nil && item.ID == id &&
		item.BoardID == target.BoardID && item.CardID == target.CardID && item.Active
}
