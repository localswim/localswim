package v2

import (
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/localswim/localswim/internal/backuptransfer"
)

// readUploadedBackup stages a bounded request on disk and verifies every member
// before returning any candidate state. The caller owns archive.close().
func (x *request) readUploadedBackup(boardID string) (*verifiedBackupArchive, error) {
	if x.r.ContentLength > MaxBackupArchiveBytes {
		return nil, errTooBig
	}
	controller := http.NewResponseController(x.w)
	uploadEnd := time.Now().Add(backuptransfer.UploadBudget(x.r.ContentLength))
	// Keep a response window even when body transfer ends in a timeout.
	if err := controller.SetWriteDeadline(uploadEnd.Add(backuptransfer.ReplyBudget(0))); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, err
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
	body := backupUploadReader{
		Reader:     http.MaxBytesReader(x.w, x.r.Body, MaxBackupArchiveBytes),
		controller: controller, end: uploadEnd,
	}
	size, err := io.Copy(file, body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, errTooBig
		}
		return nil, invalid("body", "Unreadable recovery archive.")
	}
	// Start the response window before verification. It stays in effect through
	// repair, commit, and net/http's final response flush.
	if err := controller.SetWriteDeadline(time.Now().Add(backuptransfer.ReplyBudget(size))); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, err
	}
	archive, err := inspectBackupArchiveSize(file, size, boardID)
	if err != nil {
		return nil, invalid("body", "The native backup archive failed validation.")
	}
	verified = true
	return archive, nil
}

// Keep slow uploads under an absolute limit and refuse stalled reads without
// extending the absolute deadline as the client makes progress.
type backupUploadReader struct {
	io.Reader
	controller *http.ResponseController
	end        time.Time
}

func (r backupUploadReader) Read(buffer []byte) (int, error) {
	now := time.Now()
	if !now.Before(r.end) {
		return 0, os.ErrDeadlineExceeded
	}
	deadline := now.Add(backuptransfer.BodyIdle)
	if r.end.Before(deadline) {
		deadline = r.end
	}
	if err := r.controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return 0, err
	}
	return r.Reader.Read(buffer)
}
