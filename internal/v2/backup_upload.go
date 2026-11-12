package v2

import (
	"errors"
	"io"
	"net/http"
	"os"
)

// readUploadedBackup stages a bounded request on disk and verifies every member
// before returning any candidate state. The caller owns archive.close().
func (x *request) readUploadedBackup(boardID string) (*verifiedBackupArchive, error) {
	if x.r.ContentLength > MaxBackupArchiveBytes {
		return nil, errTooBig
	}
	file, err := os.CreateTemp("", "localswim-recovery-*")
	if err != nil {
		return nil, err
	}
	verified := false
	defer func() {
		if !verified {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}()
	if _, err := io.Copy(file, http.MaxBytesReader(x.w, x.r.Body, MaxBackupArchiveBytes)); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, errTooBig
		}
		return nil, invalid("body", "Unreadable recovery archive.")
	}
	archive, err := inspectBackupArchive(file, boardID)
	if err != nil {
		return nil, invalid("body", "The native backup archive failed validation.")
	}
	verified = true
	return archive, nil
}
