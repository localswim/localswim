package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	A "github.com/localswim/localswim/internal/contract/archive"
	local "github.com/localswim/localswim/internal/transport/local"
)

func runLocalRestore(ctx context.Context, args []string, in io.Reader, out, diagnostic io.Writer) (bool, int) {
	if len(args) == 0 || (args[0] != "restore" && args[0] != "restore-status") {
		return false, 0
	}
	fail := func() (bool, int) { return true, entryFailure(args, out, diagnostic, "INVOCATION_OR_IO", 1) }
	values := map[string]string{}
	jsonOutput := false
	for i := 1; i < len(args); i++ {
		key := args[i]
		if key == "--json" {
			if jsonOutput {
				return fail()
			}
			jsonOutput = true
			continue
		}
		if i+1 >= len(args) || values[key] != "" {
			return fail()
		}
		i++
		value := args[i]
		if value == "" || strings.ContainsAny(value, "\x00\r\n") {
			return fail()
		}
		switch key {
		case "--data":
		case "--source", "--port":
			if args[0] != "restore" {
				return fail()
			}
		case "--request-id":
			if args[0] != "restore-status" {
				return fail()
			}
		default:
			return fail()
		}
		values[key] = value
	}
	if out == nil || diagnostic == nil || values["--data"] == "" {
		return fail()
	}
	data, e := filepath.Abs(filepath.FromSlash(values["--data"]))
	if e != nil || !safeAbsolute(data) {
		return fail()
	}
	var status A.Status
	exit := 4
	if args[0] == "restore-status" {
		request := A.UUID(values["--request-id"])
		if values["--request-id"] == "" || A.Validate(request) != nil {
			return fail()
		}
		r := local.RestoreStatus(ctx, data, request)
		status = r.Status
		exit = r.Exit
	} else {
		if values["--source"] == "" {
			return fail()
		}
		source, e := filepath.Abs(filepath.FromSlash(values["--source"]))
		if e != nil || !safeAbsolute(source) {
			return fail()
		}
		port := 0
		if value := values["--port"]; value != "" {
			port, e = strconv.Atoi(value)
			if e != nil || port < 1024 || port > 65535 || strconv.Itoa(port) != value {
				return fail()
			}
		}
		cwd, e := os.Getwd()
		if e != nil {
			return fail()
		}
		exe, e := os.Executable()
		if e != nil {
			return fail()
		}
		var b [16]byte
		if _, e = rand.Read(b[:]); e != nil {
			return fail()
		}
		b[6] = (b[6] & 15) | 64
		b[8] = (b[8] & 63) | 128
		request := A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
		if _, e = fmt.Fprintln(diagnostic, "RESTORE_REQUEST "+string(request)); e != nil {
			return true, 1
		}
		r := local.Restore(ctx, local.RestoreOptions{SourceArchive: source, DataDir: data, RequestID: request, ProtectedRoots: restoreProtectedRoots(cwd, exe), Port: port})
		status = r.Status
		exit = r.Exit
	}
	if jsonOutput {
		if json.NewEncoder(out).Encode(status) != nil {
			return true, 1
		}
	} else {
		if _, e = fmt.Fprintln(out, "RESTORE "+strings.ToUpper(string(status.Outcome))); e != nil {
			return true, 1
		}
	}
	return true, exit
}

// Protect a known source checkout, without treating an arbitrary working
// directory such as Documents as a live board or source tree.
func restoreProtectedRoots(cwd, exe string) []string {
	roots := []string{filepath.Dir(exe)}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		_, gitErr := os.Stat(filepath.Join(dir, ".git"))
		_, modErr := os.Stat(filepath.Join(dir, "go.mod"))
		if gitErr == nil || modErr == nil {
			roots = append(roots, dir)
			break
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	return roots
}
