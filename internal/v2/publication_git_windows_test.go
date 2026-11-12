package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestM92WindowsPublicationGitProcessFlags(t *testing.T) {
	const createNoWindow, createSuspended = 0x08000000, 0x00000004
	if publicationCreationFlags&(createNoWindow|createSuspended) != createNoWindow|createSuspended {
		t.Fatal("expected hidden suspended publication Git")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const argument = `argument with "quotes" and trailing \`
	const value = `value with Unicode 雨 and spaces`
	command := exec.CommandContext(ctx, executable, "-test.run=^TestM90WindowsStartupHelper$", "--", argument, "")
	command.Env = append(os.Environ(), "LOCALSWIM_M90_WINDOWS_STARTUP=obsolete", "localswim_m90_windows_startup="+value)
	command.Dir = filepath.Join(t.TempDir(), "working directory 雨")
	if err := os.Mkdir(command.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	command.Stdout = &stdout
	err = runPublicationCommand(ctx, command)
	var status *exec.ExitError
	if !errors.As(err, &status) || status.ExitCode() != 1 {
		t.Fatal("native Git startup did not preserve its exit status", err)
	}
	var child []string
	if err := json.Unmarshal(stdout.Bytes(), &child); err != nil || len(child) != 4 {
		t.Fatal("native Git startup did not capture standard output", err)
	}
	if !strings.EqualFold(child[0], command.Dir) || !slices.Equal(child[1:], []string{value, argument, ""}) {
		t.Fatal("native Git startup changed the working directory, environment, or arguments")
	}
}

func TestM90WindowsStartupHelper(t *testing.T) {
	value := os.Getenv("LOCALSWIM_M90_WINDOWS_STARTUP")
	if value == "" {
		return
	}
	directory, err := os.Getwd()
	if err != nil || len(os.Args) < 2 {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(append([]string{directory, value}, os.Args[len(os.Args)-2:]...)); err != nil {
		os.Exit(2)
	}
	os.Exit(1)
}
