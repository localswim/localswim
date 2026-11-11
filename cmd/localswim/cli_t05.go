package main

import (
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/client"
)

func parseActivityList(args []string) (cliCommand, error) {
	var cmd cliCommand
	flags := flag.NewFlagSet("localswim", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	from := flags.String("from", "", "inclusive UTC start")
	to := flags.String("to", "", "inclusive UTC end")
	view := flags.String("view", "monitor", "full or monitor activity view")
	ticket := flags.String("ticket", "", "filter by ticket at the event revision")
	cardID := flags.String("card-id", "", "filter by stable card UUIDv4")
	writerID := flags.String("writer-id", "", "require this writer UUIDv7 for complete coverage")
	actorID := flags.String("actor-id", "", "filter by actor UUIDv4")
	operation := flags.String("operation", "", "filter by operation identifier")
	limit := flags.Int("limit", 50, "page size")
	cursor := flags.String("cursor", "", "cursor from the preceding page")
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	if flags.NArg() != 1 || !boardIDPattern.MatchString(flags.Arg(0)) {
		return cmd, errors.New("expected one UUIDv4 board ID")
	}
	if *limit < 1 || *limit > 200 || len(*cursor) > 2048 {
		return cmd, errors.New("page limit must be 1 to 200 and cursor must fit within 2048 bytes")
	}
	const layout = "2006-01-02 15:04:05+00:00"
	start, startErr := time.Parse(layout, *from)
	end, endErr := time.Parse(layout, *to)
	if startErr != nil || endErr != nil || start.Format(layout) != *from || end.Format(layout) != *to {
		return cmd, errors.New("--from and --to require YYYY-MM-DD HH:MM:SS+00:00")
	}
	if end.Before(start) {
		return cmd, errors.New("--to must be at or after --from")
	}
	var mode string
	switch {
	case strings.EqualFold(*view, "full"):
		mode = "full"
	case strings.EqualFold(*view, "monitor"):
		mode = "monitor"
	default:
		return cmd, errors.New("--view requires full or monitor")
	}
	query := url.Values{
		"board_id": {strings.ToLower(flags.Arg(0))},
		"from":     {*from},
		"to":       {*to},
		"view":     {mode},
		"limit":    {strconv.Itoa(*limit)},
	}
	var optionErr error
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "ticket":
			n, err := strconv.ParseUint(*ticket, 10, 53)
			if err != nil || n == 0 {
				optionErr = errors.New("--ticket requires an integer from 1 to 9007199254740991")
			} else {
				query.Set("ticket", strconv.FormatUint(n, 10))
			}
		case "actor-id":
			if !boardIDPattern.MatchString(*actorID) {
				optionErr = errors.New("--actor-id requires a UUIDv4")
			} else {
				query.Set("actor_id", strings.ToLower(*actorID))
			}
		case "card-id":
			if !boardIDPattern.MatchString(*cardID) {
				optionErr = errors.New("--card-id requires a UUIDv4")
			} else {
				query.Set("card_id", strings.ToLower(*cardID))
			}
		case "writer-id":
			if !operationIDPattern.MatchString(*writerID) {
				optionErr = errors.New("--writer-id requires a UUIDv7")
			} else {
				query.Set("writer_id", strings.ToLower(*writerID))
			}
		case "operation":
			if len(*operation) == 0 || len(*operation) > 80 {
				optionErr = errors.New("--operation requires an operation identifier of 1 to 80 characters")
			} else {
				query.Set("operation", *operation)
			}
		case "cursor":
			if *cursor == "" {
				optionErr = errors.New("--cursor requires a nonempty value")
			} else {
				query.Set("cursor", *cursor)
			}
		}
	})
	if optionErr != nil {
		return cmd, optionErr
	}
	return cliCommand{method: http.MethodGet, resource: "/activities?" + query.Encode()}, nil
}

func detachAttachment(api *client.Client, cmd cliCommand) error {
	current, err := api.Do(http.MethodGet, cmd.resource, nil, "")
	if err != nil {
		return err
	}
	if current.ETag == "" {
		return errors.New("service did not return a strong attachment ETag")
	}
	if err := checkCLIRevision(cmd.revision, current.JSON); err != nil {
		return err
	}
	board, err := parentBoard(api, current.JSON)
	if err != nil {
		return err
	}
	return api.DetachAttachment(cmd.resource, current.ETag, board.ETag)
}
