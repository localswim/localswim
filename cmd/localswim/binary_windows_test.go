//go:build windows && amd64 && p01_native

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/localswim/localswim/internal/assets"
	S "github.com/localswim/localswim/internal/store/open"
	H "github.com/localswim/localswim/internal/transport/https"
	"github.com/localswim/localswim/internal/transport/setup"
	"golang.org/x/sys/windows"
)

func configFor(t *testing.T, f *nativeFixture) (runtimeConfig, string) {
	t.Helper()
	root := filepath.Dir(f.selected.Directory)
	c := runtimeConfig{ConfigVersion: 1, InstallationID: f.selected.InstallationID, Board: &runtimeBoard{BoardID: testBoard, Incarnation: testInc, Directory: f.selected.Directory}, EvidenceDirectory: filepath.Join(root, "evidence"), ProtectedRoots: []string{}}
	p := filepath.Join(root, "runtime.json")
	raw, e := json.Marshal(c)
	if e != nil || os.WriteFile(p, raw, 0600) != nil {
		t.Fatal("config fixture unavailable")
	}
	return c, p
}

func ownershipInventory(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		if e := filepath.WalkDir(root, func(path string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				out[path] = "directory"
				return nil
			}
			b, e := os.ReadFile(path)
			if e != nil {
				if entry.Name() == "install.lock" || entry.Name() == "board.lock" {
					info, statErr := entry.Info()
					if statErr != nil || info.Size() != 0 {
						return errComposition
					}
					out[path] = "held-empty-native-lock"
					return nil
				}
				return e
			}
			sum := sha256.Sum256(b)
			out[path] = hex.EncodeToString(sum[:])
			return nil
		}); e != nil {
			t.Fatal("owned inventory unavailable")
		}
	}
	return out
}

func TestNativeUnsafeEvidenceOwnershipRefusal(t *testing.T) {
	for _, kind := range []string{"native-root", "board-root", "native-descendant", "board-descendant", "native-ancestor", "board-ancestor", "board-junction", "board-short-alias"} {
		t.Run(kind, func(t *testing.T) {
			f := privateFixture(t, 47443)
			f.credential(t)
			selected := setup.CredentialSelection{InstallationID: f.selected.InstallationID, BoardID: testBoard, Incarnation: testInc, GrantID: f.grant}
			lease, e := f.manager.PrepareCredential(selected)
			if e != nil {
				t.Fatal("protected credential fixture")
			}
			if lease.Disable() != nil {
				lease.Close()
				t.Fatal("disable fixture")
			}
			if _, e = lease.Publish(f.secret); e != nil {
				lease.Close()
				t.Fatal("publish fixture")
			}
			lease.Close()
			root, e := f.manager.ProtectedRoot()
			if e != nil {
				t.Fatal("native inventory unavailable")
			}
			cfg, path := configFor(t, f)
			switch kind {
			case "native-root":
				cfg.EvidenceDirectory = root
			case "board-root":
				cfg.EvidenceDirectory = f.selected.Directory
			case "native-descendant":
				cfg.EvidenceDirectory = filepath.Join(root, "new-evidence")
			case "board-descendant":
				cfg.EvidenceDirectory = filepath.Join(f.selected.Directory, "new-evidence")
			case "native-ancestor", "board-ancestor":
				cfg.EvidenceDirectory = filepath.Dir(root)
			case "board-junction":
				cfg.EvidenceDirectory = f.selected.Directory
				cfg.Board.Directory = filepath.Join(filepath.Dir(root), "board-alias")
				createJunction(t, cfg.Board.Directory, f.selected.Directory)
			case "board-short-alias":
				cfg.EvidenceDirectory = f.selected.Directory
				var short [32768]uint16
				p, _ := windows.UTF16PtrFromString(f.selected.Directory)
				n, e := windows.GetShortPathName(p, &short[0], uint32(len(short)))
				if e == nil && n > 0 && n < uint32(len(short)) && !strings.EqualFold(windows.UTF16ToString(short[:]), f.selected.Directory) {
					cfg.Board.Directory = windows.UTF16ToString(short[:])
					t.Log("ACTUAL_SHORT_ALIAS_EXERCISED")
				} else {
					t.Log("SHORT_ALIAS_UNAVAILABLE unaliased overlap refusal only")
				}
			}
			if kind == "native-root" {
				cfg.Board = nil
			} // Offline-style config still protects actual N01 ROOT.
			raw, _ := json.Marshal(cfg)
			if os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("unsafe config fixture")
			}
			var requests atomic.Int32
			server, e := H.Listen(f.ctx, f.native, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); f.handler.ServeHTTP(w, r) }))
			if e != nil {
				t.Fatal("private listener unavailable")
			}
			defer server.Close()
			before := ownershipInventory(t, root, f.selected.Directory)
			wire, _ := json.Marshal(map[string]any{"incarnation": testInc, "opId": "50000000-0000-0000-0000-000000000002", "target": nil, "expectedRevision": 1, "expectedCardRevision": nil, "input": map[string]any{"title": "synthetic", "description": "private fixture"}})
			var out, diagnostic bytes.Buffer
			exit := run(f.ctx, []string{"--config", path, "--json", "--installation", selected.InstallationID, "--board", selected.BoardID, "--incarnation", selected.Incarnation, "--grant", selected.GrantID, "card", "create", "--input", "-"}, bytes.NewReader(wire), &out, &diagnostic)
			if exit != 1 || requests.Load() != 0 {
				t.Error("unsafe evidence config reached HTTP or effects")
			}
			if !reflect.DeepEqual(before, ownershipInventory(t, root, f.selected.Directory)) {
				t.Error("unsafe evidence config changed owned inventory")
			}
			if _, e := f.manager.ReadContext(); e != nil {
				t.Error("unsafe evidence config damaged native source")
			}
			if _, e := f.manager.AcquireServer(); e == nil {
				t.Error("unsafe config displaced native lifetime lock")
			}
			if _, e := S.Existing(f.ctx, f.selected.Directory, f.selected.Identity); e == nil {
				t.Error("unsafe config displaced source lock")
			}
			if bytes.Contains(out.Bytes(), []byte(root)) || bytes.Contains(diagnostic.Bytes(), []byte(root)) {
				t.Error("unsafe config disclosed native root")
			}
		})
	}
}

