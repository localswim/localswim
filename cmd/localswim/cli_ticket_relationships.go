package main

import (
	"bytes"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

type cliTicketRelationship struct {
	action, boardID, kind, linkID string
	ticket, related               uint64
}

func parseTicketParentCommand(name string, args []string) (cliCommand, error) {
	var cmd cliCommand
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	revision := flags.String("revision", "", "expected child card revision")
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	count := 2
	if name == "card set-parent" {
		count++
	}
	if flags.NArg() != count {
		return cmd, errors.New("expected BOARD_ID TICKET for clear-parent or BOARD_ID TICKET PARENT_TICKET for set-parent")
	}
	var err error
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "revision" {
			cmd.revision, err = parseCLIRevision(*revision)
		}
	})
	if err != nil {
		return cmd, err
	}
	cmd.ticketRelationship, err = parseTicketRelationship(name, flags.Arg(0), flags.Arg(1), flags.Arg(2))
	return cmd, err
}

func parseTicketLinkCommand(name string, args []string, id string) (*cliTicketRelationship, error) {
	request, err := parseTicketRelationship(name, args[0], args[1], args[3])
	if err != nil {
		return nil, err
	}
	request.kind = strings.ToLower(args[2])
	if !cliRelationshipKind(request.kind) {
		return nil, errors.New("kind must be blocks, duplicates, references, or relates_to")
	}
	if name == "relationship create" {
		if id == "" {
			id = newUploadID()
		}
		request.linkID = strings.ToLower(id)
	}
	return request, nil
}

func parseTicketRelationship(name, boardID, ticket, related string) (*cliTicketRelationship, error) {
	if !boardIDPattern.MatchString(boardID) {
		return nil, errors.New("BOARD_ID must be a UUIDv4")
	}
	number, err := strconv.ParseUint(ticket, 10, 53)
	if err != nil || number == 0 {
		return nil, errors.New("ticket requires an integer from 1 to 9007199254740991")
	}
	request := &cliTicketRelationship{action: name, boardID: strings.ToLower(boardID), ticket: number}
	if name != "card clear-parent" {
		request.related, err = strconv.ParseUint(related, 10, 53)
		if err != nil || request.related == 0 || request.related == number {
			return nil, errors.New("related ticket requires a different integer from 1 to 9007199254740991")
		}
	}
	return request, nil
}

func executeTicketRelationshipCommand(api *client.Client, cmd cliCommand, stdout io.Writer) error {
	request := cmd.ticketRelationship
	var removeKind string
	if request.action == "relationship remove" {
		removeKind = request.kind
	}
	target, err := api.ResolveTicketRelationship(request.boardID, request.ticket, request.related, removeKind)
	if err != nil {
		return err
	}
	currentRevision := target.CardRevision
	switch request.action {
	case "relationship create":
		currentRevision = target.BoardRevision
	case "relationship remove":
		currentRevision = target.LinkRevision
	}
	if cmd.revision != nil && *cmd.revision != currentRevision {
		return fmt.Errorf("revision mismatch (expected %d, current %d)", *cmd.revision, currentRevision)
	}

	switch request.action {
	case "relationship remove":
		err := api.RemoveRelationship("/relationships/"+target.LinkID, target.LinkETag, target.BoardETag)
		if errors.Is(err, client.ErrMutationOutcomeUnknown) {
			return fmt.Errorf("%w. Read back with localswim relationship get %s before retrying", err, target.LinkID)
		}
		return err
	case "relationship create":
		return createTicketRelationship(api, request, target, stdout)
	default:
		return patchTicketParent(api, request, target, stdout)
	}
}

func createTicketRelationship(api *client.Client, request *cliTicketRelationship, target client.TicketRelationshipTarget, stdout io.Writer) error {
	body, _ := json.Marshal(map[string]string{
		"id": request.linkID, "board_id": target.BoardID, "expected_board_etag": target.BoardETag,
		"source_card_id": target.CardID, "kind": request.kind, "target_card_id": target.RelatedCardID,
	})
	reply, err := api.Do(http.MethodPost, "/relationships", body, "")
	if err == nil && !confirmedRelationshipCreation(body, reply.JSON) {
		err = fmt.Errorf("service did not confirm the requested relationship. %w", client.ErrMutationOutcomeUnknown)
	}
	return finishTicketRelationship(stdout, reply, err, "relationship get "+request.linkID)
}

func patchTicketParent(api *client.Client, request *cliTicketRelationship, target client.TicketRelationshipTarget, stdout io.Writer) error {
	var parent *string
	if target.RelatedCardID != "" {
		parent = &target.RelatedCardID
	}
	body, _ := json.Marshal(map[string]*string{"parent_id": parent})
	reply, err := api.PatchTicketCard(target.TicketTarget, body)
	if err == nil && !confirmedTicketParent(reply.JSON, request, target) {
		err = fmt.Errorf("service did not confirm the requested parent. %w", client.ErrMutationOutcomeUnknown)
	}
	return finishTicketRelationship(stdout, reply, err, "card get "+target.CardID)
}

func confirmedTicketParent(data json.RawMessage, request *cliTicketRelationship, target client.TicketRelationshipTarget) bool {
	var value struct {
		ID       string          `json:"id"`
		BoardID  string          `json:"board_id"`
		Ticket   uint64          `json:"ticket"`
		Revision uint64          `json:"revision"`
		ParentID json.RawMessage `json:"parent_id"`
	}
	if jsonv2.Unmarshal(data, &value) != nil || value.ID != target.CardID || value.BoardID != target.BoardID ||
		value.Ticket != request.ticket || value.Revision < target.CardRevision || value.Revision > maxCLIRevision {
		return false
	}
	if target.RelatedCardID == "" {
		return bytes.Equal(value.ParentID, []byte("null"))
	}
	var id string
	return jsonv2.Unmarshal(value.ParentID, &id) == nil && id == target.RelatedCardID
}

func finishTicketRelationship(stdout io.Writer, reply client.Response, err error, readback string) error {
	if errors.Is(err, client.ErrMutationOutcomeUnknown) {
		return fmt.Errorf("%w. Read back with localswim %s before retrying", err, readback)
	}
	if err != nil {
		return err
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return fmt.Errorf("service accepted the mutation, but its response could not be written. Read back with localswim %s", readback)
	}
	return nil
}
