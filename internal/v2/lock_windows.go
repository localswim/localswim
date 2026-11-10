package v2

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	lockFileEx   = kernel32.NewProc("LockFileEx")
	unlockFileEx = kernel32.NewProc("UnlockFileEx")
	moveFileEx   = kernel32.NewProc("MoveFileExW")
)

func lockWriter(file *os.File) error {
	const (
		failImmediately = 1
		exclusive       = 2
		lockViolation   = syscall.Errno(33)
	)
	var offset syscall.Overlapped
	result, _, err := lockFileEx.Call(file.Fd(), failImmediately|exclusive, 0, 1, 0, uintptr(unsafe.Pointer(&offset)))
	if result != 0 {
		return nil
	}
	if errors.Is(err, lockViolation) {
		return errStoreBusy
	}
	return err
}

func unlockWriter(file *os.File) error {
	var offset syscall.Overlapped
	result, _, err := unlockFileEx.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&offset)))
	if result == 0 {
		return err
	}
	return nil
}

func replaceAtomic(source, destination string) error {
	const (
		replaceExisting = 1
		writeThrough    = 8
	)
	from, err := replacementPath(source)
	if err != nil {
		return err
	}
	to, err := replacementPath(destination)
	if err != nil {
		return err
	}
	// No copy fallback: both names are on the same volume and replacement must be atomic.
	result, _, err := moveFileEx.Call(uintptr(unsafe.Pointer(from)), uintptr(unsafe.Pointer(to)), replaceExisting|writeThrough)
	if result != 0 {
		return nil
	}
	if _, inspectErr := os.Lstat(source); inspectErr != nil {
		return errors.Join(errWriteUncertain, err, inspectErr)
	}
	return err
}

func replacementPath(path string) (*uint16, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Extended-length syntax keeps this call consistent with os.CreateTemp on long paths.
	if !strings.HasPrefix(absolute, "\\\\?\\") && !strings.HasPrefix(absolute, "\\\\.\\") {
		if rest, ok := strings.CutPrefix(absolute, "\\\\"); ok {
			absolute = "\\\\?\\UNC\\" + rest
		} else {
			absolute = "\\\\?\\" + absolute
		}
	}
	return syscall.UTF16PtrFromString(absolute)
}