func createJunction(t *testing.T, alias, target string) {
	t.Helper()
	if os.Mkdir(alias, 0700) != nil {
		t.Fatal("junction fixture unavailable")
	}
	p, _ := windows.UTF16PtrFromString(alias)
	h, e := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal("junction fixture handle unavailable")
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
}

func TestNativeConfigObjectSafetyAndInstallationBinding(t *testing.T) {
	f := privateFixture(t, 47443)
	c, path := configFor(t, f)
	pointer, _ := windows.UTF16PtrFromString(f.selected.Directory)
	directoryHandle, e := windows.CreateFile(pointer, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal("canonical directory handle unavailable")
	}
	if !canonicalHandle(directoryHandle, f.selected.Directory) || canonicalHandle(directoryHandle, filepath.Join(filepath.Dir(f.selected.Directory), "different-spelling")) {
		windows.CloseHandle(directoryHandle)
		t.Fatal("native canonical identity comparison failed")
	}
	windows.CloseHandle(directoryHandle)
	if actual, e := readConfig(path); e != nil || actual.InstallationID != c.InstallationID {
		t.Fatal("native config read failed")
	}
	link := filepath.Join(filepath.Dir(path), "hardlink.json")
	if os.Link(path, link) != nil {
		t.Fatal("hardlink fixture unavailable")
	}
	if _, e := readConfig(path); e == nil {
		t.Fatal("hardlinked config admitted")
	}
	if os.Remove(link) != nil {
		t.Fatal("hardlink fixture cleanup")
	}
	// Junction construction is confined to the owned synthetic fixture; it needs
	// no symlink privilege and never changes an existing directory or user source.
	alias := filepath.Join(filepath.Dir(path), "alias")
	if os.Mkdir(alias, 0700) != nil {
		t.Fatal("junction fixture unavailable")
	}
	p, _ := windows.UTF16PtrFromString(alias)
	h, e := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal("junction fixture handle unavailable")
	}
	target := filepath.Dir(path)
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
	if _, e := readConfig(filepath.Join(alias, "runtime.json")); e == nil {
		t.Fatal("junction config ancestor admitted")
	}
	// Config data never substitutes installation authority or starts a second
	// lifetime while the current native lease is held.
	c.InstallationID = "40000000-0000-4000-8000-000000000002"
	raw, _ := json.Marshal(c)
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("mismatch fixture unavailable")
	}
	var out, diagnostic bytes.Buffer
	if run(f.ctx, []string{"serve", "--config", path}, nil, &out, &diagnostic) != 2 {
		t.Fatal("config installation mismatch accepted")
	}
	if strings.Contains(out.String(), path) || strings.Contains(diagnostic.String(), path) {
		t.Fatal("private config path disclosed")
	}
	if _, e := S.Existing(f.ctx, f.selected.Directory, f.selected.Identity); e == nil {
		t.Fatal("failed config displaced source exclusion")
	}
	f.close(t)
	c.InstallationID = f.selected.InstallationID
	c.Board.Directory = filepath.Join(filepath.Dir(path), "missing-board")
	raw, _ = json.Marshal(c)
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("absent-source fixture unavailable")
	}
	if run(f.ctx, []string{"serve", "--config", path}, nil, io.Discard, io.Discard) != 3 {
		t.Fatal("absent source accepted")
	}
	if _, e := os.Stat(c.Board.Directory); !os.IsNotExist(e) {
		t.Fatal("serve created absent source")
	}
}

