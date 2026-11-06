//go:build windows && amd64

package local

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/localswim/localswim/internal/board/notices"
	"golang.org/x/sys/windows"
)

func noticeNativeSignal(dir, databaseKey string) (string, error) {
	main := filepath.Join(dir, "board.sqlite")
	db, err := noticeFileSignal(main, databaseKey)
	if err != nil {
		return "", err
	}
	wal, err := noticeFileSignal(main+"-wal", "")
	if err != nil {
		return "", err
	}
	return db + "/" + wal, nil
}

func noticeFileSignal(path, databaseKey string) (signal string, err error) {
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE)
	if databaseKey == "" {
		// A transient WAL may disappear. It is only a hint, never evidence
		// of committed events or permission to open SQLite in WAL mode.
		share |= windows.FILE_SHARE_DELETE
	}
	f, err := workspaceOpen(path, windows.GENERIC_READ, windows.OPEN_EXISTING, share)
	if err != nil {
		if databaseKey == "" && errors.Is(err, ErrWorkspaceMissing) {
			return "absent", nil
		}
		return "", notices.ErrStorage
	}
	defer func() {
		if f.Close() != nil && err == nil {
			signal, err = "", notices.ErrStorage
		}
	}()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info) != nil {
		return "", notices.ErrStorage
	}
	key := fmt.Sprintf("%08x:%08x%08x", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow)
	if databaseKey != "" && key != databaseKey {
		return "", notices.ErrForeign
	}
	// In the qualified DELETE layout a committed write changes the SQLite
	// header's change counter. NTFS write times alone can stay stale while
	// the long-lived writer handle is still open. Sample only DB/WAL bytes.
	var header [100]byte
	n, readErr := io.ReadFull(f, header[:])
	if readErr != nil && (databaseKey != "" || !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF)) {
		return "", notices.ErrStorage
	}
	if databaseKey == "" {
		return fmt.Sprintf("%s:%08x%08x:%08x%08x:%x", key, info.FileSizeHigh, info.FileSizeLow,
			info.LastWriteTime.HighDateTime, info.LastWriteTime.LowDateTime, sha256.Sum256(header[:n])), nil
	}
	return fmt.Sprintf("%s:%08x%08x:%x", key, info.FileSizeHigh, info.FileSizeLow, sha256.Sum256(header[:n])), nil
}
