//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package v2

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func lockWriter(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errStoreBusy
	}
	return err
}

func unlockWriter(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func replaceAtomic(source, destination string) error {
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := os.Rename(source, destination); err != nil {
		// POSIX does not guarantee the destination stayed intact after EIO.
		if errors.Is(err, syscall.EIO) {
			return errors.Join(errWriteUncertain, err)
		}
		return err
	}
	if err := dir.Sync(); err != nil {
		return errors.Join(errWriteUncertain, err)
	}
	return nil
}
