package v2

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

var (
	createPublicationJob    = kernel32.NewProc("CreateJobObjectW")
	setPublicationJob       = kernel32.NewProc("SetInformationJobObject")
	assignPublicationJob    = kernel32.NewProc("AssignProcessToJobObject")
	terminatePublicationJob = kernel32.NewProc("TerminateJobObject")
	queryPublicationJob     = kernel32.NewProc("QueryInformationJobObject")
	publicationThreadFirst  = kernel32.NewProc("Thread32First")
	publicationThreadNext   = kernel32.NewProc("Thread32Next")
	openPublicationThread   = kernel32.NewProc("OpenThread")
	resumePublicationThread = kernel32.NewProc("ResumeThread")
)

type publicationJobLimits struct {
	ProcessTime, JobTime                                       int64
	Flags                                                      uint32
	Minimum, Maximum                                           uintptr
	ActiveLimit                                                uint32
	Affinity                                                   uintptr
	Priority, Scheduling                                       uint32
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

type publicationJobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime int64
	PageFaults, Processes, Active, Terminated              uint32
}

type publicationThreadEntry struct {
	Size, Usage, ID, Owner      uint32
	BasePriority, DeltaPriority int32
	Flags                       uint32
}

type publicationProcessTree struct {
	job syscall.Handle
}

func newPublicationProcessTree(command *exec.Cmd) (*publicationProcessTree, error) {
	// Assign the suspended child before it can launch a transport. Descendants
	// inherit the job, and closing it also kills them if setup or waiting fails.
	const createNoWindow, createSuspended, killOnClose = 0x08000000, 0x00000004, 0x00002000
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createSuspended}
	job, _, err := createPublicationJob.Call(0, 0)
	if job == 0 {
		return nil, err
	}
	tree := &publicationProcessTree{job: syscall.Handle(job)}
	limits := publicationJobLimits{Flags: killOnClose}
	result, _, err := setPublicationJob.Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if result == 0 {
		tree.close()
		return nil, err
	}
	return tree, nil
}

func (tree *publicationProcessTree) attach(command *exec.Cmd) error {
	const processSetQuota, processTerminate = 0x0100, 0x0001
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(command.Process.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(process)
	result, _, err := assignPublicationJob.Call(uintptr(tree.job), uintptr(process))
	if result == 0 {
		return err
	}
	return resumePublicationProcess(uint32(command.Process.Pid))
}

func resumePublicationProcess(pid uint32) error {
	const snapshotThreads, threadSuspendResume = 0x00000004, 0x0002
	snapshot, err := syscall.CreateToolhelp32Snapshot(snapshotThreads, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)
	entry := publicationThreadEntry{}
	entry.Size = uint32(unsafe.Sizeof(entry))
	result, _, err := publicationThreadFirst.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	for result != 0 {
		if entry.Owner == pid {
			thread, _, err := openPublicationThread.Call(threadSuspendResume, 0, uintptr(entry.ID))
			if thread == 0 {
				return err
			}
			defer syscall.CloseHandle(syscall.Handle(thread))
			resumed, _, err := resumePublicationThread.Call(thread)
			if resumed == 0xffffffff {
				return err
			}
			return nil
		}
		result, _, err = publicationThreadNext.Call(uintptr(snapshot), uintptr(unsafe.Pointer(&entry)))
	}
	return err
}

func (tree *publicationProcessTree) stop() error {
	result, _, err := terminatePublicationJob.Call(uintptr(tree.job), 1)
	if result == 0 {
		return err
	}
	deadline := time.Now().Add(publicationGitCleanupTimeout)
	for {
		var accounting publicationJobAccounting
		result, _, err := queryPublicationJob.Call(uintptr(tree.job), 1, uintptr(unsafe.Pointer(&accounting)), unsafe.Sizeof(accounting), 0)
		if result == 0 {
			return err
		}
		if accounting.Active == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return errors.New("publication process cleanup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (tree *publicationProcessTree) close() {
	_ = syscall.CloseHandle(tree.job)
}
