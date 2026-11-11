package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func configureReplacementProcess(command *exec.Cmd) {
	const createNoWindow = 0x08000000
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

func pinRunImage(path string) (*os.File, error) {
	name := filepath.FromSlash(path)
	const slash = string(os.PathSeparator)
	const uncPrefix = slash + slash
	const extendedPrefix = uncPrefix + "?" + slash
	const devicePrefix = uncPrefix + "." + slash
	if !strings.HasPrefix(name, extendedPrefix) && !strings.HasPrefix(name, devicePrefix) {
		if unc, ok := strings.CutPrefix(name, uncPrefix); ok {
			name = extendedPrefix + "UNC" + slash + unc
		} else {
			name = extendedPrefix + name
		}
	}
	native, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(native, syscall.GENERIC_READ, syscall.FILE_SHARE_READ, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		_ = syscall.CloseHandle(handle)
		return nil, err
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = syscall.CloseHandle(handle)
		return nil, errRunImageChanged
	}
	return os.NewFile(uintptr(handle), path), nil
}
