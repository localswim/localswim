//go:build windows && amd64 && nativecustodyqualification

package nativecustody

import (
	"bufio"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var fixtureBuild struct {
	sync.Mutex
	path string
}

func TestMain(m *testing.M) {
	if os.Getenv("NATIVECUSTODY_POISON") == "1" {
		poisonHelper()
		return
	}
	if os.Getenv("NATIVECUSTODY_PARENT") == "1" {
		parentHelper()
		return
	}
	code := m.Run()
	if fixtureBuild.path != "" && strings.HasPrefix(filepath.Base(fixtureBuild.path), "helper-") {
		if os.RemoveAll(fixtureBuild.path) != nil {
			code = 1
		}
	}
	os.Exit(code)
}

func helper(t *testing.T, mode string) (string, string) {
	t.Helper()
	fixtureBuild.Lock()
	defer fixtureBuild.Unlock()
	if fixtureBuild.path == "" {
		base := os.Getenv("NATIVECUSTODY_TEST_ROOT")
		if base == "" {
			t.Fatal("NATIVECUSTODY_TEST_ROOT requires private native root")
		}
		var err error
		fixtureBuild.path, err = os.MkdirTemp(base, "helper-")
		if err != nil {
			t.Fatal(err)
		}
		goexe := os.Getenv("NATIVECUSTODY_GO")
		if goexe == "" {
			t.Fatal("NATIVECUSTODY_GO requires exact qualified compiler")
		}
		cmd := exec.Command(goexe, "build", "-trimpath", "-buildvcs=false", "-o", filepath.Join(fixtureBuild.path, "helper.exe"), "./testdata/helper")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("helper build: %v: %s", err, b)
		}
	}
	root := t.TempDir()
	b, err := os.ReadFile(filepath.Join(fixtureBuild.path, "helper.exe"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("owned helper SHA256 %x", sha256.Sum256(b))
	for _, name := range []string{mode, "child"} {
		if err = os.WriteFile(filepath.Join(root, name+".exe"), b, 0700); err != nil {
			t.Fatal(err)
		}
	}
	cwd := filepath.Join(root, "run")
	if err = os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, mode+".exe"), cwd
}

func fixtureSpec(t *testing.T, mode string) spec {
	t.Helper()
	exe, root := helper(t, mode)
	system, err := windows.GetWindowsDirectory()
	if err != nil {
		t.Fatal(err)
	}
	return spec{executable: exe, root: root, systemRoot: system, check: func() error { return nil }}
}

func launch(t *testing.T, o *owner, s spec) *operation {
	t.Helper()
	p, err := o.start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		p.cancel()
		select {
		case <-p.done:
		case <-time.After(4 * time.Second):
			t.Error("owned cleanup incomplete")
		}
	})
	return p
}

func joined(t *testing.T, p *operation) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := p.wait(ctx)
	if !r.Joined {
		t.Fatalf("not joined: %s", r.Code)
	}
	for _, h := range []*ownedHandle{&p.job, &p.process, &p.thread, &p.stdin, &p.stdoutWrite, &p.stderrWrite, &p.stdoutRead, &p.stderrRead} {
		if h.h != 0 || h.ambiguous {
			t.Fatalf("retained %s", h.name)
		}
	}
	return r
}

