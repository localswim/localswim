//go:build windows && amd64

package nativecustody

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	captureLimit     = 65536
	processLimit     = 16
	commandLimit     = 10 * time.Second
	foregroundLimit  = 2 * time.Second
	attributeJobList = 0x0002000d
)

var (
	errBusy               = errors.New("custody busy")
	errClosed             = errors.New("custody closed")
	errProfileUnsupported = errors.New("strict native profile unavailable")
	isInJob               = windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	getMitigation         = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessMitigationPolicy")
)

// spec is package-private. Production uses only the fixed identity bridge.
// The installation adapter supplies fresh held-file and current authority checks.
// Hooks are private controlled-test seams, with no command/environment selector.
type spec struct {
	executable, root, systemRoot string
	check                        func() error
	// hooks exist only at ownership boundaries, never substitute a native pass.
	beforeResume func()
	// Private negative-control boundary after real mitigation verification.
	beforeResumeBoundary func()
	// closeBefore may refuse BEFORE CloseHandle, proving the same object remains
	// owned. Actual CloseHandle errors are ambiguous and never retried.
	closeBefore func(string) bool
	closeCall   func(string, windows.Handle) error
	deadline    time.Time
}

type result struct {
	Code           string
	Joined         bool
	ExitCode       uint32
	Stdout, Stderr []byte
	ObservedBytes  uint64
}

type owner struct {
	mu     sync.Mutex
	closed bool
	active *operation
}

type ownedHandle struct {
	h         windows.Handle
	name      string
	ambiguous bool
}

type operation struct {
	owner                                                   *owner
	spec                                                    spec
	ctx                                                     context.Context
	cancel                                                  context.CancelFunc
	done                                                    chan struct{}
	mu                                                      sync.Mutex
	result                                                  result
	job, process, thread                                    ownedHandle
	stdin, stdoutWrite, stderrWrite, stdoutRead, stderrRead ownedHandle
	pid                                                     uint32
	assigned                                                bool
	resumeAttempted                                         bool
	created                                                 chan struct{}
	stopTermination, terminationDone                        chan struct{}
}

func (o *owner) start(ctx context.Context, s spec) (*operation, error) {
	if ctx == nil || s.check == nil || !filepath.IsAbs(s.executable) || !filepath.IsAbs(s.root) || !filepath.IsAbs(s.systemRoot) {
		return nil, errors.New("invalid custody specification")
	}
	now := time.Now()
	deadline := now.Add(commandLimit)
	if !s.deadline.IsZero() && s.deadline.Before(deadline) {
		deadline = s.deadline
	}
	s.deadline = deadline
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil, errClosed
	}
	if o.active != nil {
		return nil, errBusy
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	p := &operation{owner: o, spec: s, ctx: ctx, cancel: cancel, done: make(chan struct{}), created: make(chan struct{})}
	o.active = p
	go p.run()
	return p, nil
}

func (p *operation) snapshot(joined bool) result {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.result
	r.Stdout = append([]byte(nil), r.Stdout...)
	r.Stderr = append([]byte(nil), r.Stderr...)
	r.Joined = joined
	if !joined {
		r.Code = "DRAIN_INCOMPLETE"
	}
	return r
}

// wait bounds foreground reporting, not the lifetime of owned native work.
func (p *operation) wait(ctx context.Context) result {
	select {
	case <-p.done:
		return p.snapshot(true)
	default:
	}
	t := time.NewTimer(time.Until(p.spec.deadline.Add(foregroundLimit)))
	defer t.Stop()
	select {
	case <-p.done:
		return p.snapshot(true)
	case <-ctx.Done():
		return p.snapshot(false)
	case <-t.C:
		return p.snapshot(false)
	}
}

func (o *owner) stop(p *operation) error {
	o.mu.Lock()
	if o.active != p {
		o.mu.Unlock()
		if p != nil && p.owner == o {
			select {
			case <-p.done:
				return nil
			default:
			}
		}
		return errors.New("foreign custody operation")
	}
	o.mu.Unlock()
	p.cancel()
	return nil
}

