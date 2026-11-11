package v2

import (
	"bytes"
	"encoding/binary"
	"io"
)

const (
	zipDirectoryHeaderSize = 46
	zipDirectoryEndSize    = 22
	zip64DirectoryEndSize  = 56
	zip64LocatorSize       = 20
	zip64ExtraFieldID      = 1
	zipLocalHeaderSize     = 30
	maxBackupEntryNameSize = len("attachments/") + 3*36 + 2

	// A canonical attachment record occupies more than 256 bytes, including
	// its four UUIDs, SHA-2-256 digest, timestamps, and field names. There
	// is one ZIP directory entry per record, plus the snapshot entry.
	maxBackupZIPEntries = maxSnapshotBytes/256 + 1
)

// backupZIPDirectory keeps the verified ZIP index stable while archive/zip reads
// it. The file payload is still read by offset from the original handle.
type backupZIPDirectory struct {
	file         io.ReaderAt
	offset       int64
	data         *bytes.Reader
	localOffsets []int64
}

func (d backupZIPDirectory) ReadAt(p []byte, offset int64) (int, error) {
	if offset >= d.offset {
		return d.data.ReadAt(p, offset-d.offset)
	}
	prefix := min(int64(len(p)), d.offset-offset)
	n, err := d.file.ReadAt(p[:prefix], offset)
	if err != nil || n == len(p) {
		return n, err
	}
	m, err := d.data.ReadAt(p[n:], 0)
	return n + m, err
}

// Preflight the untrusted directory before archive/zip allocates from its entry
// count. ZIP comments have a 16-bit byte length.
func readBackupZIPDirectory(file io.ReaderAt, size int64) (backupZIPDirectory, error) {
	endLength := min(size, zip64DirectoryEndSize+zip64LocatorSize+zipDirectoryEndSize+0xffff)
	if endLength < zipDirectoryEndSize {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	tail := make([]byte, endLength)
	if _, err := file.ReadAt(tail, size-endLength); err != nil {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	endIndex := backupZIPEndIndex(tail)
	if endIndex < 0 {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	end := tail[endIndex:]
	le := binary.LittleEndian
	if le.Uint32(end[4:]) != 0 || le.Uint16(end[8:]) != le.Uint16(end[10:]) {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	entries := uint64(le.Uint16(end[10:]))
	length, offset := uint64(le.Uint32(end[12:])), uint64(le.Uint32(end[16:]))
	trailer := end
	if entries == 0xffff || length == 0xffffffff || offset == 0xffffffff {
		start := endIndex - zip64DirectoryEndSize - zip64LocatorSize
		if start < 0 {
			return backupZIPDirectory{}, errBackupIntegrity
		}
		trailer = tail[start:]
		record, locator := trailer[:zip64DirectoryEndSize], trailer[zip64DirectoryEndSize:]
		if le.Uint32(record) != 0x06064b50 || le.Uint64(record[4:]) != zip64DirectoryEndSize-12 ||
			le.Uint64(record[16:]) != 0 || le.Uint64(record[24:]) != le.Uint64(record[32:]) ||
			le.Uint32(locator) != 0x07064b50 || le.Uint32(locator[4:]) != 0 ||
			le.Uint64(locator[8:]) != uint64(size-int64(len(trailer))) || le.Uint32(locator[16:]) != 1 {
			return backupZIPDirectory{}, errBackupIntegrity
		}
		entries, length, offset = le.Uint64(record[32:]), le.Uint64(record[40:]), le.Uint64(record[48:])
	}
	endOffset := uint64(size - int64(len(trailer)))
	if entries == 0 || entries > maxBackupZIPEntries || length > maxSnapshotBytes ||
		offset > endOffset || endOffset-offset != length {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	data := make([]byte, int(length)+len(trailer))
	if _, err := file.ReadAt(data[:length], int64(offset)); err != nil {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	copy(data[length:], trailer)
	directory := data[:length]
	localOffsets := make([]int64, 0, entries)
	for range entries {
		if len(directory) < zipDirectoryHeaderSize || le.Uint32(directory) != 0x02014b50 {
			return backupZIPDirectory{}, errBackupIntegrity
		}
		nameSize := int(le.Uint16(directory[28:]))
		extraStart := zipDirectoryHeaderSize + nameSize
		extraEnd := extraStart + int(le.Uint16(directory[30:]))
		recordLength := extraEnd + int(le.Uint16(directory[32:]))
		if nameSize == 0 || nameSize > maxBackupEntryNameSize || recordLength > len(directory) {
			return backupZIPDirectory{}, errBackupIntegrity
		}
		local, ok := backupLocalHeaderOffset(directory[:zipDirectoryHeaderSize], directory[extraStart:extraEnd])
		if !ok || local >= offset {
			return backupZIPDirectory{}, errBackupIntegrity
		}
		localOffsets = append(localOffsets, int64(local))
		directory = directory[recordLength:]
	}
	if len(directory) != 0 {
		return backupZIPDirectory{}, errBackupIntegrity
	}
	return backupZIPDirectory{
		file: file, offset: int64(offset), data: bytes.NewReader(data), localOffsets: localOffsets,
	}, nil
}

func backupZIPEndIndex(tail []byte) int {
	le := binary.LittleEndian
	for i := len(tail) - zipDirectoryEndSize; i >= 0; i-- {
		if le.Uint32(tail[i:]) == 0x06054b50 {
			if int(le.Uint16(tail[i+20:])) != len(tail)-i-zipDirectoryEndSize {
				return -1
			}
			return i
		}
	}
	return -1
}

func backupLocalHeaderOffset(header, extra []byte) (uint64, bool) {
	le := binary.LittleEndian
	offset := le.Uint32(header[42:])
	if offset != 0xffffffff {
		return uint64(offset), true
	}
	// ZIP64 values appear only for saturated fields, with the original
	// sizes (when present) preceding the local header offset.
	skip := 0
	if le.Uint32(header[24:]) == 0xffffffff {
		skip += 8
	}
	if le.Uint32(header[20:]) == 0xffffffff {
		skip += 8
	}
	var local uint64
	found := false
	for len(extra) >= 4 {
		tag, size := le.Uint16(extra), int(le.Uint16(extra[2:]))
		if size > len(extra)-4 {
			break
		}
		if tag == zip64ExtraFieldID {
			if found || size < skip+8 {
				return 0, false
			}
			local, found = le.Uint64(extra[4+skip:]), true
		}
		extra = extra[4+size:]
	}
	return local, found
}