func TestSuspendedAtomicJobAndNormalJoin(t *testing.T) {
	s := fixtureSpec(t, "normal")
	entered, release := make(chan struct{}), make(chan struct{})
	s.beforeResume = func() { close(entered); <-release }
	o := &owner{}
	p := launch(t, o, s)
	<-entered
	if err := verifyChildPolicy(p.process.h); err != nil {
		t.Fatal(err)
	}
	for _, flags := range []uint32{0, 2, 3, 4, 5, 7, 0xffffffff} {
		if validChildPolicy(flags) {
			t.Fatal("contradictory child policy accepted")
		}
	}
	a, e := p.activeProcesses()
	if e != nil || a != 1 {
		t.Fatalf("suspended job membership: %d %v", a, e)
	}
	if state, e := windows.WaitForSingleObject(p.process.h, 0); e != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("suspended root exited")
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if e = windows.QueryInformationJobObject(p.job.h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil); e != nil {
		t.Fatal(e)
	}
	want := uint32(windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS)
	if limits.BasicLimitInformation.LimitFlags != want || limits.BasicLimitInformation.ActiveProcessLimit != 16 {
		t.Fatal("wrong job limits/breakaway")
	}
	if _, e = o.start(context.Background(), s); !errors.Is(e, errBusy) {
		t.Fatal("parallel operation admitted")
	}
	close(release)
	r := joined(t, p)
	if r.Code != "OK" || string(r.Stdout) != "codex-cli 1.2.3\n" || len(r.Stderr) != 0 {
		t.Fatalf("normal capture %q %q %s", r.Stdout, r.Stderr, r.Code)
	}
}

func TestCreationFailureAndPreResumeRefusal(t *testing.T) {
	s := fixtureSpec(t, "normal")
	s.executable = filepath.Join(s.root, "missing.exe")
	r := joined(t, launch(t, &owner{}, s))
	if r.Code != "CREATE_FAILED" {
		t.Fatal(r.Code)
	}
	s = fixtureSpec(t, "normal")
	var calls atomic.Int32
	s.check = func() error {
		if calls.Add(1) == 3 {
			return errors.New("authority revoked")
		}
		return nil
	}
	r = joined(t, launch(t, &owner{}, s))
	if r.Code != "CANCELLED" || len(r.Stdout) != 0 {
		t.Fatalf("resumed revoked root: %+v", r)
	}
	s = fixtureSpec(t, "normal")
	s.deadline = time.Now().Add(-time.Second)
	p := launch(t, &owner{}, s)
	<-p.done
	r = joined(t, p)
	if r.Code != "TIMED_OUT" || p.pid != 0 {
		t.Fatal("expired attempt created root")
	}
}

func TestClosedStdinCleanEnvironmentAndHandleList(t *testing.T) {
	t.Setenv("CUSTODY_SECRET", "must-not-inherit")
	s := fixtureSpec(t, "stdin")
	r := joined(t, launch(t, &owner{}, s))
	if !strings.Contains(string(r.Stdout), "stdin=0/<nil> secret= path="+filepath.Join(s.systemRoot, "System32")) {
		t.Fatalf("ambient/stdin: %q", r.Stdout)
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	event, e := windows.CreateEvent(&sa, 1, 0, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(event)
	s = fixtureSpec(t, "inherit-"+strconv.FormatUint(uint64(event), 10))
	r = joined(t, launch(t, &owner{}, s))
	if state, e := windows.WaitForSingleObject(event, 0); e != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("unlisted inheritable handle leaked")
	}
	// Negative control proves this same owned inheritable event is observable.
	cmd := exec.Command(s.executable, "--version")
	cmd.SysProcAttr = &syscall.SysProcAttr{AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(event)}}
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("inheritance control %v %s", e, b)
	}
	if state, e := windows.WaitForSingleObject(event, 0); e != nil || state != windows.WAIT_OBJECT_0 {
		t.Fatal("inheritance negative control did not signal event")
	}
}

func TestOutputBoundAndDeadline(t *testing.T) {
	r := joined(t, launch(t, &owner{}, fixtureSpec(t, "flood")))
	if r.Code != "OUTPUT_LIMIT" || len(r.Stdout)+len(r.Stderr) > captureLimit || r.ObservedBytes <= captureLimit {
		t.Fatal("shared capture bound failed")
	}
	p := launch(t, &owner{}, fixtureSpec(t, "wait"))
	<-p.created
	r = joined(t, p)
	if r.Code != "TIMED_OUT" {
		t.Fatal(r.Code)
	}
}

