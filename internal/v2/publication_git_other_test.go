//go:build !windows

package v2

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

type m90UnreapedLeader struct {
	publicationProcess
	command  *exec.Cmd
	unreaped bool
}

func (probe *m90UnreapedLeader) terminate(deadline time.Time) error {
	probe.unreaped = probe.command.ProcessState == nil
	return probe.publicationProcess.terminate(deadline)
}

func TestM90UnixKeepsGroupLeaderUntilSignal(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^TestM90ProcessHelper$")
	command.Env = append(os.Environ(), "LOCALSWIM_M90_MODE=exit-root")
	tree, err := newPublicationProcessTree(command)
	if err != nil {
		t.Fatal(err)
	}
	probe := &m90UnreapedLeader{publicationProcess: tree, command: command}
	if err := runPublicationProcess(ctx, command, probe); err != nil {
		t.Fatal(err)
	}
	if !probe.unreaped || command.ProcessState == nil || !command.ProcessState.Success() {
		t.Fatal("cleanup did not retain the exited group leader until its first signal")
	}
}
