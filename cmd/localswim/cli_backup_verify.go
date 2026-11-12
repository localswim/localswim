package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/localswim/localswim/internal/v2"
)

func verifyBackupFile(path string, stdin io.Reader, stdout io.Writer) (resultErr error) {
	if path == "-" {
		file, err := os.CreateTemp("", "localswim-backup-verify-*.zip")
		if err != nil {
			return errors.New("could not create temporary backup input")
		}
		defer func() {
			_ = file.Close()
			if err := os.Remove(file.Name()); err != nil {
				resultErr = errors.Join(resultErr, errors.New("could not remove temporary backup input"))
			}
		}()
		if _, err := io.Copy(file, stdin); err != nil {
			return errors.New("could not read backup bytes from standard input")
		}
		if err := file.Close(); err != nil {
			return errors.New("could not finish temporary backup input")
		}
		path = file.Name()
	}
	result, err := v2.VerifyBackupFile(path)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return errors.New("could not write backup verification result")
	}
	return nil
}
