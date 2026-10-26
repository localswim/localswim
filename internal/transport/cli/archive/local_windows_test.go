//go:build windows && amd64 && k03_archive_native

package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	A "github.com/localswim/localswim/internal/contract/archive"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

func nativeTestRoot(t *testing.T) string {
	t.Helper()
	root, e := setup.K03ArchiveQualificationRoot()
	if e != nil || root == "" || os.Getenv("K03_ARCHIVE_NATIVE_OVERLAY") != "1" {
		t.Fatal("compiled private root-only overlay required")
	}
	n, e := newLocalFS()
	if e != nil {
		t.Fatal("native policy unavailable")
	}
	hash := sha256.Sum256([]byte(t.Name()))
	path := filepath.Join(filepath.Dir(root), hex.EncodeToString(hash[:4]))
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: n.sd}
	if e = windows.CreateDirectory(localPointer(path), sa); e != nil {
		t.Fatal("private fixture create failed")
	}
	return path
}

func fixtureZIP(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("../../../..", "fixtures/contracts/archive-recovery/package")
	names := []string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json"}
	blobs, e := filepath.Glob(filepath.Join(root, "blobs", "*"))
	if e != nil {
		t.Fatal("fixture inventory")
	}
	for _, p := range blobs {
		names = append(names, "blobs/"+filepath.Base(p))
	}
	names = append(names, "COMPLETE")
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range names {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if e != nil {
			t.Fatal("fixture bytes")
		}
		w, e := z.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, ReaderVersion: 20, CreatorVersion: 20, CRC32: crc32.ChecksumIEEE(b), CompressedSize64: uint64(len(b)), UncompressedSize64: uint64(len(b))})
		if e != nil {
			t.Fatal("fixture member")
		}
		if _, e = w.Write(b); e != nil {
			t.Fatal("fixture ZIP")
		}
	}
	if z.Close() != nil {
		t.Fatal("fixture close")
	}
	return out.Bytes()
}

func treeHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if !d.IsDir() {
			raw, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			out[path] = sha(raw)
		}
		return nil
	})
	if e != nil {
		t.Fatal("tree read")
	}
	return out
}

func TestNativeOfflineRestoreAndReadOnlyStatusComposition(t *testing.T) {
	root := nativeTestRoot(t)
	input := filepath.Join(root, "in")
	output := filepath.Join(root, "out")
	if os.Mkdir(input, 0700) != nil || os.Mkdir(output, 0700) != nil {
		t.Fatal("fixture directories")
	}
	source := filepath.Join(input, "source.zip")
	raw := fixtureZIP(t)
	if nativeSave(context.Background(), source, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("source fixture publication")
	}
	destination := filepath.Join(output, "restored")
	out := offline(context.Background(), invocation{kind: "restore create", source: source, destination: destination}, []string{input}, io.Discard)
	status, ok := out.value.(A.Status)
	if !ok || out.exit != 0 || !out.localComplete || status.Outcome != "confirmed" || status.Result.Destination == nil || status.Result.SourceIdentity == nil || status.Result.Destination.Incarnation == status.Result.SourceIdentity.Incarnation {
		t.Fatal("A04 restore not confirmed")
	}
	read, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(raw, read) {
		t.Fatal("source changed")
	}
	receiptBytes, e := os.ReadFile(filepath.Join(destination, "restore-receipt.json"))
	if e != nil {
		t.Fatal("receipt unavailable")
	}
	receipt, e := A.DecodeReadable[A.Receipt](receiptBytes)
	if e != nil {
		t.Fatal("receipt invalid")
	}
	before := treeHashes(t, root)
	for i := 0; i < 2; i++ {
		check := offline(context.Background(), invocation{kind: "restore status", destination: destination, request: string(receipt.RequestID)}, nil, io.Discard)
		if check.exit != 0 || check.value.(A.Status).Outcome != "confirmed" {
			t.Fatal("status not confirmed")
		}
	}
	after := treeHashes(t, root)
	if len(before) != len(after) {
		t.Fatal("status created files")
	}
	for p, h := range before {
		if after[p] != h {
			t.Fatal("status changed bytes")
		}
	}
	wrong := offline(context.Background(), invocation{kind: "restore status", destination: destination, request: "00000000-0000-4000-8000-000000000099"}, nil, io.Discard)
	if wrong.exit != 4 {
		t.Fatal("wrong request not unknown")
	}
	duplicate := offline(context.Background(), invocation{kind: "restore create", source: source, destination: destination}, []string{input}, io.Discard)
	if duplicate.exit != 4 {
		t.Fatal("restore overwrite admitted")
	}
	missing := offline(context.Background(), invocation{kind: "restore create", source: source, destination: filepath.Join(output, "missing-config")}, nil, io.Discard)
	if missing.exit != 4 {
		t.Fatal("missing protected roots admitted")
	}
	partial := destination + ".partial-" + string(receipt.RequestID)
	if os.Mkdir(partial, 0700) != nil {
		t.Fatal("ambiguity fixture")
	}
	ambiguous := offline(context.Background(), invocation{kind: "restore status", destination: destination, request: string(receipt.RequestID)}, nil, io.Discard)
	if ambiguous.exit != 4 {
		t.Fatal("competing partial not unknown")
	}
}

