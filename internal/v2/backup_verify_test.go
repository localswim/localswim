package v2

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestM96VerifyDownloadedBackupWithDetachedAttachment(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Portable backup verification"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	data := bytes.Repeat([]byte{'a'}, maxAttachmentBytes)
	var detached attachment
	for i := range 10 {
		raw, _ = editor.call("GET", "/cards/"+c.ID, "", "", 200)
		c = turnOneDecode[card](t, raw)
		raw, headers := attachmentHTTPUpload(t, editor, []attachmentFormField{
			{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
		}, fmt.Sprintf("fixture-%d.txt", i), data, 201)
		if i == 0 {
			detached = turnOneDecode[attachment](t, raw)
			attachmentHTTPDetach(t, editor, "/attachments/"+detached.ID,
				detached.entityTag(), headers.Get("X-Board-ETag"), 204)
		}
	}
	live, err := app.store.readback(b.ID)
	if err != nil || live.Attachments[0].Active {
		t.Fatalf("expected detached attachment in durable state: %v", err)
	}
	snapshotPath := app.store.snapshotPath(b.ID)
	originalBoard := m96FileDigest(t, snapshotPath)
	request := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`, b.ID, live.Board.entityTag())
	raw, _ = editor.call("POST", "/backups", request, "", 201)
	item := turnOneDecode[backup](t, raw)

	req, err := http.NewRequest("GET", editor.url+Prefix+"/backups/"+item.ID+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+editor.token)
	response, err := editor.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("backup download status %d", response.StatusCode)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "download.zip")
	n := m96WriteArchive(t, path, response.Body)
	if n != item.Size || n <= maxSnapshotBytes || m96FileDigest(t, path) != item.SHA256 {
		t.Fatalf("expected verified download over 96 MiB, got %d bytes", n)
	}
	verified, err := VerifyBackupFile(path)
	if err != nil || verified != (backupVerification{b.ID, live.Board.Revision, 10}) {
		t.Fatalf("downloaded backup verification: %+v, %v", verified, err)
	}

	original, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	corruptPath := filepath.Join(dir, "corrupt.zip")
	if got := m96WriteArchive(t, corruptPath, original); got != n {
		t.Fatalf("corrupt fixture copy size %d, want %d", got, n)
	}
	m96CorruptAttachment(t, corruptPath, detached)
	corruptDigest := m96FileDigest(t, corruptPath)
	if _, err := VerifyBackupFile(corruptPath); !errors.Is(err, errBackupIntegrity) {
		t.Fatalf("corrupt detached member verification: %v", err)
	}
	accepted, ok := app.store.accepted(b.ID)
	if !ok || accepted.Board.Revision != live.Board.Revision || m96FileDigest(t, snapshotPath) != originalBoard {
		t.Fatal("verification changed live or durable board state")
	}
	if m96FileDigest(t, path) != item.SHA256 || m96FileDigest(t, corruptPath) != corruptDigest ||
		m96FileDigest(t, filepath.Join(app.store.root, "backups", item.ID, "content.zip")) != item.SHA256 {
		t.Fatal("verification changed archive bytes")
	}
	t.Logf("verified %d-byte download at revision %d with %d attachments, including detached content", n, verified.BoardRevision, verified.AttachmentCount)
	t.Log("corrupt detached content refused; both input files, source backup, and accepted and durable board remained unchanged")
}

func m96WriteArchive(t *testing.T, path string, src io.Reader) int64 {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	n, err := io.Copy(file, src)
	if err := errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	return n
}

func m96FileDigest(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.New()
	_, err = io.Copy(digest, file)
	if err := errors.Join(err, file.Close()); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", digest.Sum(nil))
}

func m96CorruptAttachment(t *testing.T, path string, item attachment) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(file, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		if entry.Name != filepath.ToSlash(attachmentPath(item)) {
			continue
		}
		offset, err := entry.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt([]byte{'b'}, offset); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatal("missing detached attachment member")
}

func TestM96VerifyRejectsInterruptedAndNonregularInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "interrupted.zip")
	if err := os.WriteFile(path, []byte("PK\x03\x04"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{path, dir, filepath.Join(dir, "missing.zip")} {
		if _, err := VerifyBackupFile(input); !errors.Is(err, errBackupIntegrity) {
			t.Fatalf("unusable input verification: %v", err)
		}
	}
}

func TestM96RejectsLocalHeaderMismatch(t *testing.T) {
	app, _, source, _ := backupCaptureFixture(t)
	_, original := captureRecoveryBackup(t, app, source)
	for _, test := range []struct {
		field  string
		offset int
		mask   byte
	}{
		{"name", zipLocalHeaderSize, 1},
		{"flags", 7, 8},
		{"method", 8, byte(zip.Deflate)},
	} {
		t.Run(test.field, func(t *testing.T) {
			// Capture puts the board snapshot first. Alter only one byte
			// of its local header, leaving the directory and payload intact.
			modified := bytes.Clone(original)
			modified[test.offset] ^= test.mask
			reader, err := zip.NewReader(bytes.NewReader(modified), int64(len(modified)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readBackupEntry(reader.File[0], maxSnapshotBytes); err != nil {
				t.Fatalf("fixture unexpectedly broke the ZIP member payload: %v", err)
			}
			path := filepath.Join(t.TempDir(), "mismatched.zip")
			if err := os.WriteFile(path, modified, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyBackupFile(path); !errors.Is(err, errBackupIntegrity) {
				t.Fatalf("offline verifier accepted local %s mismatch: %v", test.field, err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := inspectBackupArchive(file, source.Board.ID); !errors.Is(err, errBackupIntegrity) {
				t.Fatalf("restore inspector accepted local %s mismatch: %v", test.field, err)
			}
		})
	}
}

func TestM96RejectsCapturedDataDescriptorSizeMismatch(t *testing.T) {
	app, _, source, _ := backupCaptureFixture(t)
	_, original := captureRecoveryBackup(t, app, source)
	reader, err := zip.NewReader(bytes.NewReader(original), int64(len(original)))
	if err != nil {
		t.Fatal(err)
	}
	entry := reader.File[0]
	payload, err := entry.DataOffset()
	if err != nil || entry.Flags&zipDataDescriptorFlag == 0 {
		t.Fatalf("expected a capture-generated descriptor: %v", err)
	}
	descriptor := payload + int64(entry.CompressedSize64)
	if descriptor+16 > int64(len(original)) ||
		binary.LittleEndian.Uint32(original[descriptor:]) != zipDataDescriptorSignature {
		t.Fatal("expected a complete signed capture descriptor")
	}
	for _, test := range []struct {
		field  string
		offset int64
	}{
		{"compressed", 8},
		{"uncompressed", 12},
	} {
		t.Run(test.field, func(t *testing.T) {
			// Preserve the captured payload, CRC, and central directory.
			modified := bytes.Clone(original)
			modified[descriptor+test.offset] ^= 1
			reader, err := zip.NewReader(bytes.NewReader(modified), int64(len(modified)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := readBackupEntry(reader.File[0], maxSnapshotBytes); err != nil {
				t.Fatalf("fixture unexpectedly broke the ZIP member payload: %v", err)
			}
			path := filepath.Join(t.TempDir(), "descriptor.zip")
			if err := os.WriteFile(path, modified, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyBackupFile(path); !errors.Is(err, errBackupIntegrity) {
				t.Fatalf("offline verifier accepted mismatched %s descriptor size: %v", test.field, err)
			}
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := inspectBackupArchive(file, source.Board.ID); !errors.Is(err, errBackupIntegrity) {
				t.Fatalf("restore inspector accepted mismatched %s descriptor size: %v", test.field, err)
			}
		})
	}
}

func TestM96DataDescriptorWidths(t *testing.T) {
	le := binary.LittleEndian
	entry := zip.FileHeader{
		CRC32: zipDataDescriptorSignature, CompressedSize64: 9, UncompressedSize64: 17,
	}
	for _, test := range []struct {
		name   string
		signed bool
		wide   bool
	}{
		{"unsigned32", false, false},
		{"signed32", true, false},
		{"unsigned64", false, true},
		{"signed64", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var data []byte
			if test.signed {
				data = le.AppendUint32(data, zipDataDescriptorSignature)
			}
			data = le.AppendUint32(data, entry.CRC32)
			if test.wide {
				data = le.AppendUint64(data, entry.CompressedSize64)
				data = le.AppendUint64(data, entry.UncompressedSize64)
			} else {
				data = le.AppendUint32(data, uint32(entry.CompressedSize64))
				data = le.AppendUint32(data, uint32(entry.UncompressedSize64))
			}
			directory := backupZIPDirectory{file: bytes.NewReader(data)}
			size := int64(len(data))
			if !directory.matchesDataDescriptor(0, size, &entry) {
				t.Fatal("rejected valid data descriptor")
			}
			if directory.matchesDataDescriptor(0, size-1, &entry) {
				t.Fatal("accepted data descriptor crossing the next record boundary")
			}
		})
	}
}

func TestM96VerifiesZeroCRCWithoutDescriptor(t *testing.T) {
	for _, test := range []struct {
		name  string
		data  []byte
		valid bool
	}{
		{"actualZero", []byte{0x9d, 0x0a, 0xd9, 0x6d}, true},
		{"falseZero", []byte("attachment content"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if (crc32.ChecksumIEEE(test.data) == 0) != test.valid {
				t.Fatal("test payload does not have the expected CRC")
			}
			archive, want := m96OneAttachmentZIP(t, test.data, func(writer *zip.Writer, name string) {
				size := uint64(len(test.data))
				header := &zip.FileHeader{Name: name, Method: zip.Store, CompressedSize64: size, UncompressedSize64: size}
				header.SetMode(0o600)
				member, err := writer.CreateRaw(header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := member.Write(test.data); err != nil {
					t.Fatal(err)
				}
			})
			m96CheckArchive(t, archive, want, test.valid)
		})
	}
}

func TestM96VerifiesUnsignedDescriptorWithSignatureCRC(t *testing.T) {
	data := []byte{0xac, 0x0a, 0x7a, 0xd5}
	if crc32.ChecksumIEEE(data) != zipDataDescriptorSignature {
		t.Fatal("test payload must have a CRC equal to the descriptor signature")
	}
	archive, want := m96OneAttachmentZIP(t, data, func(writer *zip.Writer, name string) {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o600)
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatal(err)
		}
	})
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	member := reader.File[len(reader.File)-1]
	payload, err := member.DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	descriptor := int(payload) + int(member.CompressedSize64)
	directory := int(le.Uint32(archive[len(archive)-zipDirectoryEndSize+16:]))
	if descriptor+16 != directory || le.Uint32(archive[descriptor:]) != zipDataDescriptorSignature {
		t.Fatal("expected the final member's complete signed descriptor")
	}
	// The last member's descriptor is directly before the central directory.
	// Removing only its signature shifts the directory by four bytes.
	unsigned := append(bytes.Clone(archive[:descriptor]), archive[descriptor+4:]...)
	le.PutUint32(unsigned[len(unsigned)-zipDirectoryEndSize+16:], uint32(directory-4))
	m96CheckArchive(t, unsigned, want, true)
}

func TestM96ChecksDescriptorFreeLocalZIP64Sizes(t *testing.T) {
	const name = "member"
	const size = 3
	le := binary.LittleEndian
	extra := le.AppendUint16(nil, zip64ExtraFieldID)
	extra = le.AppendUint16(extra, 16)
	extra = le.AppendUint64(extra, size)
	extra = le.AppendUint64(extra, size)
	entry := &zip.File{FileHeader: zip.FileHeader{
		Name: name, CRC32: crc32.ChecksumIEEE([]byte("abc")),
		CompressedSize64: size, UncompressedSize64: size,
	}}
	var header [zipLocalHeaderSize]byte
	le.PutUint32(header[:], 0x04034b50)
	le.PutUint32(header[14:], entry.CRC32)
	le.PutUint32(header[18:], zipSizeSentinel)
	le.PutUint32(header[22:], zipSizeSentinel)
	le.PutUint16(header[26:], uint16(len(name)))
	le.PutUint16(header[28:], uint16(len(extra)))
	data := bytes.Join([][]byte{header[:], []byte(name), extra, []byte("abc")}, nil)
	directory := backupZIPDirectory{file: bytes.NewReader(data)}
	if !directory.matchesLocalMember(0, int64(len(data)), entry) {
		t.Fatal("rejected valid descriptor-free ZIP64 local header")
	}
	for _, offset := range []int{14, zipLocalHeaderSize + len(name) + 4, zipLocalHeaderSize + len(name) + 12} {
		data[offset] ^= 1
		if directory.matchesLocalMember(0, int64(len(data)), entry) {
			t.Fatal("accepted mismatched local CRC or ZIP64 size")
		}
		data[offset] ^= 1
	}
}

func m96OneAttachmentZIP(t *testing.T, data []byte, writeAttachment func(*zip.Writer, string)) ([]byte, backupVerification) {
	t.Helper()
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"ZIP member verification"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	raw, _ = attachmentHTTPUpload(t, editor, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
	}, "fixture.bin", data, 201)
	item := turnOneDecode[attachment](t, raw)
	snapshot, err := os.ReadFile(app.store.snapshotPath(b.ID))
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	if err := writeBackupEntry(writer, "boards/"+b.ID+".json", snapshot); err != nil {
		t.Fatal(err)
	}
	writeAttachment(writer, filepath.ToSlash(attachmentPath(item)))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes(), backupVerification{b.ID, item.Revision, 1}
}

func m96CheckArchive(t *testing.T, data []byte, want backupVerification, valid bool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := VerifyBackupFile(path)
	if valid && (err != nil || got != want) || !valid && !errors.Is(err, errBackupIntegrity) {
		t.Fatalf("offline verification: got %+v, %v (valid=%t)", got, err, valid)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	restored, err := inspectBackupArchive(file, want.BoardID)
	if valid && (err != nil || restored.state.Board.Revision != want.BoardRevision) ||
		!valid && !errors.Is(err, errBackupIntegrity) {
		t.Fatalf("restore archive verification: %v (valid=%t)", err, valid)
	}
}

func TestM96BoundedZIP64Directory(t *testing.T) {
	var zipped bytes.Buffer
	writer := zip.NewWriter(&zipped)
	if err := writeBackupEntry(writer, "boards/example.json", nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	// Promote a small valid ZIP to ZIP64 so the offset/count path can be
	// exercised without a multi-gigabyte fixture.
	endOffset := zipped.Len() - zipDirectoryEndSize
	end := zipped.Bytes()[endOffset:]
	le := binary.LittleEndian
	directoryOffset := le.Uint32(end[16:])
	record := zipped.Bytes()[directoryOffset:endOffset]
	var offsetField [12]byte
	le.PutUint16(offsetField[:], 1)  // ZIP64 extra field tag
	le.PutUint16(offsetField[2:], 8) // one 64-bit header offset, zero
	le.PutUint16(record[30:], uint16(len(offsetField)))
	le.PutUint32(record[42:], 0xffffffff)
	endOffset += len(offsetField)
	var trailer [zip64DirectoryEndSize + zip64LocatorSize]byte
	le.PutUint32(trailer[:], 0x06064b50)
	le.PutUint64(trailer[4:], zip64DirectoryEndSize-12)
	le.PutUint64(trailer[24:], 1)
	le.PutUint64(trailer[32:], 1)
	le.PutUint64(trailer[40:], uint64(len(record)+len(offsetField)))
	le.PutUint64(trailer[48:], uint64(le.Uint32(end[16:])))
	locator := trailer[zip64DirectoryEndSize:]
	le.PutUint32(locator, 0x07064b50)
	le.PutUint64(locator[8:], uint64(endOffset))
	le.PutUint32(locator[16:], 1)
	le.PutUint32(end[8:], 0xffffffff)
	le.PutUint64(end[12:], 0xffffffffffffffff)
	comment := bytes.Repeat([]byte{'c'}, 0xffff)
	le.PutUint16(end[20:], uint16(len(comment)))
	data := bytes.Join([][]byte{zipped.Bytes()[:directoryOffset], record, offsetField[:], trailer[:], end, comment}, nil)

	read := func() (backupZIPDirectory, error) {
		return readBackupZIPDirectory(bytes.NewReader(data), int64(len(data)))
	}
	directory, err := read()
	if err != nil || len(directory.localOffsets) != 1 || directory.localOffsets[0] != 0 {
		t.Fatalf("ZIP64 local offset rejected: %v", err)
	}
	if archive, err := zip.NewReader(directory, int64(len(data))); err != nil || len(archive.File) != 1 {
		t.Fatalf("ZIP64 directory rejected: %v", err)
	}
	for _, count := range []uint64{2, maxBackupZIPEntries + 1, 1 << 40} {
		le.PutUint64(data[endOffset+24:], count)
		le.PutUint64(data[endOffset+32:], count)
		if _, err := read(); !errors.Is(err, errBackupIntegrity) {
			t.Fatalf("malformed ZIP64 count %d: %v", count, err)
		}
	}
}
