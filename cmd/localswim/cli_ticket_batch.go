package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

func parsePriorityBatch(boardID, priority, laneID string, args []string) ([]string, json.RawMessage, error) {
	if !boardIDPattern.MatchString(priority) {
		return nil, nil, errors.New("priority batch requires a UUIDv4 priority ID")
	}
	if laneID != "" {
		if !boardIDPattern.MatchString(laneID) {
			return nil, nil, errors.New("--lane requires a UUIDv4 lane ID")
		}
		if len(args) != 0 {
			return nil, nil, errors.New("choose --lane or explicit card selectors")
		}
	}
	if len(args) > 1000 {
		return nil, nil, errors.New("priority batch accepts at most 1000 explicit card selectors")
	}
	selectors := make([]string, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, arg := range args {
		target, err := parseCardTarget(boardID, arg)
		if err != nil {
			return nil, nil, err
		}
		key := target.cardID
		if key == "" {
			key = fmt.Sprint(target.ticket)
		}
		if seen[key] {
			return nil, nil, errors.New("duplicate card selector")
		}
		seen[key] = true
		selectors = append(selectors, arg)
	}
	request := map[string]any{"priority_id": strings.ToLower(priority)}
	if laneID != "" {
		request["lane_id"] = strings.ToLower(laneID)
	} else if len(selectors) == 0 {
		request["all_unfinished"] = true
	}
	body, _ := json.Marshal(request)
	return selectors, body, nil
}

func resolvePriorityCards(api *client.Client, cmd cliCommand) ([]string, string, error) {
	var first client.TicketTarget
	ids := make([]string, 0, len(cmd.cardSelectors))
	seen := make(map[string]bool, len(cmd.cardSelectors))
	for i, ref := range cmd.cardSelectors {
		selector, err := parseCardTarget(cmd.boardID, ref)
		if err != nil {
			return nil, "", err
		}
		target, err := resolveCardTarget(api, selector)
		if err != nil {
			return nil, "", err
		}
		if i == 0 {
			first = target
			if cmd.revision != nil && *cmd.revision != first.BoardRevision {
				return nil, "", fmt.Errorf("revision mismatch (expected %d, current %d)", *cmd.revision, first.BoardRevision)
			}
		} else if target.BoardETag != first.BoardETag || target.BoardRevision != first.BoardRevision {
			return nil, "", errors.New("board changed during card selection; inspect the current mapping before retrying")
		}
		if seen[target.CardID] {
			return nil, "", errors.New("multiple selectors resolved to the same card")
		}
		seen[target.CardID] = true
		ids = append(ids, target.CardID)
	}
	return ids, first.BoardETag, nil
}