func (o *owner) close() bool {
	o.mu.Lock()
	o.closed = true
	p := o.active
	o.mu.Unlock()
	if p == nil {
		return true
	}
	p.cancel()
	t := time.NewTimer(foregroundLimit)
	defer t.Stop()
	select {
	case <-p.done:
		return true
	case <-t.C:
		return false
	}
}

func (p *operation) code(code string) {
	p.mu.Lock()
	if p.result.Code == "" {
		p.result.Code = code
	}
	p.mu.Unlock()
}

func (p *operation) admitted() error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	if err := p.spec.check(); err != nil {
		return err
	}
	return p.ctx.Err()
}

func (p *operation) closeHandle(h *ownedHandle) bool {
	if h.h == 0 {
		return true
	}
	if h.ambiguous {
		return false
	}
	if p.spec.closeBefore != nil && !p.spec.closeBefore(h.name) {
		return false
	}
	closeCall := p.spec.closeCall
	if closeCall == nil {
		closeCall = func(_ string, h windows.Handle) error { return windows.CloseHandle(h) }
	}
	if closeCall(h.name, h.h) != nil {
		h.ambiguous = true
		return false
	}
	h.h = 0
	return true
}

func (p *operation) closeUntil(h *ownedHandle) {
	for !p.closeHandle(h) {
		time.Sleep(10 * time.Millisecond)
	}
}

func (p *operation) pipe(read, write *ownedHandle, name string) error {
	read.name, write.name = name+"-read", name+"-write"
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	if err := windows.CreatePipe(&read.h, &write.h, &sa, 0); err != nil {
		return err
	}
	return windows.SetHandleInformation(read.h, windows.HANDLE_FLAG_INHERIT, 0)
}

func environment(root, system string) ([]uint16, error) {
	vars := map[string]string{"SystemRoot": system, "WINDIR": system, "PATH": filepath.Join(system, "System32")}
	for key, dir := range map[string]string{"USERPROFILE": "home", "LOCALAPPDATA": "local", "APPDATA": "roaming", "TEMP": "temp", "TMP": "temp", "CODEX_HOME": "codex"} {
		path := filepath.Join(root, dir)
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
		vars[key] = path
	}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var block []uint16
	for _, key := range keys {
		v, err := windows.UTF16FromString(key + "=" + vars[key])
		if err != nil {
			return nil, err
		}
		block = append(block, v...)
	}
	return append(block, 0), nil
}

