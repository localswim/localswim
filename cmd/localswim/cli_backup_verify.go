package main

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/localswim/localswim/internal/v2"
)

func verifyBackupFile(path string, stdout io.Writer) error {
	result, err := v2.VerifyBackupFile(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return errors.New("could not write backup verification result")
	}
	return nil
}
