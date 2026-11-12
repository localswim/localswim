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
	"syscall"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf16"
)

func TestM90WindowsPublicationEnvironmentBlockOrder(t *testing.T) {
	entries := []string{"zeta=Z", `=D:=D:\two=extra`, "beta=B", `=C:=C:\some space`, "SystemRoot=C:\\Windows",
		"ALPHA=obsolete", "Alpha-more=A-", "alpha= 雨=exact", "åVariable=Å"}
	command := &exec.Cmd{Env: slices.Clone(entries)}
	block, err := publicationWindowsEnvironment(command)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`=C:=C:\some space`, `=D:=D:\two=extra`, "alpha= 雨=exact", "Alpha-more=A-", "beta=B",
		"SystemRoot=C:\\Windows", "zeta=Z", "åVariable=Å"}
	if string(utf16.Decode(block)) != strings.Join(want, "\x00")+"\x00\x00" {
		t.Fatal("environment block changed entry order, values, or the double NUL")
	}
	if !slices.Equal(command.Env, entries) {
		t.Fatal("environment block encoding changed the caller's entries")
	}
}

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

func TestWindowsPublicationProcessIDsRetryShortSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		ids, err := readPublicationProcessIDs(start.Add(time.Second), func(buffer []uintptr) (uint32, uint32, error) {
			slots := buffer[publicationProcessListHeaderSlots:]
			calls++
			switch calls {
			case 1:
				if len(slots) != 16 {
					t.Fatalf("initial process list capacity = %d", len(slots))
				}
				return 17, 16, syscall.ERROR_MORE_DATA
			case 2:
				if len(slots) != 17 {
					t.Fatalf("grown process list capacity = %d", len(slots))
				}
				slots[0] = 123
				return 2, 1, nil
			case 3:
				if !time.Now().After(start) {
					t.Fatal("short snapshot retry did not wait")
				}
				slots[0] = 456
				return 1, 1, nil
			default:
				t.Fatalf("unexpected process list query %d", calls)
				return 0, 0, nil
			}
		})
		if err != nil || !slices.Equal(ids, []uintptr{456}) || calls != 3 {
			t.Fatalf("process list = %v, err = %v, queries = %d", ids, err, calls)
		}
	})
}

func TestWindowsPublicationProcessIDsPersistentShortSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deadline := time.Now().Add(25 * time.Millisecond)
		calls := 0
		ids, err := readPublicationProcessIDs(deadline, func([]uintptr) (uint32, uint32, error) {
			calls++
			if !time.Now().Before(deadline) || calls > 3 {
				t.Fatal("short snapshot was queried past its retry budget")
			}
			return 2, 1, nil
		})
		if ids != nil || !errors.Is(err, errPublicationCleanupDeadline) || calls != 3 || !time.Now().Equal(deadline) {
			t.Fatalf("process list = %v, err = %v, queries = %d, time past deadline = %v", ids, err, calls, time.Since(deadline))
		}
	})
}

func TestWindowsPublicationProcessIDsRejectLateSnapshot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		deadline := time.Now().Add(time.Millisecond)
		ids, err := readPublicationProcessIDs(deadline, func([]uintptr) (uint32, uint32, error) {
			time.Sleep(time.Until(deadline))
			return 0, 0, nil
		})
		if ids != nil || !errors.Is(err, errPublicationCleanupDeadline) {
			t.Fatalf("late process list = %v, err = %v", ids, err)
		}
	})
}

func TestWindowsPublicationProcessIDsRejectQueryError(t *testing.T) {
	calls := 0
	ids, err := readPublicationProcessIDs(time.Now().Add(time.Second), func([]uintptr) (uint32, uint32, error) {
		calls++
		return 0, 0, syscall.ERROR_ACCESS_DENIED
	})
	if ids != nil || !errors.Is(err, syscall.ERROR_ACCESS_DENIED) || calls != 1 {
		t.Fatalf("process list = %v, err = %v, queries = %d", ids, err, calls)
	}
}
