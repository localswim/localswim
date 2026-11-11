package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestM39WindowsPinnedImageRejectsChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), runImageName())
	if err := os.WriteFile(path, []byte("different"), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(path)
	if err := startPinnedImage(command, []byte("validated")); !errors.Is(err, errRunImageChanged) || command.Process != nil {
		t.Fatalf("different image did not refuse before launch: %v", err)
	}

	pin, err := pinRunImage(path)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	if err := os.WriteFile(path, []byte("rewrite"), 0o700); err == nil {
		t.Fatal("pinned executable accepted a concurrent write")
	}
	if err := os.Remove(path); err == nil {
		t.Fatal("pinned executable accepted a concurrent delete")
	}
	if err := pin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestM39WindowsAbortReapsAnAlreadyExitedReplacement(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(t.TempDir(), runImagePrefix)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, runImageName())
	if err := os.WriteFile(path, image, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(path, "-test.run=^TestM39WindowsExitedReplacementFixture$")
	command.Env = append(os.Environ(), "LOCALSWIM_TEST_REPLACEMENT_EXIT=1")
	configureReplacementProcess(command)
	input, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := startPinnedImage(command, image); err != nil {
		t.Fatal(err)
	}

	child := nativeReplacement{command: command, input: input, path: path}
	aborted := false
	t.Cleanup(func() {
		if !aborted {
			_ = child.Abort()
		}
	})
	// Wait for the OS process to exit without consuming the captured Go Cmd's
	// only Wait. Abort then has to reap a child which has already exited.
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := syscall.WaitForSingleObject(handle, uint32(replacementWait/time.Millisecond))
	if err != nil || state != syscall.WAIT_OBJECT_0 {
		t.Fatalf("disposable child did not exit: status %d (%v)", state, err)
	}
	aborted = true
	if err := child.Abort(); err != nil {
		t.Fatalf("already-exited child did not reap: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reaped child image was retained: %v", err)
	}
}

func TestM39WindowsExitedReplacementFixture(t *testing.T) {
	if os.Getenv("LOCALSWIM_TEST_REPLACEMENT_EXIT") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(7)
}
