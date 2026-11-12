package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

func confirmWholeBoardWrite(cmd cliCommand, input io.Reader, output io.Writer) (bool, error) {
	var action string
	switch {
	case cmd.t06Action == "card set-priorities" && len(cmd.cardSelectors) == 0 && cmd.priorityLane == "":
		action = "update the priority of all unfinished cards, including archived unfinished cards"
	case cmd.t07Action == "board restore" || cmd.t07Action == "restoration create":
		action = "replace the board contents from a backup"
	case cmd.recoveryMode != "":
		action = "recover the board contents using mode " + cmd.recoveryMode
	default:
		return true, nil
	}
	if _, err := fmt.Fprintf(output, "INFO This will %s on board %s.\n", action, cmd.boardID); err != nil {
		return false, err
	}
	if cmd.force {
		return true, nil
	}
	if cmd.inputPath == "-" {
		return false, errors.New("use -f or --force when a whole-board operation reads its payload from stdin")
	}
	if _, err := fmt.Fprint(output, "Continue? [Y/n] "); err != nil {
		return false, err
	}
	if input == nil {
		return false, errors.New("confirmation required; use -f or --force for noninteractive execution")
	}
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil {
		return false, errors.New("confirmation required; use -f or --force for noninteractive execution")
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true, nil
	case "n", "no":
		_, err := fmt.Fprintln(output, "INFO Cancelled.")
		return false, err
	default:
		return false, errors.New("confirmation must be yes or no")
	}
}
