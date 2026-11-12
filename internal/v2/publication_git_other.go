//go:build !windows

package v2

import (
	"errors"
	"os/exec"
	"syscall"
)

type publicationProcessTree struct {
	pid int
}

func newPublicationProcessTree(command *exec.Cmd) (*publicationProcessTree, error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &publicationProcessTree{}, nil
}

func (tree *publicationProcessTree) attach(command *exec.Cmd) error {
	tree.pid = command.Process.Pid
	return nil
}

func (tree *publicationProcessTree) stop() error {
	if tree.pid == 0 {
		return nil
	}
	err := syscall.Kill(-tree.pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (*publicationProcessTree) close() {}