func (p *operation) prepare() error {
	if err := p.admitted(); err != nil {
		return err
	}
	p.job.name = "job"
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	p.job.h = job
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
	limits.BasicLimitInformation.ActiveProcessLimit = processLimit
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	if err = p.pipe(&p.stdoutRead, &p.stdoutWrite, "stdout"); err != nil {
		return err
	}
	if err = p.pipe(&p.stderrRead, &p.stderrWrite, "stderr"); err != nil {
		return err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	p.stdin.name = "stdin"
	nul, _ := windows.UTF16PtrFromString("NUL")
	p.stdin.h, err = windows.CreateFile(nul, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &sa, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	env, err := environment(p.spec.root, p.spec.systemRoot)
	if err != nil {
		return err
	}
	attrs, err := windows.NewProcThreadAttributeList(3)
	if err != nil {
		return err
	}
	defer attrs.Delete()
	childPolicy := uint32(1) // PROCESS_CREATION_CHILD_PROCESS_RESTRICTED; no weaker profile
	if err = attrs.Update(0x0002000e, unsafe.Pointer(&childPolicy), unsafe.Sizeof(childPolicy)); err != nil {
		return classifyProfileError(err)
	}
	handles := []windows.Handle{p.stdin.h, p.stdoutWrite.h, p.stderrWrite.h}
	if err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&handles[0]), uintptr(len(handles))*unsafe.Sizeof(handles[0])); err != nil {
		return err
	}
	jobs := []windows.Handle{job}
	if err = attrs.Update(attributeJobList, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(jobs[0])); err != nil {
		return classifyProfileError(err)
	}
	app, err := windows.UTF16PtrFromString(p.spec.executable)
	if err != nil {
		return err
	}
	cmd, err := windows.UTF16PtrFromString(windows.EscapeArg(p.spec.executable) + " --version")
	if err != nil {
		return err
	}
	cwd, err := windows.UTF16PtrFromString(p.spec.root)
	if err != nil {
		return err
	}
	si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = p.stdin.h, p.stdoutWrite.h, p.stderrWrite.h
	if err = p.admitted(); err != nil {
		return err
	}
	var pi windows.ProcessInformation
	err = windows.CreateProcess(app, cmd, nil, nil, true, windows.CREATE_SUSPENDED|windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT|windows.DETACHED_PROCESS, &env[0], cwd, &si.StartupInfo, &pi)
	if err != nil {
		return classifyProfileError(err)
	}
	p.process = ownedHandle{h: pi.Process, name: "process"}
	p.thread = ownedHandle{h: pi.Thread, name: "thread"}
	p.pid = pi.ProcessId
	p.stopTermination, p.terminationDone = make(chan struct{}), make(chan struct{})
	go p.watchTermination()
	close(p.created)
	var member int32
	if err = isInJob.Find(); err != nil {
		return errors.Join(errProfileUnsupported, err)
	}
	ok, _, callErr := isInJob.Call(uintptr(pi.Process), uintptr(job), uintptr(unsafe.Pointer(&member)))
	if ok == 0 {
		return callErr
	}
	if member != 1 {
		return errProfileUnsupported
	}
	p.assigned = true
	if err = verifyChildPolicy(p.process.h); err != nil {
		return err
	}
	// Only the child owns these ends now; failed close is joined before resume.
	for _, h := range []*ownedHandle{&p.stdin, &p.stdoutWrite, &p.stderrWrite} {
		if !p.closeHandle(h) {
			return errors.New("child handle close incomplete")
		}
	}
	return nil
}

func validChildPolicy(flags uint32) bool { return flags == 1 }

func verifyChildPolicy(process windows.Handle) error {
	if err := getMitigation.Find(); err != nil {
		return errors.Join(errProfileUnsupported, err)
	}
	var flags uint32
	ok, _, err := getMitigation.Call(uintptr(process), 13, uintptr(unsafe.Pointer(&flags)), unsafe.Sizeof(flags))
	if ok == 0 {
		return classifyProfileError(err)
	}
	if !validChildPolicy(flags) {
		return errProfileUnsupported
	}
	return nil
}

type accounting struct {
	User, Kernel, PeriodUser, PeriodKernel int64
	Faults, Total, Active, Terminated      uint32
}

func (p *operation) activeProcesses() (uint32, error) {
	var a accounting
	err := windows.QueryInformationJobObject(p.job.h, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil)
	if err == nil && a.Total > 1 {
		if errors.Is(p.ctx.Err(), context.DeadlineExceeded) {
			p.code("TIMED_OUT")
		} else if p.ctx.Err() != nil {
			p.code("CANCELLED")
		} else {
			p.code("TOOL_UNSUPPORTED")
		}
		p.cancel()
	}
	return a.Active, err
}

