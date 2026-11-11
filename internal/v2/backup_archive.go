package v2

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// An archive opened from stored backup metadata owns a private copy. No ZIP path is extracted or
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
	return inspectBackupArchiveSize(file, info.Size(), boardID)
}

func inspectBackupArchiveSize(file *os.File, size int64, boardID string) (*verifiedBackupArchive, error) {
	directory, err := readBackupZIPDirectory(file, size)
	if err != nil {
		return nil, err
	}
	reader, err := zip.NewReader(directory, size)
	if err != nil || len(reader.File) != len(directory.localOffsets) {
		return nil, errBackupIntegrity
	}
	archive := &verifiedBackupArchive{file: file, entries: make(map[string]*zip.File, len(reader.File))}
	for i, entry := range reader.File {
		if !entry.Mode().IsRegular() || archive.entries[entry.Name] != nil || entry.Flags&1 != 0 ||
			!directory.matchesLocalHeader(directory.localOffsets[i], entry) {
			return nil, errBackupIntegrity
		}
		archive.entries[entry.Name] = entry
	}
	if boardID == "" {
		for name := range archive.entries {
			if suffix, ok := strings.CutPrefix(name, "boards/"); ok {
				id, ok := strings.CutSuffix(suffix, ".json")
				if !ok || !validSnapshotID(id) || boardID != "" {
					return nil, errBackupIntegrity
				}
				boardID = id
			}
		}
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

// archive/zip opens each member using its central metadata. Check the local
// fields too so readers using either header see the same member.
func (d backupZIPDirectory) matchesLocalHeader(offset int64, entry *zip.File) bool {
	var header [zipLocalHeaderSize]byte
	if offset > d.offset-zipLocalHeaderSize {
		return false
	}
	if _, err := d.file.ReadAt(header[:], offset); err != nil {
		return false
	}
	le := binary.LittleEndian
	nameSize, extraSize := int(le.Uint16(header[26:])), int(le.Uint16(header[28:]))
	payload := offset + int64(zipLocalHeaderSize+nameSize+extraSize)
	if le.Uint32(header[:]) != 0x04034b50 || le.Uint16(header[6:]) != entry.Flags ||
		le.Uint16(header[8:]) != entry.Method || nameSize != len(entry.Name) ||
		nameSize > maxBackupEntryNameSize || payload > d.offset ||
		entry.CompressedSize64 > uint64(d.offset-payload) {
		return false
	}
	var name [maxBackupEntryNameSize]byte
	_, err := d.file.ReadAt(name[:nameSize], offset+zipLocalHeaderSize)
	return err == nil && string(name[:nameSize]) == entry.Name
}

func readBackupEntry(entry *zip.File, limit int64) ([]byte, error) {
	// Deflate can grow incompressible input slightly. Bound compressed reads
	// as well so malformed members cannot consume arbitrary input bytes.
	if entry == nil || entry.UncompressedSize64 > uint64(limit) ||
		entry.CompressedSize64 > uint64(limit)+uint64(limit)/8+(64<<10) {
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

func (archive *verifiedBackupArchive) close() {
	_ = archive.file.Close()
	_ = os.Remove(archive.file.Name())
}
