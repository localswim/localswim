package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/localswim/localswim/internal/v2"
)

type cliV1Import struct {
	dataDirectory, attachmentDirectory, boardID, sourcePath string
}

func parseV1Import(args []string) (cliCommand, error) {
	var cmd cliCommand
	var id string
	flags := flag.NewFlagSet("board import-v1", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&id, "id", "", "new board UUIDv4")
	if err := flags.Parse(args); err != nil {
		return cmd, err
	}
	var hasID bool
	flags.Visit(func(f *flag.Flag) { hasID = hasID || f.Name == "id" })
	if hasID && !boardIDPattern.MatchString(id) {
		return cmd, errors.New("--id requires a UUIDv4")
	}
	if flags.NArg() < 2 || flags.NArg() > 3 || flags.Arg(1) == "" || (flags.NArg() == 3 && flags.Arg(2) == "") {
		return cmd, errors.New("board import-v1 requires DATA_DIRECTORY, JSON_FILE or -, and optionally ATTACHMENTS_DIRECTORY")
	}
	dataDirectory, sourcePath := flags.Arg(0), flags.Arg(1)
	if !filepath.IsAbs(dataDirectory) {
		return cmd, errors.New("board import-v1 DATA_DIRECTORY must be absolute")
	}
	attachmentDirectory := flags.Arg(2)
	if flags.NArg() == 2 && sourcePath != "-" {
		attachmentDirectory = filepath.Join(filepath.Dir(sourcePath), "attachments")
	}
	cmd.v1Import = &cliV1Import{
		dataDirectory:       dataDirectory,
		attachmentDirectory: attachmentDirectory,
		boardID:             strings.ToLower(id),
		sourcePath:          sourcePath,
	}
	return cmd, nil
}

func executeV1Import(cmd *cliV1Import, stdin io.Reader, stdout io.Writer) error {
	source, closeInput, err := openCLIInput(cmd.sourcePath, stdin)
	if err != nil {
		return err
	}
	defer closeInput()
	result, err := v2.ImportV1(cmd.dataDirectory, cmd.attachmentDirectory, cmd.boardID, source)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("board %s was committed but its import result could not be written", result.BoardID)
	}
	return nil
}
