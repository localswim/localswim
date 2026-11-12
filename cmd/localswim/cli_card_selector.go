package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/localswim/localswim/internal/client"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func parseCardTarget(boardID, ref string) (*cliTicketTarget, error) {
	if !boardIDPattern.MatchString(boardID) {
		return nil, errors.New("BOARD_ID must be a UUIDv4")
	}
	target := &cliTicketTarget{boardID: strings.ToLower(boardID)}
	if boardIDPattern.MatchString(ref) {
		target.cardID = strings.ToLower(ref)
		return target, nil
	}
	ticket, err := parseCLITicket(ref)
	if err != nil {
		return nil, err
	}
	target.ticket = ticket
	return target, nil
}

func resolveCardTarget(api *client.Client, request *cliTicketTarget) (client.TicketTarget, error) {
	if request.cardID == "" {
		return api.ResolveTicket(request.boardID, request.ticket)
	}
	reply, err := api.Do(http.MethodGet, "/cards/"+request.cardID, nil, "")
	if err != nil {
		return client.TicketTarget{}, err
	}
	if err := validateBoardResource(reply.JSON, request.boardID, request.cardID); err != nil {
		return client.TicketTarget{}, err
	}
	var card struct {
		Ticket   uint64 `json:"ticket"`
		Revision uint64 `json:"revision"`
	}
	if json.Unmarshal(reply.JSON, &card) != nil || !client.MatchesEntityTagRevision(reply.ETag, request.cardID, card.Revision) {
		return client.TicketTarget{}, errors.New("service did not confirm the card validator")
	}
	target, err := api.ResolveTicket(request.boardID, card.Ticket)
	if err != nil {
		return client.TicketTarget{}, err
	}
	if target.CardID != request.cardID || target.CardRevision != card.Revision {
		return client.TicketTarget{}, errors.New("card changed while resolving its board view")
	}
	return target, nil
}

func readCardInput(cmd cliCommand, input io.Reader) (json.RawMessage, error) {
	if cmd.inputPath != "-" {
		file, err := os.Open(cmd.inputPath)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		input = file
	}
	return readT03Input(input, cmd.edit.textInput)
}

func executeCardRead(api *client.Client, cmd cliCommand, output io.Writer) error {
	target, err := resolveCardTarget(api, cmd.cardTarget)
	if err != nil {
		return err
	}
	resource := cmd.resource
	if strings.HasPrefix(resource, "/cards") {
		resource = "/cards/" + target.CardID
	} else {
		path, queryText, _ := strings.Cut(resource, "?")
		query, _ := url.ParseQuery(queryText)
		query.Set("card_id", target.CardID)
		resource = path + "?" + query.Encode()
	}
	reply, err := api.Do(http.MethodGet, resource, nil, "")
	if err != nil {
		return err
	}
	if strings.HasPrefix(resource, "/cards/") {
		if err := validateBoardResource(reply.JSON, target.BoardID, target.CardID); err != nil {
			return err
		}
		var card struct {
			Ticket   uint64 `json:"ticket"`
			Revision uint64 `json:"revision"`
		}
		if json.Unmarshal(reply.JSON, &card) != nil || card.Ticket != target.Ticket || card.Revision != target.CardRevision || !client.MatchesEntityTagRevision(reply.ETag, target.CardID, card.Revision) {
			return errors.New("card changed while reading its resolved ticket and revision")
		}
	} else if err := validateCardListResponse(reply.JSON, resource, target.BoardID, target.CardID); err != nil {
		return err
	}
	return writeCLIJSON(output, reply.JSON)
}

func postCardComment(api *client.Client, cmd cliCommand, input io.Reader, output io.Writer) error {
	target, err := resolveCardTarget(api, cmd.ticketComment)
	if err != nil {
		return err
	}
	if err := checkTargetRevision(cmd.revision, target.CardRevision); err != nil {
		return err
	}
	body := cmd.body
	if cmd.inputPath != "" {
		body, err = readCardInput(cmd, input)
		if err != nil {
			return err
		}
	}
	fields, err := cliInputFields(body)
	if err != nil {
		return err
	}
	if fields["card_id"] != nil || fields["expected_card_etag"] != nil {
		return errors.New("CLI supplies card_id and expected_card_etag")
	}
	fields["card_id"], _ = json.Marshal(target.CardID)
	fields["expected_card_etag"], _ = json.Marshal(target.CardETag)
	body, err = json.Marshal(fields)
	if err != nil {
		return err
	}
	if len(body) > maxCLIInput {
		return errors.New("request exceeds 96 MiB after adding resource validators")
	}
	reply, err := api.PostTicketComment(target, body)
	readback := "comment list " + target.BoardID + " " + target.CardID
	if err != nil {
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. Read back with localswim %s before retrying", err, readback)
		}
		return err
	}
	var confirmation struct {
		CardID string `json:"card_id"`
	}
	if validateBoardResource(reply.JSON, target.BoardID, "") != nil || json.Unmarshal(reply.JSON, &confirmation) != nil || confirmation.CardID != target.CardID {
		return fmt.Errorf("service did not confirm the comment card. %w. Read back with localswim %s before retrying", client.ErrMutationOutcomeUnknown, readback)
	}
	if err := writeCLIJSON(output, reply.JSON); err != nil {
		return fmt.Errorf("service accepted the comment, but its response could not be written. Read back with localswim %s", readback)
	}
	return nil
}

func checkTargetRevision(expected *uint64, current uint64) error {
	if expected != nil && *expected != current {
		return errors.New("revision mismatch")
	}
	return nil
}

func validateCardListResponse(data json.RawMessage, resource, boardID, cardID string) error {
	path, _, _ := strings.Cut(resource, "?")
	key := strings.TrimPrefix(path, "/")
	if key != "comments" && key != "attachments" && key != "relationships" && key != "cards" {
		return nil
	}
	var page map[string]json.RawMessage
	if json.Unmarshal(data, &page) != nil {
		return errors.New("service returned an invalid resource list")
	}
	var entries []json.RawMessage
	if json.Unmarshal(page[key], &entries) != nil || entries == nil {
		return errors.New("service returned an invalid resource list")
	}
	for _, entry := range entries {
		if err := validateBoardResource(entry, boardID, ""); err != nil {
			return err
		}
		if cardID == "" {
			continue
		}
		var item struct {
			ID       string `json:"id"`
			CardID   string `json:"card_id"`
			SourceID string `json:"source_card_id"`
			TargetID string `json:"target_card_id"`
		}
		if json.Unmarshal(entry, &item) != nil {
			return errors.New("service returned an invalid list item")
		}
		matches := item.CardID == cardID
		if key == "cards" {
			matches = item.ID == cardID
		}
		if key == "relationships" {
			matches = item.SourceID == cardID || item.TargetID == cardID
		}
		if !matches {
			return errors.New("service returned a list item outside the requested card")
		}
	}
	return nil
}
