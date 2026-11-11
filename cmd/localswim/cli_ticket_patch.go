package main

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/localswim/localswim/internal/client"
)

type ticketCardPatchTarget struct {
	boardID string
	ticket  uint64
}

func isTicketCardPatch(name string) bool {
	switch name {
	case "card move", "card assign", "card archive", "card unarchive":
		return true
	default:
		return false
	}
}

func executeTicketCardPatch(api *client.Client, cmd cliCommand, output io.Writer) error {
	request := cmd.ticketPatch
	target, err := api.ResolveTicket(request.boardID, request.ticket)
	if err != nil {
		return err
	}
	if cmd.revision != nil && *cmd.revision != target.CardRevision {
		return fmt.Errorf("revision mismatch (expected %d, current %d)", *cmd.revision, target.CardRevision)
	}

	reply, err := api.PatchTicketCard(target, cmd.body)
	if err != nil {
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. Read back with localswim card get %s", err, target.CardID)
		}
		return err
	}
	var confirmed struct {
		ID       string `json:"id"`
		BoardID  string `json:"board_id"`
		Ticket   uint64 `json:"ticket"`
		Revision uint64 `json:"revision"`
	}
	if err := jsonv2.Unmarshal(reply.JSON, &confirmed); err != nil || confirmed.ID != target.CardID ||
		confirmed.BoardID != request.boardID || confirmed.Ticket != request.ticket ||
		confirmed.Revision < target.CardRevision || confirmed.Revision > maxCLIRevision {
		return fmt.Errorf("service did not confirm the addressed card. %w. Read back with localswim card get %s before retrying",
			client.ErrMutationOutcomeUnknown, target.CardID)
	}
	if err := writeCLIJSON(output, reply.JSON); err != nil {
		return errors.New("service accepted the mutation, but its response could not be written")
	}
	return nil
}