func TestStrictChildAndConsoleRefusal(t *testing.T) {
	for _, mode := range []string{"deny-default", "deny-nowindow", "deny-newconsole", "deny-override", "deny-console"} {
		t.Run(mode, func(t *testing.T) {
			s := fixtureSpec(t, mode)
			o := &owner{}
			p := launch(t, o, s)
			<-p.created
			for deadline := time.Now().Add(8 * time.Second); ; {
				if strings.Contains(string(p.snapshot(false).Stdout), "complete") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("strict helper failed to complete")
				}
				time.Sleep(10 * time.Millisecond)
			}
			output := string(p.snapshot(false).Stdout)
			if !strings.Contains(output, "attempted=24 started=0 rejected=24") || strings.Contains(output, "body=") {
				t.Fatalf("child policy refusal %q", output)
			}
			if mode == "deny-console" && strings.Count(output, "alloc=0\n") != 20 {
				t.Fatalf("console churn was not refused %q", output)
			}
			if mode == "deny-override" && strings.Count(output, "override-create-refused\n") != 24 {
				t.Fatal("override request failed before actual creation boundary")
			}
			members := inspectJob(t, p)
			if len(members) != 1 || members[p.pid] != s.executable {
				t.Fatalf("strict profile has unexpected job members: %v", members)
			}
			assertNoProcessDescendants(t, p)
			if err := verifyChildPolicy(p.process.h); err != nil {
				t.Fatal(err)
			}
			if err := o.stop(p); err != nil {
				t.Fatal(err)
			}
			if r := joined(t, p); r.Code != "CANCELLED" {
				t.Fatal(r.Code)
			}
		})
	}
}

// Snapshot the live root's parent tree independently of job membership. A
// helper or console host outside the job cannot be hidden by a job-only count.
func assertNoProcessDescendants(t *testing.T, p *operation) {
	t.Helper()
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	parents := map[uint32]uint32{}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		parents[entry.ProcessID] = entry.ParentProcessID
	}
	if err != windows.ERROR_NO_MORE_FILES {
		t.Fatal(err)
	}
	desc := map[uint32]bool{p.pid: true}
	changed := true
	for changed {
		changed = false
		for pid, parent := range parents {
			if !desc[pid] && desc[parent] {
				desc[pid] = true
				changed = true
			}
		}
	}
	for pid := range desc {
		if pid != p.pid {
			t.Fatalf("strict helper has process descendant outside or inside job: %d", pid)
		}
	}
	if _, present := parents[p.pid]; !present {
		t.Fatal("live strict root absent from independent process snapshot")
	}
}
func inspectJob(t *testing.T, p *operation) map[uint32]string {
	t.Helper()
	var ids struct {
		Assigned, Listed uint32
		PIDs             [64]uintptr
	}
	if err := windows.QueryInformationJobObject(p.job.h, windows.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&ids)), uint32(unsafe.Sizeof(ids)), nil); err != nil {
		t.Fatal(err)
	}
	active, e := p.activeProcesses()
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("job accounting active=%d assigned=%d listed=%d", active, ids.Assigned, ids.Listed)
	result := map[uint32]string{}
	for _, pid := range ids.PIDs[:ids.Listed] {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			t.Fatal(err)
		}
		state, err := windows.WaitForSingleObject(h, 0)
		if err != nil {
			_ = windows.CloseHandle(h)
			t.Fatal(err)
		}
		if state == windows.WAIT_OBJECT_0 {
			var exit uint32
			_ = windows.GetExitCodeProcess(h, &exit)
			_ = windows.CloseHandle(h)
			t.Logf("terminated job member pid=%d exit=%d", pid, exit)
			continue
		}
		var image [4096]uint16
		size := uint32(len(image))
		err = windows.QueryFullProcessImageName(h, 0, &image[0], &size)
		var created, exited, kernel, user windows.Filetime
		te := windows.GetProcessTimes(h, &created, &exited, &kernel, &user)
		var member int32
		ok, _, _ := isInJob.Call(uintptr(h), uintptr(p.job.h), uintptr(unsafe.Pointer(&member)))
		ce := windows.CloseHandle(h)
		if err != nil || te != nil || ce != nil {
			t.Fatal("job member identity unavailable")
		}
		if ok == 0 || member != 1 || created.LowDateTime == 0 && created.HighDateTime == 0 {
			t.Fatal("held member identity or owned-job membership unavailable")
		}
		name := windows.UTF16ToString(image[:size])
		result[uint32(pid)] = name
		t.Logf("owned job pid=%d image=%s created=%v", pid, name, created)
	}
	return result
}

