package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/localswim/localswim/internal/client"
)

func executeBoardExport(command cliCommand, stdout io.Writer, getenv func(string) string) error {
	var data json.RawMessage
	if command.dataDir != "" {
		var err error
		data, err = readNativeBoard(command.dataDir, command.boardID, "export", 0, false)
		if err != nil {
			return err
		}
	} else {
		api, err := client.New(getenv("LOCALSWIM_URL"), getenv("LOCALSWIM_TOKEN"))
		if err != nil {
			return err
		}
		reply, err := api.Do(http.MethodGet, command.resource, nil, "")
		if err != nil {
			return err
		}
		data = reply.JSON
	}
	var output bytes.Buffer
	if err := writeCLIJSON(&output, data); err != nil {
		return errors.New("could not format the board export")
	}
	if command.exportPath == "-" {
		if _, err := io.Copy(stdout, &output); err != nil {
			return errors.New("could not write the board export to standard output")
		}
		return nil
	}
	file, err := os.OpenFile(command.exportPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("could not create a new board export file")
	}
	_, writeErr := io.Copy(file, &output)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return errors.New("could not finish writing the board export file. Inspect the destination before trying again")
	}
	return nil
}
