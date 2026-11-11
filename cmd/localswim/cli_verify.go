package main

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

var errBoardVerificationFailed = errors.New("board verification failed")

func executeOfflineVerification(command cliCommand, stdout io.Writer) error {
	report, err := v2.VerifyOffline(command.dataDir, command.boardID)
	if err != nil {
		return errors.New("offline verification could not read or verify the requested board")
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return errors.New("could not write the verification report")
	}
	if !report.Valid {
		return errBoardVerificationFailed
	}
	return nil
}

func writeLiveVerificationReport(stdout io.Writer, reply client.Response, boardID string) error {
	valid, err := reply.BoardVerification(boardID)
	if err != nil {
		return err
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return errors.New("could not write the verification report")
	}
	if !valid {
		return errBoardVerificationFailed
	}
	return nil
}
