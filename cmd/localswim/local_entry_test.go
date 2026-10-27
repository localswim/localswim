package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalEntryRejectsRemoteAndActorOverrides(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--config", `C:\synthetic\old.json`},
		{"serve", "--data", `C:\synthetic\board`, "--host", "0.0.0.0"},
		{"serve", "--data", `C:\synthetic\board`, "--port", "08792"},
		{"--data", `C:\synthetic\board`, "--actor", "human", "board", "read"},
		{"--data", `C:\synthetic\board`, "--grant", "00000000-0000-4000-8000-000000000001", "board", "read"},
		{"--config", `C:\synthetic\old.json`, "setup", "init"},
	} {
		if _, e := parseLocal(args); e == nil {
			t.Fatal("legacy/network/authority override accepted")
		}
	}
	q, e := parseLocal([]string{"serve", "--data", `C:\synthetic\board`, "--port", "8792"})
	if e != nil || !q.serve || q.port != 8792 {
		t.Fatal("normal local invocation refused")
	}
	var output bytes.Buffer
	if run(context.Background(), []string{"--help"}, nil, &output, nil) != 0 || !bytes.Contains(output.Bytes(), []byte("localhost swimlanes")) {
		t.Fatal("local help unavailable")
	}
}

func TestNormalEntryDispatchesReadOnlyRestoreStatus(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-created")
	var output, diagnostic bytes.Buffer
	exit := run(context.Background(), []string{"restore-status", "--data", missing, "--request-id", "00000000-0000-4000-8000-000000000001", "--json"}, nil, &output, &diagnostic)
	var status struct {
		Scope   string `json:"scope"`
		Outcome string `json:"outcome"`
	}
	if exit != 4 || json.Unmarshal(output.Bytes(), &status) != nil || status.Scope != "restore" || status.Outcome != "unknown" {
		t.Fatal("normal executable did not dispatch restore status")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("status created a missing destination")
	}
	output.Reset()
	if run(context.Background(), []string{"--help"}, nil, &output, &diagnostic) != 0 || !bytes.Contains(output.Bytes(), []byte("restore-status")) {
		t.Fatal("normal help omitted restore entry")
	}
}
