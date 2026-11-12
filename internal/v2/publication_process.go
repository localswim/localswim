package v2

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

const publicationGitCleanupTimeout = 2 * time.Second

var errPublicationGitTimeout = errors.New("publication Git timed out")
var errPublicationCleanupDeadline = errors.New("publication process cleanup timed out")

type publicationProcess interface {
	attach(*exec.Cmd) error
	stop(time.Time) error
	close()
}

// The exact tree and root wait remain available if the operating system does
// not confirm termination within the request's cleanup allowance.
type publicationCleanup struct {
	tree     publicationProcess
	rootDone <-chan struct{}
}

func (*publicationCleanup) Error() string { return "publication process cleanup unconfirmed" }

func (cleanup *publicationCleanup) confirm() bool {
	deadline := time.Now().Add(publicationGitCleanupTimeout)
	if cleanup.tree.stop(deadline) != nil || !publicationWaitUntil(cleanup.rootDone, deadline) {
		return false
	}
	cleanup.tree.close()
	return true
}

func publicationWaitUntil(done <-chan struct{}, deadline time.Time) bool {
	select {
	case <-done:
		return true
	default:
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

type publicationPipe struct {
	child, parent *os.File
	source        io.Reader
	target        io.Writer
}

func publicationPipes(command *exec.Cmd) ([]publicationPipe, error) {
	var pipes []publicationPipe
	for _, target := range []*io.Writer{&command.Stdout, &command.Stderr} {
		if *target == nil {
			continue
		}
		read, write, err := os.Pipe()
		if err != nil {
			closePublicationPipes(pipes)
			return nil, err
		}
		pipes = append(pipes, publicationPipe{child: write, parent: read, source: read, target: *target})
		*target = write
	}
	if command.Stdin != nil {
		read, write, err := os.Pipe()
		if err != nil {
			closePublicationPipes(pipes)
			return nil, err
		}
		pipes = append(pipes, publicationPipe{child: read, parent: write, source: command.Stdin, target: write})
		command.Stdin = read
	}
	return pipes, nil
}

func closePublicationPipes(pipes []publicationPipe) {
	for _, pipe := range pipes {
		_ = pipe.child.Close()
		_ = pipe.parent.Close()
	}
}

func runPublicationCommand(ctx context.Context, command *exec.Cmd) error {
	tree, err := newPublicationProcessTree(command)
	if err != nil {
		return err
	}
	return runPublicationProcess(ctx, command, tree)
}

// Keep the root wait independent of inherited I/O. Cmd.Wait can otherwise
// hide a pipe deadline behind an earlier nonzero exit from Git itself.
func runPublicationProcess(ctx context.Context, command *exec.Cmd, tree publicationProcess) error {
	pipes, err := publicationPipes(command)
	if err != nil {
		tree.close()
		return err
	}
	if err := command.Start(); err != nil {
		closePublicationPipes(pipes)
		tree.close()
		return err
	}
	for _, pipe := range pipes {
		_ = pipe.child.Close()
	}
	attachErr := tree.attach(command)
	if attachErr != nil {
		_ = command.Process.Kill()
	}
	rootDone := make(chan struct{})
	var rootErr error
	go func() {
		rootErr = command.Wait()
		close(rootDone)
	}()
	pipeDone := make(chan struct{})
	var pipeErr error
	go func() {
		results := make(chan error, len(pipes))
		for _, pipe := range pipes {
			go func() {
				defer pipe.parent.Close()
				_, err := io.Copy(pipe.target, pipe.source)
				results <- err
			}()
		}
		for range pipes {
			pipeErr = errors.Join(pipeErr, <-results)
		}
		close(pipeDone)
	}()

	var timedOut bool
	if attachErr == nil {
		select {
		case <-rootDone:
			drain := time.Now().Add(publicationGitCleanupTimeout)
			if deadline, ok := ctx.Deadline(); ok && deadline.Before(drain) {
				drain = deadline
			}
			timedOut = !publicationWaitUntil(pipeDone, drain) || ctx.Err() != nil
		case <-ctx.Done():
			timedOut = true
		}
	}
	cleanup := &publicationCleanup{tree: tree, rootDone: rootDone}
	confirmed := cleanup.confirm()
	if !confirmed {
		// Keep the exact tree on an unconfirmed result. This direct-child
		// fallback does not establish anything about its descendants.
		_ = command.Process.Kill()
	}
	closePublicationPipes(pipes)
	<-pipeDone // Closing the parent ends releases I/O even if a helper remains.
	if !confirmed {
		return cleanup
	}
	if timedOut {
		return errPublicationGitTimeout
	}
	if attachErr != nil {
		return attachErr
	}
	if rootErr != nil {
		return rootErr
	}
	return pipeErr
}
