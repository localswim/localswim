package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/localswim/localswim/internal/v2"
)

const (
	replacementCommand = "serve-prepared-replacement"
	replacementWait    = 5 * time.Second
	runImagePrefix     = ".localswim-run-"
)

func runImageName() string {
	if runtime.GOOS == "windows" {
		return "localswim.exe"
	}
	return "localswim"
}

type nativeReplacement struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  io.ReadCloser
	replies *bufio.Scanner
	path    string
}

func prepareNativeReplacement(root string, port int, image []byte, id string) (v2.PreparedActivation, error) {
	dir, err := os.MkdirTemp(root, runImagePrefix)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, runImageName())
	started := false
	defer func() {
		if !started {
			removeRunImage(path)
		}
	}()
	if err := os.WriteFile(path, image, 0o700); err != nil {
		return nil, err
	}
	command := exec.Command(path, replacementCommand, root, strconv.Itoa(port), id)
	configureReplacementProcess(command)
	input, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := command.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	if err := startPinnedImage(command, image); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	started = true
	replies := bufio.NewScanner(output)
	replies.Buffer(make([]byte, 128), 128)
	child := &nativeReplacement{command: command, input: input, output: output, replies: replies, path: path}
	if err := child.expect("ready " + v2.Version); err != nil {
		return nil, errors.Join(err, child.Abort())
	}
	return child, nil
}

var errRunImageChanged = errors.New("prepared executable image changed")

func startPinnedImage(command *exec.Cmd, image []byte) error {
	pinned, err := pinRunImage(command.Path)
	if err != nil {
		return err
	}
	defer pinned.Close()
	size := int64(len(image))
	info, err := pinned.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != size {
		return errRunImageChanged
	}
	expected := sha256.Sum256(image)
	hash := sha256.New()
	if n, err := io.Copy(hash, io.LimitReader(pinned, size+1)); err != nil || n != size ||
		!bytes.Equal(hash.Sum(nil), expected[:]) {
		return errRunImageChanged
	}
	current, err := os.Lstat(command.Path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return errRunImageChanged
	}
	// On Windows the open handle denies write and delete sharing until
	// CreateProcess has mapped this verified image into the child.
	return command.Start()
}

func (child *nativeReplacement) expect(expected string) error {
	reply := make(chan string, 1)
	go func() {
		if child.replies.Scan() {
			reply <- child.replies.Text()
		} else {
			reply <- ""
		}
	}()
	timer := time.NewTimer(replacementWait)
	defer timer.Stop()
	select {
	case phase := <-reply:
		if phase != expected {
			return errors.New("replacement did not acknowledge its expected phase")
		}
		return nil
	case <-timer.C:
		return errors.New("replacement did not acknowledge before the deadline")
	}
}

func (child *nativeReplacement) Activate() error {
	if _, err := fmt.Fprintln(child.input, "takeover"); err != nil {
		return err
	}
	if err := child.expect("prepared"); err != nil {
		return err
	}
	// The child has acquired the exact listener and workspace writer claim.
	// Authorize its durable completion before letting it admit requests.
	if _, err := fmt.Fprintln(child.input, "commit"); err != nil {
		return err
	}
	if err := child.expect("committed"); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(child.input, "serve"); err != nil {
		return err
	}
	_ = child.input.Close()
	_ = child.output.Close()
	go func() { _ = child.command.Wait() }()
	return nil
}

func (child *nativeReplacement) Abort() error {
	killErr := child.command.Process.Kill()
	done := make(chan error, 1)
	go func() { done <- child.command.Wait() }()
	timer := time.NewTimer(replacementWait)
	defer timer.Stop()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			return errors.Join(killErr, err)
		}
		// Windows may refuse termination of a process which already exited.
		// Wait's reaped result is authoritative for this captured child.
		removeRunImage(child.path)
		return nil
	case <-timer.C:
		return errors.Join(killErr, errors.New("replacement did not exit before the reap deadline"))
	}
}

func removeRunImage(path string) {
	if err := os.Remove(path); err == nil || errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(filepath.Dir(path))
	}
}

func cleanupRunImages(root string) {
	current, err := os.Executable()
	if err != nil {
		return
	}
	currentInfo, err := os.Stat(current)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), runImagePrefix) {
			continue
		}
		path := filepath.Join(root, entry.Name(), runImageName())
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && !os.SameFile(currentInfo, info) {
			removeRunImage(path)
		}
	}
}

func servePreparedReplacement(args []string) (result error) {
	if len(args) != 3 || !filepath.IsAbs(args[0]) || !isPrivatePipe(os.Stdin) || !isPrivatePipe(os.Stdout) {
		return errors.New("invalid native replacement launch")
	}
	root, id := args[0], args[2]
	port, err := strconv.Atoi(args[1])
	if err != nil || port < 1 || port > 65535 {
		return errors.New("invalid native replacement port")
	}
	commands := bufio.NewScanner(os.Stdin)
	commands.Buffer(make([]byte, 128), 128)
	if _, err := fmt.Fprintln(os.Stdout, "ready "+v2.Version); err != nil || !commands.Scan() || commands.Text() != "takeover" {
		return errors.New("replacement was not authorized to take ownership")
	}
	listener, err := listenLoopback(port)
	if err != nil {
		return err
	}
	defer listener.Close()
	app, err := openService(root)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, app.Close()) }()
	if _, err := fmt.Fprintln(os.Stdout, "prepared"); err != nil || !commands.Scan() || commands.Text() != "commit" {
		return errors.New("replacement handoff was not committed")
	}
	if err := app.CompleteActivation(id); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(os.Stdout, "committed")
	// The parent may exit unexpectedly after authorizing the durable commit.
	// At that point it no longer owns the writer or the listener. A clean
	// pipe EOF has the same effect as its normal final serve message.
	if commands.Scan() && commands.Text() != "serve" || commands.Err() != nil {
		return errors.New("invalid replacement serve phase")
	}
	_ = os.Stdin.Close()
	_ = os.Stdout.Close()
	cleanupRunImages(app.DataRoot())
	return runService(app, listener, app.DataRoot())
}

func isPrivatePipe(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeNamedPipe != 0
}

func handoffService(app *v2.Service, root string, port int, exit v2.ServiceExit) (_ *net.TCPListener, result error) {
	unlock, activationErr := v2.LockServiceStartup(root)
	if unlock != nil {
		defer func() { result = errors.Join(result, unlock()) }()
	}
	if activationErr == nil {
		activationErr = app.ReleaseForActivation()
	}
	if activationErr == nil {
		activationErr = exit.Replacement.Activate()
	}
	if activationErr == nil {
		return nil, nil
	}
	if err := exit.Replacement.Abort(); err != nil {
		return nil, errors.New("could not reap the unsuccessful replacement")
	}
	if err := app.ResumeAfterActivationFailure(exit.ActivationID); err != nil {
		return nil, errors.New("could not recover previous workspace ownership")
	}
	listener, err := listenLoopback(port)
	if err != nil {
		return nil, errors.New("could not restore the previous loopback listener")
	}
	return listener, nil
}