func TestCancellationCloseAndRetainedFailure(t *testing.T) {
	o := &owner{}
	p := launch(t, o, fixtureSpec(t, "wait"))
	<-p.created
	if e := o.stop(&operation{}); e == nil {
		t.Fatal("foreign stop accepted")
	}
	if e := o.stop(p); e != nil {
		t.Fatal(e)
	}
	r := joined(t, p)
	if r.Code != "CANCELLED" {
		t.Fatal(r.Code)
	}
	s := fixtureSpec(t, "normal")
	var release atomic.Bool
	s.closeBefore = func(name string) bool { return name != "job" || release.Load() }
	o = &owner{}
	p = launch(t, o, s)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	r = p.wait(ctx)
	if r.Joined || r.Code != "DRAIN_INCOMPLETE" {
		t.Fatal("failed close reported joined")
	}
	if _, e := o.start(context.Background(), s); !errors.Is(e, errBusy) {
		t.Fatal("incomplete operation released exclusion")
	}
	start := time.Now()
	if o.close() {
		t.Fatal("close reported false join")
	}
	if time.Since(start) > 2500*time.Millisecond {
		t.Fatal("foreground close exceeded bound")
	}
	if _, e := o.start(context.Background(), s); !errors.Is(e, errClosed) {
		t.Fatal("closed owner admitted successor")
	}
	release.Store(true)
	joined(t, p)
}

func TestNativeCloseErrorIsNotRetried(t *testing.T) {
	// A controlled invalid handle provokes the actual CloseHandle error path.
	// There is no live resource to abandon in this negative case.
	var attempts int
	p := &operation{spec: spec{closeBefore: func(string) bool { attempts++; return true }}}
	h := ownedHandle{h: windows.Handle(0x12345678), name: "negative-control"}
	if p.closeHandle(&h) || !h.ambiguous || attempts != 1 {
		t.Fatal("native close error not retained")
	}
	for range 3 {
		if p.closeHandle(&h) {
			t.Fatal("ambiguous close reported successful")
		}
	}
	if attempts != 1 || h.h != windows.Handle(0x12345678) {
		t.Fatal("ambiguous numeric handle retried")
	}
}

func TestAmbiguousCloseTerminatesSuspendedRoot(t *testing.T) {
	s := fixtureSpec(t, "normal")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), "NATIVECUSTODY_POISON=1", "NATIVECUSTODY_PARENT_EXE="+s.executable, "NATIVECUSTODY_PARENT_ROOT="+s.root)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "POISON_PROOF_PASS") {
		t.Fatalf("ambiguous-close isolated proof %v: %s", err, output)
	}
	t.Log(string(output))
}

