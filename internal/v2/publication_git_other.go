//go:build !windows

package v2

import "os/exec"

func configurePublicationGitCommand(_ *exec.Cmd) {}
