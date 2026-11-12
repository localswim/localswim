package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
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
		if inspect.cardID != "" {
			return source.InspectCardJSON(inspect.cardID, inspect.prose)
		}
		return source.InspectTicketJSON(inspect.ticket, inspect.prose)
	case "cards":
		_, query, _ := strings.Cut(command.resource, "?")
		return source.ListCardsJSON(query)
	case "comments":
		_, query, _ := strings.Cut(command.resource, "?")
		if command.cardTarget != nil && command.cardTarget.cardID == "" {
			data, err := source.InspectTicketJSON(command.cardTarget.ticket, false)
			if err != nil {
				return nil, err
			}
			var view struct {
				Card struct {
					ID string `json:"id"`
				} `json:"card"`
			}
			if err := json.Unmarshal(data, &view); err != nil {
				return nil, err
			}
			values, _ := url.ParseQuery(query)
			values.Set("card_id", view.Card.ID)
			query = values.Encode()
		}
		return source.ListCommentsJSON(query)
	case "activity":
		_, query, _ := strings.Cut(command.resource, "?")
		values, _ := url.ParseQuery(query)
		if id := values.Get("card_id"); id != "" {
			if _, err := source.CardTicket(id); err != nil {
				return nil, err
			}
		}
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
