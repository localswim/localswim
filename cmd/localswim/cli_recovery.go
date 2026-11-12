package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

func parseBoardRecovery(args []string) (cliCommand, error) {
	command := cliCommand{method: http.MethodPost}
	flags := flag.NewFlagSet("board recover", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&command.force, "force", false, "confirm board recovery without prompting")
	flags.BoolVar(&command.force, "f", false, "confirm board recovery without prompting")
	flags.StringVar(&command.recoveryMode, "mode", "", "explicit recovery mode (same_id or fresh)")
	if err := flags.Parse(args); err != nil {
		return command, err
	}
	if command.recoveryMode != "same_id" && command.recoveryMode != "fresh" {
		return command, errors.New("board recover requires --mode same_id or --mode fresh")
	}
	if flags.NArg() != 2 || !boardIDPattern.MatchString(flags.Arg(0)) || flags.Arg(1) == "" {
		return command, errors.New("board recover requires a UUIDv4 board ID followed by FILE or - for standard input")
	}
	command.boardID, command.inputPath = strings.ToLower(flags.Arg(0)), flags.Arg(1)
	command.resource = "/recoveries/" + command.boardID + "?mode=" + command.recoveryMode
	return command, nil
}

func executeBoardRecovery(api *client.Client, command cliCommand, stdin io.Reader, stdout io.Writer) error {
	file, contentType, err := prepareRestoreInput(command.inputPath, stdin)
	if err != nil {
		return err
	}
	defer closeRestoreInput(file)
	report, err := api.Do(http.MethodGet, "/verifications/"+command.boardID, nil, "")
	if err != nil {
		return err
	}
	valid, err := report.BoardVerification(command.boardID)
	if err != nil {
		return err
	}
	var point struct {
		Recovery *struct {
			Mode string `json:"mode"`
		} `json:"recovery"`
	}
	if valid || json.Unmarshal(report.JSON, &point) != nil || point.Recovery == nil || report.ETag == "" {
		return errors.New("board recover requires a current startup recovery diagnostic and its validator")
	}
	if command.recoveryMode == "same_id" && point.Recovery.Mode != "same_id" {
		return errors.New("trusted continuity is unavailable; explicit --mode fresh is required")
	}
	info, err := file.Stat()
	if err != nil {
		return errors.New("could not read staged recovery input")
	}
	reply, err := api.DoWithReader(command.method, command.resource, file, info.Size(), report.ETag, contentType)
	if err != nil {
		return fmt.Errorf("%w. Inspect board list and the recovery diagnostic before another recovery", err)
	}
	var recovered struct {
		ID         string `json:"id"`
		Revision   uint64 `json:"revision"`
		Publishing struct {
			Enabled bool `json:"enabled"`
		} `json:"publishing"`
	}
	if json.Unmarshal(reply.JSON, &recovered) != nil || !boardIDPattern.MatchString(recovered.ID) ||
		(command.recoveryMode == "same_id" && recovered.ID != command.boardID) ||
		(command.recoveryMode == "fresh" && recovered.ID == command.boardID) || recovered.Publishing.Enabled {
		return fmt.Errorf("service did not confirm the recovered board identity: %w", client.ErrMutationOutcomeUnknown)
	}
	if _, err := reply.BoardAvailability(recovered.ID); err != nil {
		return fmt.Errorf("service did not confirm the recovered board version: %w", client.ErrMutationOutcomeUnknown)
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return fmt.Errorf("recovery response could not be written; inspect board list before another recovery: %w", client.ErrMutationOutcomeUnknown)
	}
	return nil
}
