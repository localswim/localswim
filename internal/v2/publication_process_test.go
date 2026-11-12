package v2

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A disposable subprocess is both the SSH transport and its pipe-inheriting
// child. It holds an OS lock for its whole lifetime, making cleanup observable
// without relying on PID reuse or platform-specific zombie process reporting.
func TestM90ProcessHelper(t *testing.T) {
	mode := os.Getenv("LOCALSWIM_M90_MODE")
	if mode == "" {
		return
	}
	root := os.Getenv("LOCALSWIM_M90_ROOT")
	if mode == "child" {
		file, err := os.OpenFile(filepath.Join(root, "child.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil || lockWriter(file) != nil {
			os.Exit(2)
		}
		defer file.Close()
		if os.WriteFile(filepath.Join(root, "ready"), []byte(strconv.Itoa(os.Getpid())), 0o600) != nil {
			os.Exit(2)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	if mode == "transport" {
		_, err := os.Stat(filepath.Join(root, "recover"))
		if err == nil || os.Getenv("LOCALSWIM_M90_DELIVER") == "1" {
			command := exec.Command("git", "receive-pack", os.Getenv("LOCALSWIM_M90_REMOTE"))
			command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
			if command.Run() != nil {
				os.Exit(2)
			}
			if err == nil {
				os.Exit(0)
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	child := exec.Command(executable, "-test.run=^TestM90ProcessHelper$")
	child.Env = append(os.Environ(), "LOCALSWIM_M90_MODE=child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if child.Start() != nil {
		os.Exit(2)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "ready")); err == nil {
			break
		}
		if !time.Now().Before(deadline) {
			os.Exit(2)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mode == "exit" {
		os.Exit(0)
	}
	for {
		time.Sleep(time.Second)
	}
}

func m90CheckChildStopped(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(root, "ready")); err != nil {
		t.Fatal("the helper never started", err)
	}
	file, err := os.OpenFile(filepath.Join(root, "child.lock"), os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := lockWriter(file); err != nil {
		t.Fatal("the pipe-inheriting helper survived cleanup", err)
	}
	_ = unlockWriter(file)
}

func TestM90CommandDeadlineContainsInheritedPipes(t *testing.T) {
	for _, mode := range []string{"wait", "exit"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(executable, "-test.run=^TestM90ProcessHelper$")
			command.Env = append(os.Environ(), "LOCALSWIM_M90_MODE="+mode, "LOCALSWIM_M90_ROOT="+root)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			started := time.Now()
			if err := runPublicationCommand(ctx, command); !errors.Is(err, errPublicationGitTimeout) {
				t.Fatalf("stalled command returned %v", err)
			}
			if elapsed := time.Since(started); elapsed > 8*time.Second {
				t.Fatal("inherited pipes exceeded the command and cleanup deadlines", elapsed)
			}
			m90CheckChildStopped(t, root)
		})
	}
}

func TestM90StalledPublicationPreservesCommitAndRecovers(t *testing.T) {
	for _, delivered := range []bool{false, true} {
		t.Run(strconv.FormatBool(delivered), func(t *testing.T) {
			fixture := m34PublicationFixture(t)
			fixture.editor.client = &http.Client{Timeout: publicationGitTimeout + 20*time.Second}
			root := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("LOCALSWIM_M90_MODE", "transport")
			t.Setenv("LOCALSWIM_M90_ROOT", root)
			t.Setenv("LOCALSWIM_M90_REMOTE", fixture.remote)
			if delivered {
				t.Setenv("LOCALSWIM_M90_DELIVER", "1")
			}
			m34Git(t, fixture.repository, "config", "core.sshCommand", "'"+filepath.ToSlash(executable)+"' -test.run=^TestM90ProcessHelper$ --")
			m34Git(t, fixture.repository, "config", "ssh.variant", "simple")
			m34Git(t, fixture.repository, "remote", "set-url", "origin", "ssh://localhost/disposable")
			baseline := m34Git(t, fixture.repository, "rev-parse", "HEAD")
			started := time.Now()
			_, failed := m89Publish(t, fixture, "")
			if failed.State != "failed" || snapshotString(failed.ErrorCode) != "git_push_timeout" {
				t.Fatalf("stalled publication did not return a durable timeout: %+v", failed)
			}
			if elapsed := time.Since(started); elapsed < publicationGitTimeout || elapsed > publicationGitTimeout+15*time.Second {
				t.Fatal("stalled publication exceeded its bounded attempt", elapsed)
			}
			m90CheckChildStopped(t, root)
			pending := m34Git(t, fixture.repository, "rev-parse", "HEAD")
			if bytes.Equal(pending, baseline) {
				t.Fatal("the known local publication commit was lost")
			}
			stateRef := publicationCheckpointRef(fixture.app.store.root, "backup-"+fixture.item.ID)
			intent := m34Git(t, fixture.repository, "rev-parse", stateRef)
			lease, err := acquirePublicationRepository(fixture.repository, 0)
			if err != nil {
				t.Fatal("publication retained its repository lock after cleanup", err)
			}
			repo := publicationRepository{root: fixture.repository, prefix: fixture.prefix, lease: lease}
			checkpoint, oid, inspectErr := repo.checkpoint(stateRef)
			releasePublicationRepository(lease)
			if inspectErr != nil || checkpoint.Pending == nil || checkpoint.Pending.Commit != strings.TrimSpace(string(pending)) || oid != strings.TrimSpace(string(intent)) {
				t.Fatal("timeout lost the exact pending commit evidence", inspectErr)
			}
			remote := m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")
			if delivered && !bytes.Equal(remote, pending) || !delivered && !bytes.Equal(remote, baseline) {
				t.Fatal("fixture did not exercise the intended remote uncertainty")
			}
			m34Write(t, filepath.Join(root, "recover"), []byte("ready"))
			_, recovered := m89Publish(t, fixture, "")
			if recovered.State != "succeeded" || recovered.ErrorCode != nil || recovered.ID == failed.ID {
				t.Fatal("the same pending publication did not recover")
			}
			if !bytes.Equal(pending, m34Git(t, fixture.repository, "rev-parse", "HEAD")) || !bytes.Equal(pending, m34Git(t, fixture.remote, "rev-parse", "refs/heads/main")) {
				t.Fatal("recovery created or delivered a different commit")
			}
			if count := m34Git(t, fixture.repository, "rev-list", "--count", strings.TrimSpace(string(baseline))+"..HEAD"); string(count) != "1\n" {
				t.Fatal("recovery duplicated the local commit")
			}
			if refs := m34Git(t, fixture.repository, "for-each-ref", "--format=%(refname)", stateRef, publicationSendRefPrefix); len(refs) != 0 {
				t.Fatal("recovery retained a settled publication ref")
			}
		})
	}
}
