package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

func parseTicketPriorityBatch(priority string, args []string) ([]uint64, json.RawMessage, error) {
	if !boardIDPattern.MatchString(priority) {
		return nil, nil, errors.New("priority batch requires a UUIDv4 priority ID")
	}
	if len(args) < 1 || len(args) > 1000 {
		return nil, nil, errors.New("priority batch requires 1 to 1000 tickets")
	}
	tickets := make([]uint64, 0, len(args))
	seen := make(map[uint64]bool, len(args))
	for _, arg := range args {
		ticket, err := parseCLITicket(arg)
		if err != nil {
			return nil, nil, err
		}
		if seen[ticket] {
			return nil, nil, fmt.Errorf("duplicate ticket %d", ticket)
		}
		seen[ticket] = true
		tickets = append(tickets, ticket)
	}
	body, _ := json.Marshal(map[string]string{"priority_id": strings.ToLower(priority)})
	return tickets, body, nil
}

func resolvePriorityTickets(api *client.Client, cmd cliCommand) ([]string, string, error) {
	var first client.TicketTarget
	ids := make([]string, 0, len(cmd.batchTickets))
	seen := make(map[string]bool, len(cmd.batchTickets))
	for i, ticket := range cmd.batchTickets {
		target, err := api.ResolveTicket(cmd.boardID, ticket)
		if err != nil {
			return nil, "", err
		}
		if i == 0 {
			first = target
			if cmd.revision != nil && *cmd.revision != first.BoardRevision {
				return nil, "", fmt.Errorf("revision mismatch (expected %d, current %d)", *cmd.revision, first.BoardRevision)
			}
		} else if target.BoardETag != first.BoardETag || target.BoardRevision != first.BoardRevision {
			return nil, "", errors.New("board changed during ticket resolution; inspect the current ticket mapping before retrying")
		}
		if seen[target.CardID] {
			return nil, "", errors.New("multiple tickets resolved to the same card")
		}
		seen[target.CardID] = true
		ids = append(ids, target.CardID)
	}
	return ids, first.BoardETag, nil
}
