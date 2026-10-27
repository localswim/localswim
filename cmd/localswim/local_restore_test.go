package main

import (
	"bytes"
	"context"
	"encoding/json"
	A "github.com/localswim/localswim/internal/contract/archive"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalRestoreEntryRequestAndJSON(t *testing.T) {
	var out, diagnostic bytes.Buffer
	root := t.TempDir()
	handled, exit := runLocalRestore(context.Background(), []string{"restore", "--source", filepath.Join(root, "absent.zip"), "--data", filepath.Join(root, "new"), "--json"}, nil, &out, &diagnostic)
	if !handled || exit != 4 {
		t.Fatal("unavailable restore should be unknown")
	}
	var status A.Status
	if json.Unmarshal(out.Bytes(), &status) != nil || status.Outcome != "unknown" {
		t.Fatal("stdout is not one archive status")
	}
	request := strings.TrimSpace(strings.TrimPrefix(diagnostic.String(), "RESTORE_REQUEST "))
	if !strings.HasPrefix(diagnostic.String(), "RESTORE_REQUEST ") || A.Validate(A.UUID(request)) != nil {
		t.Fatal("request identifier missing before effects")
	}
}
func TestLocalRestoreEntryRejectsSelectors(t *testing.T) {
	for _, args := range [][]string{{"restore", "--data", "x", "--source", "y", "--actor", "human"}, {"restore-status", "--data", "x", "--source", "y"}, {"restore", "--data", "x", "--source", "y", "--json", "--json"}} {
		var out, diag bytes.Buffer
		handled, exit := runLocalRestore(context.Background(), args, nil, &out, &diag)
		if !handled || exit != 1 || strings.Contains(diag.String(), "RESTORE_REQUEST") {
			t.Fatal("invalid restore invocation caused effects")
		}
	}
	if handled, _ := runLocalRestore(context.Background(), []string{"serve"}, nil, nil, nil); handled {
		t.Fatal("restore consumed normal command")
	}
}
