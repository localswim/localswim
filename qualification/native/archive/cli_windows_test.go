//go:build nativequalification && windows && amd64

package archivequalification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	L "github.com/localswim/localswim/internal/transport/local"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func cliRestored(t *testing.T, ctx context.Context, attempt, archive string) (*L.Runtime, string, L.ClientInfo) {
	t.Helper()
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	appDir := filepath.Join(attempt, "app")
	if e := os.Mkdir(appDir, 0700); e != nil {
		t.Fatal("private application directory unavailable")
	}
	binary := filepath.Join(appDir, "localswim.exe")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/localswim")
	build.Dir = repo
	buildLog, e := build.CombinedOutput()
	save(t, filepath.Join(attempt, "application-build.log"), buildLog)
	if e != nil {
		t.Fatal("actual application build failed")
	}
	raw, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	save(t, filepath.Join(attempt, "application.sha256"), []byte(fmt.Sprintf("%x\n", sha256.Sum256(raw))))
	root := filepath.Join(attempt, "unknown-copy")
	command := exec.CommandContext(ctx, binary, "restore", "--source", archive, "--data", root, "--port", fmt.Sprint(port(t)), "--json")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	e = command.Run()
	save(t, filepath.Join(attempt, "cli-restore.json"), stdout.Bytes())
	save(t, filepath.Join(attempt, "cli-restore.stderr"), stderr.Bytes())
	if e != nil {
		t.Fatal("actual CLI restore failed")
	}
	status, e := A.Decode[A.Status](stdout.Bytes())
	if e != nil || status.Outcome != "confirmed" {
		t.Fatal("CLI restore status malformed")
	}
	marker := strings.TrimSpace(stderr.String())
	request := strings.TrimPrefix(marker, "RESTORE_REQUEST ")
	if request == marker || A.Validate(A.UUID(request)) != nil {
		t.Fatal("CLI did not emit exact pre-effect request marker")
	}
	before := hashTree(t, root)
	check := exec.CommandContext(ctx, binary, "restore-status", "--data", root, "--request-id", request, "--json")
	output, e := check.CombinedOutput()
	save(t, filepath.Join(attempt, "cli-restore-status.json"), output)
	if e != nil {
		t.Fatal("actual CLI restored status failed")
	}
	got, e := A.Decode[A.Status](output)
	if e != nil || got.Outcome != "confirmed" || !reflect.DeepEqual(before, hashTree(t, root)) {
		t.Fatal("CLI status malformed or mutated restored data")
	}
	r, e := L.Start(ctx, L.Options{DataDir: root})
	if e != nil {
		t.Fatal("CLI restored copy cannot start normally")
	}
	t.Cleanup(func() {
		if e := r.Close(); e != nil {
			t.Error("CLI restored shutdown failed")
		}
	})
	info, e := L.ReadClient(root)
	if e != nil {
		t.Fatal("CLI restored metadata unavailable")
	}
	return r, root, info
}
