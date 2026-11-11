package v2

import (
	"archive/zip"
	"cmp"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	zipDataDescriptorFlag      = 1 << 3
	zipDataDescriptorSignature = 0x08074b50
	zipSizeSentinel            = 0xffffffff
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
	// The central directory can list members in a different order than the
	// local headers. Bound each member by the next physical record.
	order := make([]int, len(reader.File))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(directory.localOffsets[a], directory.localOffsets[b])
	})
	archive := &verifiedBackupArchive{file: file, entries: make(map[string]*zip.File, len(reader.File))}
	for i, index := range order {
		entry := reader.File[index]
		limit := directory.offset
		if i+1 < len(order) {
			limit = directory.localOffsets[order[i+1]]
		}
		if !entry.Mode().IsRegular() || archive.entries[entry.Name] != nil || entry.Flags&1 != 0 ||
			!directory.matchesLocalMember(directory.localOffsets[index], limit, entry) {
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

// archive/zip opens each member using its central metadata. Compare the local
// header and any trailing data descriptor with that metadata.
func (d backupZIPDirectory) matchesLocalMember(offset, limit int64, entry *zip.File) bool {
	var header [zipLocalHeaderSize]byte
	if offset > limit-zipLocalHeaderSize {
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
		nameSize > maxBackupEntryNameSize || payload > limit ||
		entry.CompressedSize64 > uint64(limit-payload) {
		return false
	}
	var name [maxBackupEntryNameSize]byte
	if _, err := d.file.ReadAt(name[:nameSize], offset+zipLocalHeaderSize); err != nil ||
		string(name[:nameSize]) != entry.Name {
		return false
	}
	if entry.Flags&zipDataDescriptorFlag != 0 {
		return d.matchesDataDescriptor(payload+int64(entry.CompressedSize64), limit, &entry.FileHeader)
	}

	compressed, uncompressed := le.Uint32(header[18:]), le.Uint32(header[22:])
	if le.Uint32(header[14:]) != entry.CRC32 ||
		(compressed != zipSizeSentinel && uint64(compressed) != entry.CompressedSize64) ||
		(uncompressed != zipSizeSentinel && uint64(uncompressed) != entry.UncompressedSize64) {
		return false
	}
	return compressed != zipSizeSentinel && uncompressed != zipSizeSentinel ||
		d.matchesLocalZIP64Sizes(payload-int64(extraSize), extraSize, &entry.FileHeader)
}

// When the local header needs ZIP64 sizes, the extra field carries the
// uncompressed size followed by the compressed size.
func (d backupZIPDirectory) matchesLocalZIP64Sizes(offset int64, size int, entry *zip.FileHeader) bool {
	extra := make([]byte, size)
	if _, err := d.file.ReadAt(extra, offset); err != nil {
		return false
	}
	le := binary.LittleEndian
	found := false
	for len(extra) >= 4 {
		tag, length := le.Uint16(extra), int(le.Uint16(extra[2:]))
		if length > len(extra)-4 {
			return false
		}
		if tag == zip64ExtraFieldID {
			if found || length < 16 ||
				le.Uint64(extra[4:]) != entry.UncompressedSize64 ||
				le.Uint64(extra[12:]) != entry.CompressedSize64 {
				return false
			}
			found = true
		}
		extra = extra[4+length:]
	}
	return found && len(extra) == 0
}

// Native ZIP members with a descriptor pack it immediately before the next
// local header or the directory. The span distinguishes the optional signature
// from the CRC and 32-bit sizes from ZIP64 sizes.
func (d backupZIPDirectory) matchesDataDescriptor(offset, limit int64, entry *zip.FileHeader) bool {
	var descriptor [24]byte
	size := limit - offset
	switch size {
	case 12, 16, 20, 24:
	default:
		return false
	}
	data := descriptor[:size]
	if _, err := d.file.ReadAt(data, offset); err != nil {
		return false
	}
	le := binary.LittleEndian
	if size == 16 || size == 24 {
		if le.Uint32(data) != zipDataDescriptorSignature {
			return false
		}
		data = data[4:]
	}
	if le.Uint32(data) != entry.CRC32 {
		return false
	}
	if len(data) == 12 {
		return uint64(le.Uint32(data[4:])) == entry.CompressedSize64 &&
			uint64(le.Uint32(data[8:])) == entry.UncompressedSize64
	}
	return le.Uint64(data[4:]) == entry.CompressedSize64 &&
		le.Uint64(data[12:]) == entry.UncompressedSize64
}

func readBackupEntry(entry *zip.File, limit int64) ([]byte, error) {
	// Deflate can grow incompressible input slightly. Bound compressed reads
	// as well so malformed members cannot consume arbitrary input bytes.
	if entry == nil || entry.UncompressedSize64 > uint64(limit) ||
		entry.CompressedSize64 > uint64(limit)+uint64(limit)/8+(64<<10) {
		return nil, errBackupIntegrity
	}
	// The inspector already checked the descriptor against the directory.
	// Keep archive/zip's bounded decompression without having it infer a
	// descriptor signature from a potentially identical CRC.
	member := *entry
	member.Flags &^= zipDataDescriptorFlag
	reader, err := member.Open()
	if err != nil {
		return nil, errBackupIntegrity
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	if errors.Join(readErr, reader.Close()) != nil || int64(len(data)) > limit || uint64(len(data)) != entry.UncompressedSize64 {
		return nil, errBackupIntegrity
	}
	// archive/zip treats a zero CRC without a descriptor as unspecified.
	if entry.CRC32 == 0 && crc32.ChecksumIEEE(data) != 0 {
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
