package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/localswim/localswim/internal/v2"
)

func readNativeBoard(dataDir, boardID, view string, ticket uint64, includeProse bool) (json.RawMessage, error) {
	source, err := v2.OpenNativeBoard(dataDir, boardID)
	if err != nil {
		return nil, fmt.Errorf("could not read saved board: %w", err)
	}
	switch view {
	case "metadata":
		return source.MetadataJSON()
	case "export":
		return source.ExportJSON()
	case "ticket":
		return source.InspectTicketJSON(ticket, includeProse)
	default:
		return nil, errors.New("this view is unavailable for native board reads")
	}
}

func executeNativeRead(dataDir, boardID, view string, ticket uint64, includeProse bool, stdout io.Writer) error {
	data, err := readNativeBoard(dataDir, boardID, view, ticket, includeProse)
	if err != nil {
		return err
	}
	if err := writeCLIJSON(stdout, data); err != nil {
		return errors.New("could not write the saved board response")
	}
	return nil
}