func TestNativeRestoreInvalidPackageFailsAndAbsentStatusStaysUnknown(t *testing.T) {
	root := nativeTestRoot(t)
	input := filepath.Join(root, "in")
	output := filepath.Join(root, "out")
	if os.Mkdir(input, 0700) != nil || os.Mkdir(output, 0700) != nil {
		t.Fatal("fixture parents")
	}
	source := filepath.Join(input, "invalid.zip")
	raw := []byte("incomplete package")
	if nativeSave(context.Background(), source, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("source fixture")
	}
	destination := filepath.Join(output, "invalid")
	var diag bytes.Buffer
	failed := offline(context.Background(), invocation{kind: "restore create", source: source, destination: destination}, []string{input}, &diag)
	if failed.exit != 1 || failed.value.(A.Status).Outcome != "failed" {
		t.Fatal("conclusive invalid package not failed")
	}
	paths, _ := filepath.Glob(destination + "*")
	if len(paths) != 0 {
		t.Fatal("invalid source created receipt or destination")
	}
	unknown := offline(context.Background(), invocation{kind: "restore status", destination: destination, request: "00000000-0000-4000-8000-000000000099"}, nil, io.Discard)
	if unknown.exit != 4 || unknown.value.(A.Status).Outcome != "unknown" {
		t.Fatal("absent status fabricated outcome")
	}
	got, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("invalid source changed")
	}
}

func TestNativeLocalSaveNoOverwriteAndExactRawReadback(t *testing.T) {
	root := nativeTestRoot(t)
	path := filepath.Join(root, "new.bin")
	raw := []byte{0, 255, 10, 13, 0}
	if nativeSave(context.Background(), path, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("save not verified")
	}
	b, e := nativeRead(context.Background(), path, 100)
	if e != nil || !bytes.Equal(raw, b) {
		t.Fatal("raw mismatch")
	}
	if nativeSave(context.Background(), path, []byte("different"), []string{}, func(string) error { return nil }) != saveFailed {
		t.Fatal("overwrite admitted")
	}
	b, e = nativeRead(context.Background(), path, 100)
	if e != nil || !bytes.Equal(raw, b) {
		t.Fatal("existing bytes changed")
	}
	n, _ := newLocalFS()
	f, e := n.file(path, false, false, true)
	if e != nil {
		t.Fatal("published file ACL invalid")
	}
	f.Close()
}

func TestNativeLocalSaveFaultsNeverClaimVerifiedPublication(t *testing.T) {
	root := nativeTestRoot(t)
	for _, stage := range []string{"file-flush", "file-readback", "before-rename", "after-rename", "published-readback"} {
		path := filepath.Join(root, stage+".bin")
		got := nativeSave(context.Background(), path, []byte("frozen"), []string{}, func(s string) error {
			if s == stage {
				return errors.New("synthetic fault")
			}
			return nil
		})
		want := saveFailed
		if stage == "after-rename" || stage == "published-readback" {
			want = saveUnknown
		}
		if got != want {
			t.Fatal("fault misclassified", stage)
		}
		_, e := os.Lstat(path)
		if want == saveFailed && !os.IsNotExist(e) {
			t.Fatal("prepublication fault created final")
		}
		if want == saveUnknown && e != nil {
			t.Fatal("published candidate disappeared")
		}
	}
}

func TestNativeLocalPathsOverlapLinksAndSourceBounds(t *testing.T) {
	root := nativeTestRoot(t)
	path := filepath.Join(root, "new.bin")
	for _, p := range []string{path + ".", path + " ", filepath.Join(root, "NUL.bin"), path + ":ads", filepath.Join(root, "a~1.bin"), `\\localhost\share\x`} {
		if nativeSave(context.Background(), p, []byte("x"), []string{}, func(string) error { return nil }) != saveFailed {
			t.Fatal("alias admitted")
		}
	}
	if nativeSave(context.Background(), path, []byte("x"), nil, func(string) error { return nil }) != saveFailed {
		t.Fatal("missing boundary configuration admitted")
	}
	if nativeSave(context.Background(), path, []byte("x"), []string{root}, func(string) error { return nil }) != saveFailed {
		t.Fatal("overlap admitted")
	}
	if nativeSave(context.Background(), path, []byte("bytes"), []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("fixture save failed")
	}
	if _, e := nativeRead(context.Background(), path, 4); e == nil {
		t.Fatal("oversize input read")
	}
	link := filepath.Join(root, "linked.bin")
	if e := os.Link(path, link); e != nil {
		t.Fatal("hardlink fixture unavailable")
	}
	if _, e := nativeRead(context.Background(), path, 10); e == nil {
		t.Fatal("hardlinked input admitted")
	}
	if _, e := nativeRead(context.Background(), link, 10); e == nil {
		t.Fatal("hardlinked alias admitted")
	}
}

