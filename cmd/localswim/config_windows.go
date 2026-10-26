//go:build windows && amd64

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Config is nonsecret selection data. This reader pins native ancestor/file
// identities only; credentials/evidence remain exclusively in their owned stores.
func readConfig(path string) (runtimeConfig, error) {
	if !safeAbsolute(path) || len(path) == 3 {
		return runtimeConfig{}, errComposition
	}
	closeDirs, e := pinCanonicalDirectory(filepath.Dir(path), false)
	if e != nil {
		return runtimeConfig{}, errComposition
	}
	defer closeDirs()
	p, _ := windows.UTF16PtrFromString(path)
	h, e := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if e != nil {
		return runtimeConfig{}, errComposition
	}
	f := os.NewFile(uintptr(h), "runtime-config")
	if f == nil {
		_ = windows.CloseHandle(h)
		return runtimeConfig{}, errComposition
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow > 64*1024 || !canonicalHandle(h, path) {
		return runtimeConfig{}, errComposition
	}
	if kind, e := windows.GetFileType(h); e != nil || kind != windows.FILE_TYPE_DISK {
		return runtimeConfig{}, errComposition
	}
	raw, e := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if e != nil || len(raw) != int(info.FileSizeLow) {
		return runtimeConfig{}, errComposition
	}
	return parseConfig(raw)
}

func canonicalHandle(h windows.Handle, expected string) bool {
	var normalized [32768]uint16
	n, e := windows.GetFinalPathNameByHandle(h, &normalized[0], uint32(len(normalized)), 0)
	if e != nil || n == 0 || n >= uint32(len(normalized)) {
		return false
	}
	actual := strings.TrimPrefix(windows.UTF16ToString(normalized[:]), `\\?\`)
	return strings.EqualFold(actual, expected)
}

// Existing ancestors are pinned and must already have their canonical spelling.
// Missing tails are allowed only for explicitly selected future evidence/output
// inventory paths. No directory, lock, ACL or data file is created here.
func pinCanonicalDirectory(path string, allowMissing bool) (func(), error) {
	if !safeAbsolute(path) {
		return nil, errComposition
	}
	ptr, e := windows.UTF16PtrFromString(path[:3])
	if e != nil {
		return nil, errComposition
	}
	var fs [32]uint16
	if windows.GetDriveType(ptr) != windows.DRIVE_FIXED || windows.GetVolumeInformation(ptr, nil, 0, nil, nil, nil, &fs[0], uint32(len(fs))) != nil || windows.UTF16ToString(fs[:]) != "NTFS" {
		return nil, errComposition
	}
	var pins []windows.Handle
	closePins := func() {
		for i := len(pins) - 1; i >= 0; i-- {
			_ = windows.CloseHandle(pins[i])
		}
	}
	cur := path[:3]
	parts := strings.Split(path[3:], `\`)
	if len(path) == 3 {
		parts = nil
	}
	for i := -1; i < len(parts); i++ {
		if i >= 0 {
			cur = filepath.Join(cur, parts[i])
		}
		p, _ := windows.UTF16PtrFromString(cur)
		h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if e != nil {
			if allowMissing && (e == windows.ERROR_FILE_NOT_FOUND || e == windows.ERROR_PATH_NOT_FOUND) {
				return closePins, nil
			}
			closePins()
			return nil, errComposition
		}
		pins = append(pins, h)
		var info windows.ByHandleFileInformation
		if windows.GetFileInformationByHandle(h, &info) != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || !canonicalHandle(h, cur) {
			closePins()
			return nil, errComposition
		}
	}
	return closePins, nil
}

func pinRuntimeConfig(c runtimeConfig, nativeRoot string) (func(), error) {
	paths := []string{nativeRoot, c.EvidenceDirectory}
	if c.Board != nil {
		paths = append(paths, c.Board.Directory)
	}
	paths = append(paths, c.ProtectedRoots...)
	var closePaths []func()
	closeAll := func() {
		for i := len(closePaths) - 1; i >= 0; i-- {
			closePaths[i]()
		}
	}
	for _, path := range paths {
		close, e := pinCanonicalDirectory(path, true)
		if e != nil {
			closeAll()
			return nil, e
		}
		closePaths = append(closePaths, close)
	}
	return closeAll, nil
}