func TestCallerRevocationCancelsWholeJob(t *testing.T) {
	s := fixtureSpec(t, "wait")
	ctx, revoke := context.WithCancel(context.Background())
	defer revoke()
	o := &owner{}
	p, err := o.start(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { revoke(); <-p.done })
	for deadline := time.Now().Add(8 * time.Second); ; {
		if strings.Contains(string(p.snapshot(false).Stdout), "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("post-resume family not established")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// This controlled caller signal models loss of authority/generation. The
	// future adapter supplying that signal remains M18 scope.
	revoke()
	if r := joined(t, p); r.Code != "CANCELLED" {
		t.Fatal(r.Code)
	}
}

func poisonHelper() {
	fail := func() { os.Exit(2) }
	system, err := windows.GetWindowsDirectory()
	if err != nil {
		fail()
	}
	var calls atomic.Int32
	closed := make(chan windows.Handle, 1)
	var original windows.Handle
	s := spec{executable: os.Getenv("NATIVECUSTODY_PARENT_EXE"), root: os.Getenv("NATIVECUSTODY_PARENT_ROOT"), systemRoot: system, check: func() error { return nil }}
	s.closeCall = func(name string, h windows.Handle) error {
		if name != "stdin" {
			return windows.CloseHandle(h)
		}
		calls.Add(1)
		original = h
		if windows.CloseHandle(h) != nil {
			fail()
		}
		event, e := windows.CreateEvent(nil, 1, 0, nil)
		if e != nil {
			fail()
		}
		closed <- event
		return errors.New("controlled close completed but result ambiguous")
	}
	o := &owner{}
	p, err := o.start(context.Background(), s)
	if err != nil {
		fail()
	}
	event := <-closed
	if state, e := windows.WaitForSingleObject(p.process.h, 5000); e != nil || state != windows.WAIT_OBJECT_0 {
		fail()
	}
	deadline := time.Now().Add(time.Second)
	for {
		n, e := p.activeProcesses()
		if e != nil {
			fail()
		}
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			fail()
		}
		time.Sleep(5 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if p.wait(ctx).Joined {
		fail()
	}
	if _, e := o.start(context.Background(), s); !errors.Is(e, errBusy) {
		fail()
	}
	if o.close() {
		fail()
	}
	if calls.Load() != 1 || windows.SetEvent(event) != nil {
		fail()
	}
	fmt.Printf("POISON_PROOF_PASS closed-root=true job-zero=true joined=false close-calls=1 recycled-slot=%t\n", event == original)
	_ = windows.CloseHandle(event)
	os.Exit(0) // Isolated owner exit releases its intentionally poisoned custody.
}

func parentHelper() {
	system, e := windows.GetWindowsDirectory()
	if e != nil {
		os.Exit(2)
	}
	o := &owner{}
	p, e := o.start(context.Background(), spec{executable: os.Getenv("NATIVECUSTODY_PARENT_EXE"), root: os.Getenv("NATIVECUSTODY_PARENT_ROOT"), systemRoot: system, check: func() error { return nil }})
	if e != nil {
		os.Exit(2)
	}
	<-p.created
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		output := string(p.result.Stdout)
		p.mu.Unlock()
		if strings.Contains(output, "ready") {
			fmt.Printf("CUSTODY_PIDS=%d\n", p.pid)
			select {}
		}
		time.Sleep(5 * time.Millisecond)
	}
	os.Exit(2)
}

func TestParentDeathKillsOwnedJob(t *testing.T) {
	s := fixtureSpec(t, "wait")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "NATIVECUSTODY_PARENT=1", "NATIVECUSTODY_PARENT_EXE="+s.executable, "NATIVECUSTODY_PARENT_ROOT="+s.root)
	pipe, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	defer pipe.Close()
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			if strings.HasPrefix(scanner.Text(), "CUSTODY_PIDS=") {
				lines <- scanner.Text()
				return
			}
		}
	}()
	var line string
	select {
	case line = <-lines:
	case <-time.After(10 * time.Second):
		t.Fatal("custodian child not reported")
	}
	var handles []windows.Handle
	for _, value := range strings.Split(strings.TrimPrefix(line, "CUSTODY_PIDS="), ",") {
		pid, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
		if err != nil {
			t.Fatal(err)
		}
		defer windows.CloseHandle(h)
		handles = append(handles, h)
	}
	if len(handles) != 1 {
		t.Fatal("strict root proof missing")
	}
	if e = cmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	for _, h := range handles {
		if state, e := windows.WaitForSingleObject(h, 5000); e != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatal("parent death left job root or descendant alive")
		}
	}
}
