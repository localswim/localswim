package main

import (
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"

	"github.com/localswim/localswim/internal/client"
)

type cliTicketInspection struct {
	boardID string
	ticket  uint64
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
	ticket, err := strconv.ParseUint(flags.Arg(1), 10, 64)
	if err != nil || ticket == 0 || ticket > 1<<53-1 {
		return nil, errors.New("ticket must be a positive safe integer")
	}
	command.boardID, command.ticket = strings.ToLower(flags.Arg(0)), ticket
	var dataProvided bool
	flags.Visit(func(seen *flag.Flag) { dataProvided = dataProvided || seen.Name == "data" })
	if dataProvided && command.dataDir == "" {
		return nil, errors.New("--data requires a nonempty directory")
	}
	return command, nil
}

func executeTicketInspection(api *client.Client, command *cliTicketInspection, stdout io.Writer) error {
	reply, err := api.InspectTicket(command.boardID, command.ticket, command.prose)
	if err != nil {
		return err
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return errors.New("could not write the ticket inspection")
	}
	return nil
}
