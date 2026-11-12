package v2

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	errStoreBusy      = errors.New("workspace already has a writer")
	errWriteUncertain = errors.New("snapshot write outcome is uncertain; inspect the durable snapshot before retrying")
)

func acquireWriter(root string) (*os.File, error) {
	return acquireWorkspaceLock(filepath.Join(root, ".writer.lock"))
}

func acquireWorkspaceLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		var named os.FileInfo
		named, err = os.Lstat(path)
		if err == nil && (!named.Mode().IsRegular() || !os.SameFile(info, named)) {
			err = errors.New("workspace lock must be a regular file")
		}
	}
	if err == nil {
		err = lockWriter(file)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	// Never unlink this path. It is the rendezvous for every writer of this root.
	return file, nil
}

func writeAtomic(path string, data []byte) error {
	return writeAtomicChecked(path, data, nil)
}

func writeAtomicChecked(path string, data []byte, beforeReplace func() error) error {
	return writeAtomicUsing(path, data, beforeReplace, replaceAtomic)
}

func writeAtomicNew(path string, data []byte, beforePublish func() error) error {
	return writeAtomicUsing(path, data, beforePublish, createAtomic)
}

func writeAtomicUsing(path string, data []byte, beforePublish func() error, publish func(string, string) error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".pending-*.tmp")
	if err != nil {
		return fmt.Errorf("create pending snapshot: %w", err)
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return fmt.Errorf("flush pending snapshot: %w", err)
	}
	if beforePublish != nil {
		if err := beforePublish(); err != nil {
			return err
		}
	}
	if err := publish(temp, path); err != nil {
		return fmt.Errorf("publish snapshot: %w", err)
	}
	return nil
}
