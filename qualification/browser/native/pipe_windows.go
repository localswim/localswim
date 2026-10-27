//go:build windows && amd64 && v02qualification

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/signal"
	"time"
	"unsafe"
)

func signalNotify(c chan os.Signal) { signal.Notify(c, os.Interrupt) }

type request struct {
	Op      string `json:"op"`
	Subject string `json:"subject,omitempty"`
}

func (x *runtimeFixture) dispatch(q request) any {
	fail := map[string]any{"ok": false, "error": "V02_REFUSED"}
	if q.Op == "status" {
		h, e := x.state(x.f.HumanActor)
		if e != nil {
			return fail
		}
		a, e := x.state(x.f.AutomationActor)
		if e != nil {
			return fail
		}
		v, e := x.state(x.f.RevocableActor)
		if e != nil {
			return fail
		}
		rev, e := x.revision()
		if e != nil {
			return fail
		}
		x.delivery.mu.Lock()
		reached := x.delivery.reached
		x.delivery.mu.Unlock()
		return map[string]any{"ok": true, "ready": x.qualified && x.trusted() && x.humanReady(), "trustInstalled": x.trusted(), "humanBootstrapConfirmed": x.humanReady(), "ownerDelegationConfirmed": x.qualified, "profileId": x.f.ProfileID, "installationId": x.f.InstallationID, "origin": x.f.Origin, "boardId": x.f.BoardID, "incarnation": x.f.Incarnation, "human": h, "automation": a, "revocable": v, "revision": rev, "mutationCount": x.delivery.mutations.Load() + x.mutations, "lookupCount": x.delivery.lookups.Load() + x.lookups, "delayReached": reached}
	}
	if q.Op == "auditProfile" {
		return auditProfile(x.f)
	}
	if q.Op == "stop" {
		x.stopping = true
		return map[string]any{"ok": true}
	}
	if q.Op == "release" {
		x.delivery.release()
		return map[string]any{"ok": true}
	}
	if !x.qualified || !x.trusted() || !x.humanReady() {
		return fail
	}
	switch q.Op {
	case "code":
		actor := map[string]string{"human": x.f.HumanActor, "automation": x.f.AutomationActor, "revocable": x.f.RevocableActor}[q.Subject]
		g, e := x.state(actor)
		if e != nil || !g.Enabled {
			return fail
		}
		token, e := x.nativeToken()
		if e != nil {
			return fail
		}
		v, e := x.sessions.IssueCode(x.ctx, token, T.CodeRequest{InstallationID: T.UUIDv4(x.f.InstallationID), BoardID: A.UUID(x.f.BoardID), Incarnation: A.UUID(x.f.Incarnation), GrantID: A.UUID(g.Grant)})
		if e != nil {
			return fail
		} // encode directly to protected pipe, never logs, files or URLs.
		return v
	case "provisionAutomation":
		for _, actor := range []string{x.f.AutomationActor, x.f.RevocableActor} {
			g, e := x.state(actor)
			if e != nil {
				return fail
			}
			if g.Grant != "" {
				if !g.Enabled || g.Kind != "automation" || !equal(g.Rights, automationRights) {
					return fail
				}
				continue
			}
			if x.submit(actor, "automation", automationRights, "") != nil {
				return fail
			}
		}
		return map[string]any{"ok": true}
	case "revoke":
		if q.Subject != "revocable" {
			return fail
		}
		g, e := x.state(x.f.RevocableActor)
		if e != nil || !g.Enabled || x.submit("", "", nil, g.Grant) != nil {
			return fail
		}
	case "dropNext", "delayNext":
		x.delivery.mu.Lock()
		defer x.delivery.mu.Unlock()
		if x.delivery.nextMode != "" || x.delivery.gate != nil {
			return fail
		}
		x.delivery.nextMode = map[string]string{"dropNext": "drop", "delayNext": "delay"}[q.Op]
	case "release":
		x.delivery.release()
	case "restart":
		x.mutations += x.delivery.mutations.Load()
		x.lookups += x.delivery.lookups.Load()
		x.close()
		if x.open() != nil {
			x.stopping = true
			return fail
		}
	case "stop":
		x.stopping = true
	default:
		return fail
	}
	return map[string]any{"ok": true}
}
func valid(q request) bool {
	switch q.Op {
	case "code":
		return q.Subject == "human" || q.Subject == "automation" || q.Subject == "revocable"
	case "revoke":
		return q.Subject == "revocable"
	case "auditProfile", "status", "provisionAutomation", "dropNext", "delayNext", "release", "restart", "stop":
		return q.Subject == ""
	}
	return false
}

