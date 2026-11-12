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
	return publishAtomic(source, destination, os.Rename)
}

func createAtomic(source, destination string) error {
	// A hard link publishes the flushed inode without ever replacing a name.
	return publishAtomic(source, destination, os.Link)
}

func publishAtomic(source, destination string, publish func(string, string) error) error {
	dir, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := publish(source, destination); err != nil {
		// An I/O error may leave the destination publication outcome unknown.
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