// All three handles stay owned until this worker joins. Cancellation can thus
// terminate even a suspended root while another preparation phase is blocked.
func (p *operation) watchTermination() {
	defer close(p.terminationDone)
	select {
	case <-p.stopTermination:
		return
	case <-p.ctx.Done():
	}
	for {
		_ = windows.TerminateJobObject(p.job.h, 1)
		_ = windows.TerminateProcess(p.process.h, 1) // held-root fallback if membership is unproven
		state, e := windows.WaitForSingleObject(p.process.h, 0)
		count, qe := p.activeProcesses()
		if e == nil && qe == nil && state == windows.WAIT_OBJECT_0 && count == 0 {
			return
		}
		select {
		case <-p.stopTermination:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (p *operation) drain(h *ownedHandle, stderr bool, done chan<- struct{}) {
	defer close(done)
	var buf [4096]byte
	for {
		var n uint32
		err := windows.ReadFile(h.h, buf[:], &n, nil)
		if n > 0 {
			p.mu.Lock()
			p.result.ObservedBytes += uint64(n)
			used := len(p.result.Stdout) + len(p.result.Stderr)
			keep := min(int(n), captureLimit-used)
			if stderr {
				p.result.Stderr = append(p.result.Stderr, buf[:keep]...)
			} else {
				p.result.Stdout = append(p.result.Stdout, buf[:keep]...)
			}
			over := p.result.ObservedBytes > captureLimit
			if over && p.result.Code == "" {
				p.result.Code = "OUTPUT_LIMIT"
			}
			p.mu.Unlock()
			if over {
				p.cancel()
			}
		}
		if err != nil {
			if err != windows.ERROR_BROKEN_PIPE && err != windows.ERROR_HANDLE_EOF {
				p.code("PIPE_ERROR")
				p.cancel()
			}
			return
		}
		if n == 0 {
			return
		}
	}
}

func (p *operation) run() {
	err := p.prepare()
	if err != nil {
		if errors.Is(p.ctx.Err(), context.DeadlineExceeded) {
			p.code("TIMED_OUT")
		} else if p.ctx.Err() != nil {
			p.code("CANCELLED")
		} else if errors.Is(err, errProfileUnsupported) {
			p.code("TOOL_UNSUPPORTED")
		} else {
			p.code("CREATE_FAILED")
		}
		p.cancel()
	}
	var outDone, errDone chan struct{}
	if err == nil {
		outDone, errDone = make(chan struct{}), make(chan struct{})
		go p.drain(&p.stdoutRead, false, outDone)
		go p.drain(&p.stderrRead, true, errDone)
		if p.spec.beforeResume != nil {
			p.spec.beforeResume()
		}
		err = p.admitted()
		if err == nil {
			err = verifyChildPolicy(p.process.h)
		}
		if err == nil && p.spec.beforeResumeBoundary != nil {
			p.spec.beforeResumeBoundary()
		}
		// The mitigation query can be uninterruptible. A cancellation/deadline
		// observed while it ran must never become a late ResumeThread call.
		if err == nil {
			err = p.ctx.Err()
		}
		if err == nil {
			p.resumeAttempted = true
			_, err = windows.ResumeThread(p.thread.h)
		}
		if err != nil {
			if errors.Is(p.ctx.Err(), context.DeadlineExceeded) {
				p.code("TIMED_OUT")
			} else if p.ctx.Err() == nil && errors.Is(err, errProfileUnsupported) {
				p.code("TOOL_UNSUPPORTED")
			} else {
				p.code("CANCELLED")
			}
			p.cancel()
		}
	}
	if p.process.h != 0 {
		for {
			if err != nil || p.ctx.Err() != nil {
				if errors.Is(p.ctx.Err(), context.DeadlineExceeded) {
					p.code("TIMED_OUT")
				} else {
					p.code("CANCELLED")
				}
			}
			state, e := windows.WaitForSingleObject(p.process.h, 0)
			count, qe := p.activeProcesses()
			if e == nil && qe == nil && state == windows.WAIT_OBJECT_0 && count == 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		var exit uint32
		if windows.GetExitCodeProcess(p.process.h, &exit) != nil {
			p.code("EXIT_UNKNOWN")
		}
		p.mu.Lock()
		p.result.ExitCode = exit
		p.mu.Unlock()
		if exit != 0 {
			p.code("EXIT_FAILED")
		}
		close(p.stopTermination)
		<-p.terminationDone
	}
	if outDone != nil {
		<-outDone
		<-errDone
	}
	// Creation failures may leave parent child-ends open; close before readers.
	for _, h := range []*ownedHandle{&p.stdin, &p.stdoutWrite, &p.stderrWrite, &p.stdoutRead, &p.stderrRead, &p.thread, &p.process, &p.job} {
		p.closeUntil(h)
	}
	p.code("OK")
	p.owner.mu.Lock()
	p.owner.active = nil
	p.owner.mu.Unlock()
	p.cancel()
	close(p.done)
}

func classifyProfileError(err error) error {
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) || errors.Is(err, windows.ERROR_PROC_NOT_FOUND) {
		return errors.Join(errProfileUnsupported, err)
	}
	return err
}