func (x *runtimeFixture) pipe() error {
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return fixtureFailure("V02_PIPE_ACCOUNT_UNAVAILABLE")
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;;GA;;;" + u.User.Sid.String() + ")(A;;GA;;;SY)")
	if e != nil {
		return fixtureFailure("V02_PIPE_DESCRIPTOR_UNAVAILABLE")
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, e := windows.CreateNamedPipe(windows.StringToUTF16Ptr(x.f.PipeName), windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE|windows.FILE_FLAG_OVERLAPPED, windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 8192, 1024, 5000, sa)
	if e != nil {
		return fixtureFailure("V02_PIPE_CREATE_UNAVAILABLE")
	}
	f := os.NewFile(uintptr(h), "v02-private-pipe")
	defer f.Close()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sig:
			x.cancel()
			_ = windows.CancelIoEx(h, nil)
			_ = f.Close()
		case <-done:
		}
	}()
	for !x.stopping {
		if x.ctx.Err() != nil {
			return nil
		}
		event, e := windows.CreateEvent(nil, 1, 0, nil)
		if e != nil {
			return fixtureFailure("V02_PIPE_EVENT_UNAVAILABLE")
		}
		ov := windows.Overlapped{HEvent: event | 1}
		e = windows.ConnectNamedPipe(h, &ov)
		if e == windows.ERROR_IO_PENDING {
			for {
				code, err := windows.WaitForSingleObject(event, 100)
				if err != nil {
					e = unavailable
					break
				}
				if code == windows.WAIT_OBJECT_0 {
					var transferred uint32
					e = windows.GetOverlappedResult(h, &ov, &transferred, false)
					break
				}
				if x.ctx.Err() != nil {
					_ = windows.CancelIoEx(h, &ov)
					var transferred uint32
					_ = windows.GetOverlappedResult(h, &ov, &transferred, true)
					e = unavailable
					break
				}
			}
		}
		_ = windows.CloseHandle(event)
		if e != nil && e != windows.ERROR_PIPE_CONNECTED {
			return fixtureFailure("V02_PIPE_CONNECT_UNAVAILABLE")
		}
		if f.SetDeadline(time.Now().Add(5*time.Second)) != nil {
			return fixtureFailure("V02_PIPE_DEADLINE_UNAVAILABLE")
		}
		r := bufio.NewReader(io.LimitReader(f, 1025))
		wire, e := r.ReadBytes('\n')
		var response any = map[string]any{"ok": false, "error": "V02_REFUSED"}
		if e == nil && len(wire) <= 1024 && r.Buffered() == 0 {
			var q request
			d := json.NewDecoder(bytes.NewReader(wire))
			d.DisallowUnknownFields()
			_, strict := contract.DecodeJSON(wire)
			if strict == nil && d.Decode(&q) == nil && valid(q) {
				_ = f.SetDeadline(time.Now().Add(30 * time.Second))
				response = x.dispatch(q)
			}
		}
		frame, e := encodePipeFrame(response)
		response = nil
		if e == nil {
			_, _ = f.Write(frame)
		}
		clear(frame)
		// Wait for the client to close after consuming the response. Disconnecting
		// immediately can discard unread pipe bytes. The same 5-second deadline bounds
		// a client that never reads/closes; no second frame is ever dispatched.
		var extra [1]byte
		_, _ = f.Read(extra[:])
		for i := range wire {
			wire[i] = 0
		}
		_ = windows.DisconnectNamedPipe(h)
	}
	return nil
}

// encodePipeFrame is private to the protected pipe and its synthetic encoder
// test. Secret bodies opt in through the production ephemeral encoder; they
// never enter generic JSON output, stdout, logs, evidence or retained files.
func encodePipeFrame(response any) ([]byte, error) {
	var raw []byte
	var e error
	switch secret := response.(type) {
	case T.CodeSecret:
		raw, e = T.MarshalEphemeral(secret)
	default:
		raw, e = json.Marshal(response)
	}
	defer func() { clear(raw) }()
	if e != nil || len(raw) > 8191 {
		return nil, unavailable
	}
	// D02 ephemeral encoding is readable/multiline. IPC requires one NDJSON frame.
	var compact bytes.Buffer
	defer func() { clear(compact.Bytes()) }()
	if json.Compact(&compact, raw) != nil || compact.Len() > 8191 {
		return nil, unavailable
	}
	frame := make([]byte, compact.Len()+1)
	copy(frame, compact.Bytes())
	frame[len(frame)-1] = '\n'
	return frame, nil
}
