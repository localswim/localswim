package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func launchBackground(root string, port int, stdout io.Writer) error {
	if port == 0 {
		return errors.New("serve --background requires an explicit nonzero --port")
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return err
	}
	defer read.Close()

	command := exec.Command(executable, "serve", "--data", root, "--port", strconv.Itoa(port))
	// DETACHED_PROCESS prevents console attachment. CREATE_NO_WINDOW can
	// still associate a hidden console with the service.
	const detachedProcess = 0x00000008
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detachedProcess}
	command.Stdout = write
	err = command.Start()
	_ = write.Close()
	if err != nil {
		return err
	}
	defer command.Process.Release()

	// Only the child that has opened the data directory and bound its listener
	// can announce readiness. Probing a port could reach an unrelated process.
	reply := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(read)
		scanner.Buffer(make([]byte, 128), 128)
		if scanner.Scan() {
			reply <- scanner.Text()
			return
		}
		if scanner.Err() == nil {
			close(reply)
			return
		}
		reply <- ""
	}()
	expected := fmt.Sprintf("http://127.0.0.1:%d", port)
	unconfirmed := func() error {
		return fmt.Errorf("background startup unconfirmed (PID %d); inspect the process and listener before retrying", command.Process.Pid)
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case line, ok := <-reply:
		if !ok {
			return errors.New("background service closed its startup stream before announcing its listener; run serve in the foreground for startup diagnostics")
		}
		if line != expected {
			return unconfirmed()
		}
		_, err := fmt.Fprintf(stdout, "%s (PID %d)\n", line, command.Process.Pid)
		return err
	case <-timer.C:
		return unconfirmed()
	}
}
