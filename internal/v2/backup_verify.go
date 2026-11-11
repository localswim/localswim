package v2

import (
	"errors"
	"os"
	"path/filepath"
)

// backupVerification summarizes the audited content, including detached attachments.
type backupVerification struct {
	BoardID         string `json:"board_id"`
	BoardRevision   uint64 `json:"board_revision"`
	AttachmentCount int    `json:"attachment_count"`
}

// VerifyBackupFile verifies a portable backup using only the supplied read-only
// file. It never opens service data or extracts a ZIP member.
func VerifyBackupFile(path string) (backupVerification, error) {
	if path == "" || path == "-" {
		return backupVerification{}, errors.New("expected a local backup ZIP file")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return backupVerification{}, errBackupIntegrity
	}
	defer root.Close()
	name := filepath.Base(path)
	file, err := openBackupFile(root, name, maxSnapshotInteger)
	if err != nil {
		return backupVerification{}, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || before.Size() > maxSnapshotInteger {
		return backupVerification{}, errBackupIntegrity
	}
	archive, err := inspectBackupArchiveSize(file, before.Size(), "")
	if err != nil {
		return backupVerification{}, err
	}
	after, err := root.Lstat(name)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return backupVerification{}, errBackupIntegrity
	}
	return backupVerification{
		BoardID:         archive.state.Board.ID,
		BoardRevision:   archive.state.Board.Revision,
		AttachmentCount: len(archive.state.Attachments),
	}, nil
}
