package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/localswim/localswim/internal/v2"
)

func readNativeBoard(command cliCommand) (json.RawMessage, error) {
	source, err := v2.OpenNativeBoard(command.dataDir, command.boardID)
	if err != nil {
		return nil, fmt.Errorf("could not read saved board: %w", err)
	}
	switch command.nativeRead {
	case "metadata":
		return source.MetadataJSON()
	case "export":
		return source.ExportJSON()
	case "ticket":
		inspect := command.ticketInspection
		return source.InspectTicketJSON(inspect.ticket, inspect.prose)
	case "cards":
		_, query, _ := strings.Cut(command.resource, "?")
		return source.ListCardsJSON(query)
	case "comments":
		_, query, _ := strings.Cut(command.resource, "?")
		return source.ListCommentsJSON(query)
	case "activity":
		_, query, _ := strings.Cut(command.resource, "?")
		return source.ListActivitiesJSON(query)
	default:
		return nil, errors.New("this view is unavailable for native board reads")
	}
}

func executeNativeRead(command cliCommand, stdout io.Writer) error {
	data, err := readNativeBoard(command)
	if err != nil {
		return err
	}
	if err := writeCLIJSON(stdout, data); err != nil {
		return errors.New("could not write the saved board response")
	}
	return nil
}
