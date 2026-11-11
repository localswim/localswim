//go:build unix

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureReplacementProcess(_ *exec.Cmd) {}

func pinRunImage(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
