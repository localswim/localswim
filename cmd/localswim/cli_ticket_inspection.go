package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/localswim/localswim/internal/client"
)

type cliTicketInspection struct {
	boardID string
	ticket  uint64
	cardID  string
	prose   bool
	dataDir string
}

func parseTicketInspection(args []string) (*cliTicketInspection, error) {
	command := &cliTicketInspection{}
	flags := flag.NewFlagSet("localswim card inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&command.prose, "prose", false, "include the selected card's description and comments")
	flags.StringVar(&command.dataDir, "data", "", "read the board from the private data directory")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 2 || !boardIDPattern.MatchString(flags.Arg(0)) {
		return nil, errors.New("card inspect requires a board UUIDv4 and ticket")
	}
	target, err := parseCardTarget(flags.Arg(0), flags.Arg(1))
	if err != nil {
		return nil, err
	}
	command.boardID, command.ticket, command.cardID = target.boardID, target.ticket, target.cardID
	var dataProvided bool
	flags.Visit(func(seen *flag.Flag) { dataProvided = dataProvided || seen.Name == "data" })
	if dataProvided && command.dataDir == "" {
		return nil, errors.New("--data requires a nonempty directory")
	}
	return command, nil
}

func executeTicketInspection(api *client.Client, command *cliTicketInspection, stdout io.Writer) error {
	ticket := command.ticket
	if command.cardID != "" {
		target, err := resolveCardTarget(api, &cliTicketTarget{boardID: command.boardID, cardID: command.cardID})
		if err != nil {
			return err
		}
		ticket = target.Ticket
	}
	reply, err := api.InspectTicket(command.boardID, ticket, command.prose)
	if err != nil {
		return err
	}
	if command.cardID != "" {
		var view struct {
			Card struct {
				ID string `json:"id"`
			} `json:"card"`
		}
		if json.Unmarshal(reply.JSON, &view) != nil || view.Card.ID != command.cardID {
			return errors.New("card changed while reading its inspection")
		}
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return errors.New("could not write the ticket inspection")
	}
	return nil
}
