//go:build !windows

package v2

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type publicationProcessTree struct {
	pid             int
	signalAttempted bool
}

func newPublicationProcessTree(command *exec.Cmd) (*publicationProcessTree, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &publicationProcessTree{}, nil
}

func (tree *publicationProcessTree) attach(command *exec.Cmd) error {
	tree.pid = command.Process.Pid
	return nil
}

func (tree *publicationProcessTree) stop(deadline time.Time) error {
	if tree.pid == 0 {
		return nil
	}
	if !tree.signalAttempted {
		tree.signalAttempted = true
		err := syscall.Kill(-tree.pid, syscall.SIGKILL)
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
	}
	for time.Now().Before(deadline) {
		// A later check never sends another terminating signal to a numeric
		// group ID that could have been recycled after the original exits.
		err := syscall.Kill(-tree.pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			tree.pid = 0
			return nil
		}
		if err != nil {
			return err
		}
		// An exited descendant can remain a zombie until its reaper runs.
		// Only living members can retain an OS lock or create new members.
		live, err := publicationGroupHasLiveMember(tree.pid, deadline)
		if err != nil || !live {
			if err == nil {
				tree.pid = 0
			}
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return errPublicationCleanupDeadline
}

func (*publicationProcessTree) close() {}

func publicationGroupHasLiveMember(group int, deadline time.Time) (bool, error) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/ps", "-A", "-o", "pgid=", "-o", "stat=")
	command.WaitDelay = 100 * time.Millisecond
	output := boundedGitOutput{limit: 4 * 1024 * 1024}
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return false, err
	}
	rows := strings.SplitSeq(strings.TrimSpace(output.data.String()), "\n")
	for row := range rows {
		fields := strings.Fields(row)
		if len(fields) != 2 {
			return false, errors.New("publication process listing is unavailable")
		}
		pgid, err := strconv.Atoi(fields[0])
		if err != nil {
			return false, err
		}
		if pgid == group && fields[1][0] != 'Z' {
			return true, nil
		}
	}
	return false, nil
}