func TestNativeJunctionAncestorAndPublishedACLChangeRefused(t *testing.T) {
	root := nativeTestRoot(t)
	n, _ := newLocalFS()
	target := filepath.Join(root, "target")
	alias := filepath.Join(root, "alias")
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: n.sd}
	if windows.CreateDirectory(localPointer(target), sa) != nil || windows.CreateDirectory(localPointer(alias), sa) != nil {
		t.Fatal("junction fixture directories")
	}
	h, e := windows.CreateFile(localPointer(alias), windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal("junction fixture handle")
	}
	sub := utf16.Encode([]rune(`\??\` + target))
	display := utf16.Encode([]rune(target))
	names := append(append(append(sub, 0), display...), 0)
	buf := make([]byte, 16+len(names)*2)
	binary.LittleEndian.PutUint32(buf, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buf[12:], uint16((len(sub)+1)*2))
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(display)*2))
	for i, v := range names {
		binary.LittleEndian.PutUint16(buf[16+i*2:], v)
	}
	var returned uint32
	e = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	windows.CloseHandle(h)
	if e != nil {
		t.Fatal("junction fixture unavailable")
	}
	if nativeSave(context.Background(), filepath.Join(alias, "alias.bin"), []byte("x"), []string{}, func(string) error { return nil }) != saveFailed {
		t.Fatal("junction ancestor admitted")
	}
	path := filepath.Join(root, "acl.bin")
	mutated := false
	got := nativeSave(context.Background(), path, []byte("private"), []string{}, func(stage string) error {
		if stage != "published-readback" {
			return nil
		}
		sd, e := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
		if e != nil {
			t.Fatal("broad ACL fixture")
		}
		dacl, _, _ := sd.DACL()
		if windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil) == nil {
			mutated = true
		}
		return nil
	})
	if !mutated {
		t.Fatal("published ACL change fixture unavailable")
	}
	if got != saveUnknown {
		t.Fatal("changed published ACL claimed verified")
	}
}

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, errors.New("synthetic output unavailable") }

func TestNativeRestoreRequestDeliveryGateAndCompletedFileOutputSeparation(t *testing.T) {
	root := nativeTestRoot(t)
	input := filepath.Join(root, "in")
	output := filepath.Join(root, "out")
	if os.Mkdir(input, 0700) != nil || os.Mkdir(output, 0700) != nil {
		t.Fatal("fixture parents")
	}
	source := filepath.Join(input, "source.zip")
	raw := fixtureZIP(t)
	if nativeSave(context.Background(), source, raw, []string{}, func(string) error { return nil }) != saveConfirmed {
		t.Fatal("source save")
	}
	runner := Runner{ProtectedRoots: []string{input}}
	denied := filepath.Join(output, "denied")
	var out bytes.Buffer
	if runner.Run(context.Background(), []string{"--json", "restore", "create", "--source", source, "--destination", denied}, nil, &out, failedOutput{}) != 1 {
		t.Fatal("request diagnostic failure must refuse before effects")
	}
	candidates, _ := filepath.Glob(denied + "*")
	if len(candidates) != 0 {
		t.Fatal("request diagnostic failure constructed destination")
	}
	destination := filepath.Join(output, "complete")
	var diagnostic bytes.Buffer
	if runner.Run(context.Background(), []string{"--json", "restore", "create", "--source", source, "--destination", destination}, nil, failedOutput{}, &diagnostic) != 4 {
		t.Fatal("completed publication/output failure not separate")
	}
	request := strings.TrimSuffix(strings.TrimPrefix(diagnostic.String(), "RESTORE_REQUEST "), "\n")
	if !validID(request) {
		t.Fatal("request reference unavailable")
	}
	receiptBytes, e := os.ReadFile(filepath.Join(destination, "restore-receipt.json"))
	if e != nil {
		t.Fatal("completed destination lost")
	}
	receipt, e := A.DecodeReadable[A.Receipt](receiptBytes)
	if e != nil || string(receipt.RequestID) != request || receipt.Phase != "published" || receipt.Result != "complete" {
		t.Fatal("completed receipt downgraded")
	}
	out.Reset()
	if runner.Run(context.Background(), []string{"--json", "restore", "status", "--destination", destination, "--request-id", request}, nil, &out, io.Discard) != 0 {
		t.Fatal("explicit status did not confirm")
	}
	status, e := A.Decode[A.Status](out.Bytes())
	if e != nil || status.Outcome != "confirmed" {
		t.Fatal("standalone status output invalid")
	}
	out.Reset()
	if runner.Run(context.Background(), []string{"restore", "status", "--destination", destination, "--request-id", request}, nil, &out, io.Discard) != 0 {
		t.Fatal("human status unavailable")
	}
	first := strings.SplitN(out.String(), "\n", 2)[0]
	if status.Result.SourceIdentity == nil || first != "restore confirmed board "+string(status.Result.SourceIdentity.BoardID) {
		t.Fatal("human restore identity missing")
	}
	got, e := os.ReadFile(source)
	if e != nil || !bytes.Equal(got, raw) {
		t.Fatal("source changed after output failure")
	}
}
