//go:build windows && amd64

// Controlled owned helper only. This is not installed-tool invocation evidence.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func main() {
	mode := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if strings.HasPrefix(mode, "deny-") {
		started, rejected := 0, 0
		for i := 0; i < 24; i++ {
			path := filepath.Join(filepath.Dir(os.Args[0]), "child.exe")
			if mode == "deny-override" {
				if override(path) {
					started++
				} else {
					rejected++
				}
				continue
			}
			flags := uint32(0)
			if mode == "deny-nowindow" {
				flags = windows.CREATE_NO_WINDOW
			}
			if mode == "deny-newconsole" {
				flags = windows.CREATE_NEW_CONSOLE
			}
			c := exec.Command(path)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags, HideWindow: true}
			if c.Start() != nil {
				rejected++
			} else {
				started++
				_ = c.Process.Release()
			}
		}
		fmt.Printf("attempted=24 started=%d rejected=%d\n", started, rejected)
		if mode == "deny-console" {
			k := windows.NewLazySystemDLL("kernel32.dll")
			for i := 0; i < 20; i++ {
				_, _, _ = k.NewProc("FreeConsole").Call()
				ok, _, _ := k.NewProc("AllocConsole").Call()
				fmt.Printf("alloc=%d\n", ok)
			}
		}
		fmt.Println("complete")
		time.Sleep(time.Minute)
		return
	}
	if mode == "child" {
		fmt.Printf("child-ready=%d\n", os.Getpid())
		time.Sleep(time.Minute)
		return
	}
	if mode == "normal" {
		fmt.Println("codex-cli 1.2.3")
		return
	}
	if mode == "stdin" {
		b, e := io.ReadAll(os.Stdin)
		fmt.Printf("stdin=%d/%v secret=%s path=%s\n", len(b), e, os.Getenv("CUSTODY_SECRET"), os.Getenv("PATH"))
		return
	}
	if strings.HasPrefix(mode, "inherit-") {
		n, _ := strconv.ParseUint(strings.TrimPrefix(mode, "inherit-"), 10, 64)
		fmt.Printf("set-event=%v\n", windows.SetEvent(windows.Handle(n)))
		return
	}
	if mode == "flood" {
		var wg sync.WaitGroup
		for _, w := range []*os.File{os.Stdout, os.Stderr} {
			wg.Go(func() {
				b := make([]byte, 4096)
				for i := range b {
					b[i] = 'x'
				}
				for i := 0; i < 64; i++ {
					_, _ = w.Write(b)
				}
			})
		}
		wg.Wait()
		time.Sleep(time.Minute)
		return
	}
	fmt.Println("ready")
	time.Sleep(time.Minute)
}

func override(executable string) bool {
	a, e := windows.NewProcThreadAttributeList(2)
	if e != nil {
		fmt.Println("override-setup-failed")
		return false
	}
	defer a.Delete()
	policy := uint32(2)
	if e = a.Update(0x0002000e, unsafe.Pointer(&policy), unsafe.Sizeof(policy)); e != nil {
		fmt.Println("override-setup-failed")
		return false
	}
	hs := []windows.Handle{windows.Handle(os.Stdin.Fd()), windows.Handle(os.Stdout.Fd()), windows.Handle(os.Stderr.Fd())}
	if e = a.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&hs[0]), uintptr(len(hs))*unsafe.Sizeof(hs[0])); e != nil {
		fmt.Println("override-setup-failed")
		return false
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: a.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = hs[0]
	si.StdOutput = hs[1]
	si.StdErr = hs[2]
	app, _ := windows.UTF16PtrFromString(executable)
	cmd, _ := windows.UTF16PtrFromString(windows.EscapeArg(executable))
	var pi windows.ProcessInformation
	e = windows.CreateProcess(app, cmd, nil, nil, true, windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_NO_WINDOW, nil, nil, &si.StartupInfo, &pi)
	if e != nil {
		fmt.Println("override-create-refused")
		return false
	}
	_ = windows.TerminateProcess(pi.Process, 1)
	_, _ = windows.WaitForSingleObject(pi.Process, 5000)
	_ = windows.CloseHandle(pi.Thread)
	_ = windows.CloseHandle(pi.Process)
	return true
}
