package v2

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
)

// verifiedBackupArchive owns a private copy. No path in the ZIP is extracted or
// used as filesystem authority. Callers can use only the checked exact inventory.
type verifiedBackupArchive struct {
	file         *os.File
	state        snapshot
	snapshotData []byte
	entries      map[string]*zip.File
}

func openVerifiedBackupArchive(rootPath string, item backup) (*verifiedBackupArchive, error) {
	file, err := copyVerifiedBackup(rootPath, item)
	if err != nil {
		return nil, err
	}
	archive, err := inspectBackupArchive(file, item.BoardID)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(file.Name())
		return nil, err
	}
	if archive.state.Board.Revision != item.BoardRevision || len(archive.state.Attachments) != len(item.AttachmentIDs) {
		archive.close()
		return nil, errBackupIntegrity
	}
	ids := make([]string, 0, len(archive.state.Attachments))
	for _, attachment := range archive.state.Attachments {
		ids = append(ids, attachment.ID)
	}
	slices.Sort(ids)
	expected := slices.Clone(item.AttachmentIDs)
	slices.Sort(expected)
	if !slices.Equal(ids, expected) {
		archive.close()
		return nil, errBackupIntegrity
	}
	return archive, nil
}

func inspectBackupArchive(file *os.File, boardID string) (*verifiedBackupArchive, error) {
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errBackupIntegrity
	}
	reader, err := zip.NewReader(file, info.Size())
	if err != nil {
		return nil, errBackupIntegrity
	}
	archive := &verifiedBackupArchive{file: file, entries: make(map[string]*zip.File)}
	for _, entry := range reader.File {
		if !entry.Mode().IsRegular() || archive.entries[entry.Name] != nil || entry.Flags&1 != 0 {
			return nil, errBackupIntegrity
		}
		archive.entries[entry.Name] = entry
	}
	name := "boards/" + boardID + ".json"
	data, err := readBackupEntry(archive.entries[name], maxSnapshotBytes)
	if err != nil {
		return nil, err
	}
	state, err := parseSnapshot(data, boardID)
	if err != nil || len(archive.entries) != len(state.Attachments)+1 {
		return nil, errBackupIntegrity
	}
	archive.state, archive.snapshotData = state, data
	for _, item := range state.Attachments {
		if _, err := archive.attachmentData(item); err != nil {
			return nil, err
		}
	}
	return archive, nil
}

func readBackupEntry(entry *zip.File, limit int64) ([]byte, error) {
	if entry == nil || entry.UncompressedSize64 > uint64(limit) {
		return nil, errBackupIntegrity
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, errBackupIntegrity
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	if errors.Join(readErr, reader.Close()) != nil || int64(len(data)) > limit || uint64(len(data)) != entry.UncompressedSize64 {
		return nil, errBackupIntegrity
	}
	return data, nil
}

func (archive *verifiedBackupArchive) attachmentData(item attachment) ([]byte, error) {
	data, err := readBackupEntry(archive.entries[filepath.ToSlash(attachmentPath(item))], item.Size)
	if err != nil || !attachmentBytesMatch(item, data) {
		return nil, errBackupIntegrity
	}
	return data, nil
}

func attachmentBytesMatch(item attachment, data []byte) bool {
	digest := sha256.Sum256(data)
	mediaType := http.DetectContentType(data)
	return int64(len(data)) == item.Size && hex.EncodeToString(digest[:]) == item.SHA256 &&
		mediaType == item.MediaType && previewableAttachment(mediaType) == item.Previewable
}

func (archive *verifiedBackupArchive) close() {
	_ = archive.file.Close()
	_ = os.Remove(archive.file.Name())
}
