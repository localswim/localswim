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
	inPublicationJob        = kernel32.NewProc("IsProcessInJob")
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

type publicationThreadEntry struct {
	Size, Usage, ID, Owner      uint32
	BasePriority, DeltaPriority int32
	Flags                       uint32
}

type publicationProcessTree struct {
	job       syscall.Handle
	sealed    bool
	processes []syscall.Handle
}

const publicationJobKillOnClose = 0x00002000

func newPublicationProcessTree(command *exec.Cmd) (*publicationProcessTree, error) {
	// Assign the suspended child before it can launch a transport. Descendants
	// inherit the job, and closing it also kills them if setup or waiting fails.
	const createNoWindow, createSuspended = 0x08000000, 0x00000004
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createSuspended}
	job, _, err := createPublicationJob.Call(0, 0)
	if job == 0 {
		return nil, err
	}
	tree := &publicationProcessTree{job: syscall.Handle(job)}
	limits := publicationJobLimits{Flags: publicationJobKillOnClose}
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

func (tree *publicationProcessTree) stop(deadline time.Time) error {
	// Set the job's active-process limit to zero before sampling membership.
	// Existing members can exit, but attempted new members are refused before
	// running, including a child launched concurrently with this cleanup.
	if !tree.sealed {
		const activeProcessLimit = 0x00000008
		limits := publicationJobLimits{Flags: publicationJobKillOnClose | activeProcessLimit}
		result, _, err := setPublicationJob.Call(uintptr(tree.job), 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
		if result == 0 {
			return err
		}
		tree.sealed = true
	}
	// Job termination can remove a member before Windows closes that
	// process's handles. Capture waitable handles before terminating the job.
	if tree.processes == nil {
		processes, err := tree.processHandles(deadline)
		if err != nil {
			for _, process := range processes {
				_ = syscall.CloseHandle(process)
			}
			// Leave the sealed job inspectable before termination erases PIDs.
			return err
		}
		tree.processes = processes
	}
	result, _, err := terminatePublicationJob.Call(uintptr(tree.job), 1)
	if result == 0 {
		return err
	}
	for len(tree.processes) > 0 {
		process := tree.processes[0]
		remaining := max(0, time.Until(deadline)).Milliseconds()
		status, err := syscall.WaitForSingleObject(process, uint32(remaining))
		if err != nil {
			return err
		}
		if status != syscall.WAIT_OBJECT_0 {
			return errPublicationCleanupDeadline
		}
		_ = syscall.CloseHandle(process)
		tree.processes = tree.processes[1:]
	}
	return nil
}

func (tree *publicationProcessTree) processHandles(deadline time.Time) ([]syscall.Handle, error) {
	ids, err := tree.processIDs(deadline)
	if err != nil {
		return nil, err
	}
	const processQueryLimitedInformation, processGone = 0x1000, syscall.Errno(87)
	processes := make([]syscall.Handle, 0, len(ids))
	for _, id := range ids {
		if !time.Now().Before(deadline) {
			return processes, errors.New("publication process inspection timed out")
		}
		process, err := syscall.OpenProcess(syscall.SYNCHRONIZE|processQueryLimitedInformation, false, uint32(id))
		if errors.Is(err, processGone) {
			continue
		}
		if err != nil {
			return processes, err
		}
		// A listed process could exit and its PID be reused before OpenProcess.
		var member int32
		result, _, err := inPublicationJob.Call(uintptr(process), uintptr(tree.job), uintptr(unsafe.Pointer(&member)))
		if result != 0 && member != 0 {
			processes = append(processes, process)
			continue
		}
		_ = syscall.CloseHandle(process)
		if result == 0 {
			return processes, err
		}
	}
	return processes, nil
}

func (tree *publicationProcessTree) processIDs(deadline time.Time) ([]uintptr, error) {
	// JOBOBJECT_BASIC_PROCESS_ID_LIST has two DWORD counts followed by
	// a variable-length array of pointer-sized process IDs.
	const basicProcessIDList = 3
	const slotSize = unsafe.Sizeof(uintptr(0))
	const headerSlots = int(unsafe.Sizeof([2]uint32{}) / slotSize)
	for capacity := 16; time.Now().Before(deadline); {
		buffer := make([]uintptr, headerSlots+capacity)
		result, _, err := queryPublicationJob.Call(uintptr(tree.job), basicProcessIDList, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer))*slotSize, 0)
		if result == 0 && !errors.Is(err, syscall.ERROR_MORE_DATA) {
			return nil, err
		}
		counts := (*[2]uint32)(unsafe.Pointer(&buffer[0]))
		assigned, listed := int(counts[0]), int(counts[1])
		if result != 0 && listed == assigned && listed <= capacity {
			return buffer[headerSlots : headerSlots+listed], nil
		}
		if assigned <= capacity {
			return nil, errors.New("publication process list could not be read")
		}
		capacity = assigned
	}
	return nil, errors.New("publication process inspection timed out")
}

func (tree *publicationProcessTree) close() {
	for _, process := range tree.processes {
		_ = syscall.CloseHandle(process)
	}
	_ = syscall.CloseHandle(tree.job)
}
