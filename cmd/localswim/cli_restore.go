package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/localswim/localswim/internal/client"
	"github.com/localswim/localswim/internal/v2"
)

func openRestoreInput(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("could not open input file")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, errors.New("input must be a regular file")
	}
	return file, func() { _ = file.Close() }, nil
}

// Keep a private, bounded copy so neither file size nor content can change
// between request construction and transmission. ZIP payloads remain on disk.
func prepareRestoreInput(path string, stdin io.Reader) (*os.File, string, error) {
	input, closeInput, err := openRestoreInput(path, stdin)
	if err != nil {
		return nil, "", err
	}
	defer closeInput()
	reader := bufio.NewReader(input)
	prefix, _ := reader.Peek(4)
	contentType, limit := "application/json", int64(maxCLIInput)
	if bytes.Equal(prefix, []byte{'P', 'K', 3, 4}) {
		contentType, limit = "application/zip", v2.MaxBackupArchiveBytes
	}
	file, err := os.CreateTemp("", "localswim-restore-input-*")
	if err != nil {
		return nil, "", errors.New("could not stage restore input")
	}
	prepared := false
	defer func() {
		if !prepared {
			closeRestoreInput(file)
		}
	}()
	size, err := io.Copy(file, io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, "", errors.New("could not read restore input")
	}
	if size > limit {
		return nil, "", errors.New("restore input exceeds its entity limit")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, "", errors.New("could not read staged restore input")
	}
	if contentType == "application/json" {
		data, err := io.ReadAll(file)
		if err != nil || !client.IsJSONObject(data) {
			return nil, "", errors.New("restore input must be a native JSON snapshot or ZIP backup")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, "", errors.New("could not read staged restore input")
		}
	}
	prepared = true
	return file, contentType, nil
}

func closeRestoreInput(file *os.File) {
	_ = file.Close()
	_ = os.Remove(file.Name())
}

func executeBoardRestore(api *client.Client, cmd cliCommand, stdin io.Reader, stdout io.Writer) error {
	file, contentType, err := prepareRestoreInput(cmd.inputPath, stdin)
	if err != nil {
		return err
	}
	defer closeRestoreInput(file)
	board, err := api.Do(http.MethodGet, cmd.resource, nil, "")
	if err != nil {
		return err
	}
	if board.ETag == "" {
		return errors.New("service did not return a strong board ETag")
	}
	var snapshot struct {
		Board json.RawMessage `json:"board"`
	}
	if json.Unmarshal(board.JSON, &snapshot) != nil || !client.IsJSONObject(snapshot.Board) {
		return errors.New("service did not return a native snapshot board")
	}
	if err := checkCLIRevision(cmd.revision, snapshot.Board); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return errors.New("could not read staged restore input")
	}
	reply, err := api.DoWithReader(cmd.method, cmd.resource, file, info.Size(), board.ETag, contentType)
	if err != nil {
		return err
	}
	if err := writeCLIJSON(stdout, reply.JSON); err != nil {
		return errors.New("service accepted the mutation, but its response could not be written")
	}
	return nil
}
