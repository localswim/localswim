package v2

import (
	"context"
	"syscall"
	"unsafe"
)

func publicationChildExited(_ context.Context, pid int) (bool, error) {
	// waitid's fixed-size siginfo_t is zero when WNOHANG finds no exit.
	// WNOWAIT leaves the child waitable and its numeric PID reserved.
	const childPID = 1
	var info [32]uint32
	_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, childPID, uintptr(pid), uintptr(unsafe.Pointer(&info[0])),
		syscall.WEXITED|syscall.WNOHANG|syscall.WNOWAIT, 0, 0)
	if errno == syscall.EINTR {
		return false, nil
	}
	if errno != 0 {
		return false, errno
	}
	return info[0] != 0, nil
}
