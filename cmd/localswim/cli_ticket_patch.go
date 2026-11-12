package main

import (
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"

	"github.com/localswim/localswim/internal/client"
)

type cliTicketTarget struct {
	boardID string
	ticket  uint64
	cardID  string
}

func executeTicketCardPatch(api *client.Client, cmd cliCommand, input io.Reader, output io.Writer) error {
	request := cmd.ticketPatch
	target, err := resolveCardTarget(api, request)
	if err != nil {
		return err
	}
	if cmd.revision != nil && *cmd.revision != target.CardRevision {
		return fmt.Errorf("revision mismatch (expected %d, current %d)", *cmd.revision, target.CardRevision)
	}

	body := cmd.body
	if cmd.inputPath != "" {
		body, err = readCardInput(cmd, input)
		if err != nil {
			return err
		}
	}
	reply, err := api.PatchTicketCard(target, body)
	if err != nil {
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. Read back with localswim card get %s %s", err, target.BoardID, target.CardID)
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
		confirmed.BoardID != request.boardID || confirmed.Ticket != target.Ticket ||
		confirmed.Revision < target.CardRevision || confirmed.Revision > maxCLIRevision {
		return fmt.Errorf("service did not confirm the addressed card. %w. Read back with localswim card get %s %s before retrying",
			client.ErrMutationOutcomeUnknown, target.BoardID, target.CardID)
	}
	if err := writeCLIJSON(output, reply.JSON); err != nil {
		return fmt.Errorf("service accepted the mutation, but its response could not be written. Read back with localswim card get %s %s", target.BoardID, target.CardID)
	}
	return nil
}