func binaryPath(t *testing.T) string {
	t.Helper()
	p := os.Getenv("P01_BINARY")
	if !safeAbsolute(p) {
		t.Fatal("compiled private overlay binary required")
	}
	if _, e := os.Stat(p); e != nil {
		t.Fatal("compiled private overlay binary unavailable")
	}
	return p
}

func signalOwnedHiddenConsole(t *testing.T, pid int) {
	t.Helper()
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	free := kernel.NewProc("FreeConsole")
	attach := kernel.NewProc("AttachConsole")
	// Detach this test process if it inherited a console; attach ONLY the new
	// hidden child. Never attach or send an event to a user/parent process.
	_, _, _ = free.Call()
	ok, _, _ := attach.Call(uintptr(pid))
	if ok == 0 {
		t.Fatal("owned child console unavailable")
	}
	defer free.Call()
	if windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid)) != nil {
		t.Fatal("owned child stop signal unavailable")
	}
}

func TestNativeCompiledExecutablePinnedServeCLIAndGracefulStop(t *testing.T) {
	exe := binaryPath(t)
	for _, port := range []int{47443, 48443} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			f := privateFixture(t, port)
			f.credential(t)
			selection := setup.CredentialSelection{InstallationID: f.selected.InstallationID, BoardID: testBoard, Incarnation: testInc, GrantID: f.grant}
			lease, e := f.manager.PrepareCredential(selection)
			if e != nil {
				t.Fatal("private credential fixture lease")
			}
			if lease.Disable() != nil {
				lease.Close()
				t.Fatal("private credential disable fixture")
			}
			if _, e = lease.Publish(f.secret); e != nil {
				lease.Close()
				t.Fatal("private credential fixture publication")
			}
			lease.Close()
			connection := f.native.Context()
			cfg, path := configFor(t, f)
			f.close(t)
			for attempt := 0; attempt < 2; attempt++ {
				cmd := exec.Command(exe, "serve", "--config", path)
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NEW_CONSOLE | windows.CREATE_NEW_PROCESS_GROUP}
				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if cmd.Start() != nil {
					t.Fatal("compiled executable start failed")
				}
				wait := make(chan error, 1)
				go func() { wait <- cmd.Wait() }()
				stopped := false
				defer func() {
					if !stopped {
						_ = cmd.Process.Kill()
						<-wait
					}
				}()
				tlsConfig, e := connection.TLSConfig()
				if e != nil {
					t.Fatal("private issuer pin unavailable")
				}
				transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp4", fmt.Sprintf("127.0.0.1:%d", port))
				}}
				client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
				deadline := time.Now().Add(10 * time.Second)
				for {
					r, e := client.Get(connection.Origin + "/")
					if e == nil {
						body, readErr := io.ReadAll(r.Body)
						r.Body.Close()
						if r.StatusCode != 200 || readErr != nil || r.Header.Get("X-Localswim-Installation") != connection.InstallationID {
							t.Fatal("compiled bootstrap boundary failed")
						}
						static, e := assets.Handler()
						if e != nil {
							t.Fatal("embedded assets unavailable")
						}
						w := httptest.NewRecorder()
						static.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
						if !bytes.Equal(body, w.Body.Bytes()) {
							t.Fatal("bootstrap changed verified HTML")
						}
						break
					}
					select {
					case <-wait:
						stopped = true
						t.Fatal("compiled serve exited before readiness")
					default:
					}
					if time.Now().After(deadline) {
						t.Fatal("compiled serve readiness timed out")
					}
					time.Sleep(20 * time.Millisecond)
				}
				for _, target := range []struct {
					path   string
					header bool
				}{{"/index.html", true}, {"/styles/app.css", false}, {"/api/v1/session", false}, {"/?unexpected=1", false}, {"/?", false}, {"/%69ndex.html", false}, {"/missing.html", false}} {
					r, e := client.Get(connection.Origin + target.path)
					if e != nil {
						t.Fatal("compiled request failed")
					}
					r.Body.Close()
					got := r.Header.Get("X-Localswim-Installation")
					if (got != "") != target.header || target.header && got != cfg.InstallationID {
						t.Fatal("bootstrap header escaped selected HTML")
					}
				}
				for _, method := range []string{http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete} {
					req, e := http.NewRequest(method, connection.Origin+"/", nil)
					if e != nil {
						t.Fatal("method fixture unavailable")
					}
					req.Header.Set("Content-Type", "application/json")
					r, e := client.Do(req)
					if e != nil {
						t.Fatal("compiled method refusal unavailable")
					}
					r.Body.Close()
					if method != http.MethodHead && r.StatusCode == 200 || r.Header.Get("X-Localswim-Installation") != "" {
						t.Fatal("wrong method received bootstrap metadata")
					}
				}
				cli := exec.Command(exe, "--config", path, "--json", "--installation", selection.InstallationID, "--board", selection.BoardID, "--incarnation", selection.Incarnation, "--grant", selection.GrantID, "board", "read")
				cliOut, e := cli.Output()
				if e != nil || !bytes.Contains(cliOut, []byte(`"boardId":"`+testBoard+`"`)) && !bytes.Contains(cliOut, []byte(`"boardId": "`+testBoard+`"`)) {
					t.Fatal("compiled CLI did not use actual protected credential and pinned server")
				}
				if bytes.Contains(cliOut, []byte(path)) || bytes.Contains(cliOut, []byte(f.secret.Token)) {
					t.Fatal("compiled CLI disclosed private data")
				}
				// This credential has real export authority: failure must come
				// from native publication overlap policy, not from missing rights.
				privateRoot, e := f.manager.ProtectedRoot()
				if e != nil {
					t.Fatal("native inventory root unavailable")
				}
				blocked := filepath.Join(privateRoot, "must-not-publish.zip")
				blockedCLI := exec.Command(exe, "--config", path, "--json", "--installation", selection.InstallationID, "--board", selection.BoardID, "--incarnation", selection.Incarnation, "--grant", selection.GrantID, "export", "create", "--destination", blocked)
				blockedOut, e := blockedCLI.Output()
				var exit *exec.ExitError
				if !errors.As(e, &exit) || exit.ExitCode() != 1 {
					t.Fatal("compiled export allowed native ROOT destination")
				}
				if _, e := os.Stat(blocked); !os.IsNotExist(e) {
					t.Fatal("blocked native ROOT destination was created")
				}
				if bytes.Contains(blockedOut, []byte(privateRoot)) {
					t.Fatal("protected root disclosed")
				}
				if _, e := f.manager.AcquireServer(); e == nil {
					t.Fatal("compiled server did not own N01 exclusion")
				}
				signalOwnedHiddenConsole(t, cmd.Process.Pid)
				select {
				case e := <-wait:
					stopped = true
					if e != nil {
						t.Fatal("compiled graceful stop failed")
					}
				case <-time.After(15 * time.Second):
					t.Fatal("compiled graceful stop timed out")
				}
				client.CloseIdleConnections()
				if stdout.Len() != 0 || stderr.Len() != 0 {
					t.Fatal("successful serve emitted unexpected output")
				}
				opened, e := S.Existing(f.ctx, f.selected.Directory, f.selected.Identity)
				if e != nil {
					t.Fatal("compiled shutdown retained source exclusion")
				}
				if opened.Close() != nil {
					t.Fatal("reopened source close")
				}
				native, e := f.manager.AcquireServer()
				if e != nil {
					t.Fatal("compiled shutdown retained native exclusion")
				}
				native.Close()
			}
		})
	}
}
