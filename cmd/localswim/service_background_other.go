//go:build !windows

package main

import (
	"errors"
	"io"
)

func launchBackground(_ string, _ int, _ io.Writer) error {
	return errors.New("serve --background is available on Windows")
}
