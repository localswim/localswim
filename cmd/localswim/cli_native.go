package main

import (
	"encoding/json"
	"errors"
	"fmt"

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
