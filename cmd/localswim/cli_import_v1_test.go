package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestV1ImportPaths(t *testing.T) {
	dataDirectory := t.TempDir()
	exportDirectory := t.TempDir()
	sourcePath := filepath.Join(exportDirectory, "board snapshot.json")
	attachmentDirectory := filepath.Join(exportDirectory, "original attachments")
	const boardID = "f103a8c2-68b2-42e9-a34b-f6c780410ca6"
	for _, tc := range []struct {
		name string
		args []string
		want cliV1Import
	}{
		{"sibling attachments", []string{dataDirectory, sourcePath}, cliV1Import{
			dataDirectory: dataDirectory, sourcePath: sourcePath, attachmentDirectory: filepath.Join(exportDirectory, "attachments"),
		}},
		{"stdin without attachments", []string{dataDirectory, "-"}, cliV1Import{
			dataDirectory: dataDirectory, sourcePath: "-",
		}},
		{"stdin with attachments and ID", []string{"--id", strings.ToUpper(boardID), dataDirectory, "-", attachmentDirectory}, cliV1Import{
			dataDirectory: dataDirectory, sourcePath: "-", attachmentDirectory: attachmentDirectory, boardID: boardID,
		}},
		{"explicit attachments for a file", []string{dataDirectory, sourcePath, attachmentDirectory}, cliV1Import{
			dataDirectory: dataDirectory, sourcePath: sourcePath, attachmentDirectory: attachmentDirectory,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, err := parseCLI(append([]string{"board", "import-v1"}, tc.args...))
			if err != nil || cmd.v1Import == nil || *cmd.v1Import != tc.want {
				t.Fatalf("parseCLI(%q) = %+v, %v; want %+v", tc.args, cmd.v1Import, err, tc.want)
			}
		})
	}
}

func TestV1ImportHelpAndOfflineDispatch(t *testing.T) {
	dataDirectory := t.TempDir()
	offline := func(name string) string {
		t.Errorf("offline import checked %s", name)
		return ""
	}
	const usage = "localswim board import-v1 [--id UUID] DATA_DIRECTORY <JSON_FILE|-> [ATTACHMENTS_DIRECTORY]"
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing source", []string{dataDirectory}, "requires DATA_DIRECTORY"},
		{"extra positional", []string{dataDirectory, "-", "attachments", "extra"}, "requires DATA_DIRECTORY"},
		{"empty source", []string{dataDirectory, ""}, "requires DATA_DIRECTORY"},
		{"empty attachment directory", []string{dataDirectory, "-", ""}, "requires DATA_DIRECTORY"},
		{"relative destination", []string{"relative", "-"}, "must be absolute"},
		{"empty ID", []string{"--id=", dataDirectory, "-"}, "--id requires a UUIDv4"},
		{"wrong ID version", []string{"--id", "f103a8c2-68b2-72e9-a34b-f6c780410ca6", dataDirectory, "-"}, "--id requires a UUIDv4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput(append([]string{"board", "import-v1"}, tc.args...), nil, &stdout, &stderr, offline)
			if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), tc.want) || !strings.Contains(stderr.String(), usage) {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, &stdout, &stderr)
			}
		})
	}
	var stdout, stderr bytes.Buffer
	code := runCLIWithInput([]string{"board", "import-v1", "--help"}, nil, &stdout, &stderr, offline)
	if code != 0 || !strings.Contains(stdout.String(), usage) || stderr.Len() != 0 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
	stdout.Reset()
	code = runCLIWithInput([]string{"board", "import-v1", dataDirectory, dataDirectory}, nil, &stdout, &stderr, offline)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "input must be a regular file") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, &stdout, &stderr)
	}
}
