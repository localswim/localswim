package main

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

func parseNotificationAction(args []string) (cliCommand, error) {
	var command cliCommand
	if len(args) == 0 {
		return command, errors.New("board notifications requires target, enable, disable, or status")
	}
	action := args[0]
	switch action {
	case "target", "enable", "disable", "status":
	case "--help", "-h":
		return command, flag.ErrHelp
	default:
		return command, errors.New("board notifications requires target, enable, disable, or status")
	}

	flags := flag.NewFlagSet("board notifications "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var revision string
	if action != "status" {
		flags.StringVar(&revision, "revision", "", "expected board revision")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return command, err
	}
	count := 1
	if action == "target" {
		count = 2
	}
	if flags.NArg() != count || !boardIDPattern.MatchString(flags.Arg(0)) {
		if action == "target" {
			return command, errors.New("expected a UUIDv4 board ID followed by a chat UUID")
		}
		return command, errors.New("expected one UUIDv4 board ID")
	}
	if action == "target" {
		command.notificationTarget = strings.ToLower(flags.Arg(1))
		if !v2.ValidNotificationTarget(command.notificationTarget) {
			return command, errors.New("expected a valid chat UUID")
		}
	}
	if flags.NFlag() != 0 {
		var err error
		command.revision, err = parseCLIRevision(revision)
		if err != nil {
			return command, err
		}
	}
	command.notificationAction = action
	command.boardID = strings.ToLower(flags.Arg(0))
	return command, nil
}

type cliNotificationStatus struct {
	BoardID        string  `json:"board_id"`
	Revision       uint64  `json:"revision"`
	Enabled        bool    `json:"enabled"`
	TargetThreadID *string `json:"target_thread_id"`
}

func readNotificationStatus(reply client.Response, boardID string) (cliNotificationStatus, error) {
	var board struct {
		ID            string          `json:"id"`
		Revision      uint64          `json:"revision"`
		ETag          string          `json:"etag"`
		Notifications json.RawMessage `json:"notifications"`
	}
	if jsonv2.Unmarshal(reply.JSON, &board) != nil || board.ID != boardID ||
		board.Revision == 0 || board.Revision > maxCLIRevision ||
		reply.ETag == "" || reply.ETag != board.ETag {
		return cliNotificationStatus{}, errors.New("service did not return the addressed board with its revision and matching ETag")
	}
	var settings struct {
		Enabled        *bool           `json:"enabled"`
		TargetThreadID json.RawMessage `json:"target_thread_id"`
	}
	var target *string
	if jsonv2.Unmarshal(board.Notifications, &settings, jsonv2.RejectUnknownMembers(true)) != nil ||
		settings.Enabled == nil || jsonv2.Unmarshal(settings.TargetThreadID, &target) != nil ||
		target == nil && *settings.Enabled || target != nil && !v2.ValidNotificationTarget(*target) {
		return cliNotificationStatus{}, errors.New("service did not return valid notification settings")
	}
	return cliNotificationStatus{
		BoardID: board.ID, Revision: board.Revision, Enabled: *settings.Enabled, TargetThreadID: target,
	}, nil
}

func executeNotificationAction(api *client.Client, command cliCommand, stdout io.Writer) error {
	resource := "/boards/" + command.boardID
	reply, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
		return err
	}
	current, err := readNotificationStatus(reply, command.boardID)
	if err != nil {
		return err
	}
	if command.notificationAction == "status" {
		return json.NewEncoder(stdout).Encode(current)
	}
	if err := checkCLIRevision(command.revision, reply.JSON); err != nil {
		return err
	}

	var patch map[string]any
	expectedEnabled, expectedTarget := current.Enabled, current.TargetThreadID
	switch command.notificationAction {
	case "target":
		expectedTarget = &command.notificationTarget
		patch = map[string]any{"target_thread_id": command.notificationTarget}
	case "enable", "disable":
		expectedEnabled = command.notificationAction == "enable"
		if expectedEnabled && expectedTarget == nil {
			return errors.New("set a notification target before enabling")
		}
		patch = map[string]any{"enabled": expectedEnabled}
	}
	body, err := json.Marshal(map[string]any{"notifications": patch})
	if err != nil {
		return err
	}
	inspect := "Read back with localswim board notifications status " + command.boardID + " before retrying"
	reply, err = api.Do(http.MethodPatch, resource, body, reply.ETag)
	if err != nil {
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. %s", err, inspect)
		}
		return err
	}
	result, err := readNotificationStatus(reply, command.boardID)
	if err != nil || reply.BoardETag != reply.ETag || result.Enabled != expectedEnabled ||
		(result.TargetThreadID == nil) != (expectedTarget == nil) ||
		expectedTarget != nil && *result.TargetThreadID != *expectedTarget {
		return fmt.Errorf("service did not confirm the requested notification settings. %w. %s",
			client.ErrMutationOutcomeUnknown, inspect)
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return fmt.Errorf("service accepted the notification change, but its response could not be written. %s", inspect)
	}
	return nil
}
