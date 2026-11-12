package v2

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	initPublicationAttributes   = kernel32.NewProc("InitializeProcThreadAttributeList")
	updatePublicationAttributes = kernel32.NewProc("UpdateProcThreadAttribute")
	deletePublicationAttributes = kernel32.NewProc("DeleteProcThreadAttributeList")
	comparePublicationNames     = kernel32.NewProc("CompareStringOrdinal")
)

const (
	publicationCreateNoWindow      = 0x08000000
	publicationCreateSuspended     = 0x00000004
	publicationExtendedStartupInfo = 0x00080000
	publicationCreationFlags       = publicationCreateNoWindow | publicationCreateSuspended |
		publicationExtendedStartupInfo | syscall.CREATE_UNICODE_ENVIRONMENT
)

type publicationStartupInfo struct {
	syscall.StartupInfo
	attributes unsafe.Pointer
}

func (tree *publicationProcessTree) start(command *exec.Cmd) error {
	if command.Err != nil {
		return command.Err
	}
	if command.Path == "" {
		return exec.ErrNotFound
	}
	executable, err := filepath.Abs(command.Path)
	if err != nil {
		return err
	}
	application, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return err
	}
	args := command.Args
	if len(args) == 0 {
		args = []string{command.Path}
	}
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = syscall.EscapeArg(arg)
	}
	line, err := syscall.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return err
	}
	var directory *uint16
	if command.Dir != "" {
		directory, err = syscall.UTF16PtrFromString(command.Dir)
		if err != nil {
			return err
		}
	}
	environment, err := publicationWindowsEnvironment(command)
	if err != nil {
		return err
	}
	handles, err := publicationWindowsStdio(command)
	if err != nil {
		return err
	}
	defer closePublicationHandles(handles[:])
	// UpdateProcThreadAttribute retains this array until the attribute list
	// is deleted, so its address must not move between the native calls.
	var pinned runtime.Pinner
	pinned.Pin(&handles)
	defer pinned.Unpin()
	attributes, err := publicationWindowsAttributes(&handles)
	if err != nil {
		return err
	}
	defer func() { deletePublicationAttributes.Call(uintptr(attributes)) }()
	startup := publicationStartupInfo{attributes: attributes}
	startup.Cb = uint32(unsafe.Sizeof(startup))
	startup.Flags = syscall.STARTF_USESTDHANDLES
	startup.StdInput, startup.StdOutput, startup.StdErr = handles[0], handles[1], handles[2]
	// Keep the process and primary thread returned together by CreateProcessW.
	// The exact suspended child can be assigned without any system-wide scan.
	return syscall.CreateProcess(application, line, nil, nil, true, publicationCreationFlags,
		&environment[0], directory, &startup.StartupInfo, &tree.root)
}

func publicationWindowsEnvironment(command *exec.Cmd) ([]uint16, error) {
	for _, entry := range command.Env {
		if strings.ContainsRune(entry, 0) {
			return nil, syscall.EINVAL
		}
	}
	type environmentEntry struct {
		text string
		name []uint16
	}
	environment := command.Environ()
	entries := make([]environmentEntry, 0, len(environment))
	for _, entry := range environment {
		name := entry
		// A drive entry such as =C:=C:\work uses the second equals sign
		// as the separator. Regular names have at least one character.
		if len(entry) > 1 {
			if separator := strings.IndexByte(entry[1:], '='); separator >= 0 {
				name = entry[:separator+1]
			}
		}
		entries = append(entries, environmentEntry{text: entry, name: utf16.Encode([]rune(name))})
	}
	var compareErr error
	slices.SortStableFunc(entries, func(a, b environmentEntry) int {
		// CompareStringOrdinal's ignore-case mode uses Windows Unicode
		// order independently of the user's locale. The middle result is 2.
		result, _, err := comparePublicationNames.Call(uintptr(unsafe.Pointer(unsafe.SliceData(a.name))), uintptr(len(a.name)),
			uintptr(unsafe.Pointer(unsafe.SliceData(b.name))), uintptr(len(b.name)), 1)
		if result == 0 {
			compareErr = err
			return 0
		}
		return int(result) - 2
	})
	if compareErr != nil {
		return nil, compareErr
	}
	var block []uint16
	for _, entry := range entries {
		value, err := syscall.UTF16FromString(entry.text)
		if err != nil {
			return nil, err
		}
		block = append(block, value...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func publicationWindowsStdio(command *exec.Cmd) ([3]syscall.Handle, error) {
	var handles [3]syscall.Handle
	var null *os.File
	current, err := syscall.GetCurrentProcess()
	if err != nil {
		return handles, err
	}
	for index, stream := range []any{command.Stdin, command.Stdout, command.Stderr} {
		file, ok := stream.(*os.File)
		if !ok && stream != nil {
			closePublicationHandles(handles[:index])
			return handles, syscall.EINVAL
		}
		if file == nil {
			if null == nil {
				null, err = os.OpenFile(os.DevNull, os.O_RDWR, 0)
				if err != nil {
					closePublicationHandles(handles[:index])
					return handles, err
				}
				defer null.Close()
			}
			file = null
		}
		err = syscall.DuplicateHandle(current, syscall.Handle(file.Fd()), current, &handles[index], 0, true, syscall.DUPLICATE_SAME_ACCESS)
		runtime.KeepAlive(file)
		if err != nil {
			closePublicationHandles(handles[:index])
			return handles, err
		}
	}
	return handles, nil
}

func closePublicationHandles(handles []syscall.Handle) {
	for _, handle := range handles {
		_ = syscall.CloseHandle(handle)
	}
}

func publicationWindowsAttributes(handles *[3]syscall.Handle) (unsafe.Pointer, error) {
	const handleList = 0x00020002
	var size uintptr
	_, _, err := initPublicationAttributes.Call(0, 1, 0, uintptr(unsafe.Pointer(&size)))
	if size == 0 {
		return nil, err
	}
	const slot = unsafe.Sizeof(uintptr(0))
	buffer := make([]uintptr, (size+slot-1)/slot)
	attributes := unsafe.Pointer(&buffer[0])
	result, _, err := initPublicationAttributes.Call(uintptr(attributes), 1, 0, uintptr(unsafe.Pointer(&size)))
	if result == 0 {
		return nil, err
	}
	result, _, err = updatePublicationAttributes.Call(uintptr(attributes), 0, handleList,
		uintptr(unsafe.Pointer(handles)), unsafe.Sizeof(*handles), 0, 0)
	if result == 0 {
		deletePublicationAttributes.Call(uintptr(attributes))
		return nil, err
	}
	return attributes, nil
}
