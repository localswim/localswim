package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func validConfigBytes(t *testing.T) []byte {
	t.Helper()
	b, e := json.Marshal(runtimeConfig{ConfigVersion: 1, InstallationID: "40000000-0000-4000-8000-000000000001", EvidenceDirectory: `C:\synthetic\evidence`, ProtectedRoots: []string{}, Board: &runtimeBoard{BoardID: testBoard, Incarnation: testCard, Directory: `C:\synthetic\board`}})
	if e != nil {
		t.Fatal("fixture unavailable")
	}
	return b
}

func TestRuntimeConfigStrictSelection(t *testing.T) {
	raw := validConfigBytes(t)
	c, e := parseConfig(raw)
	if e != nil {
		t.Fatal("explicit config refused")
	}
	if !reflect.DeepEqual(protectedInventory(c, `C:\synthetic\native`), []string{`C:\synthetic\native`, `C:\synthetic\evidence`, `C:\synthetic\board`}) {
		t.Fatal("protected inventory omitted an owned root")
	}
	for _, bad := range []string{`C:\synthetic\native`, `c:\SYNTHETIC\NATIVE\child`, `C:\synthetic`, `C:\synthetic\board`, `C:\synthetic\board\child`} {
		q := c
		q.EvidenceDirectory = bad
		if separateEvidence(q, `C:\synthetic\native`) {
			t.Fatal("ownership overlap accepted")
		}
	}
	q := c
	q.EvidenceDirectory = `C:\synthetic\native-old`
	if !separateEvidence(q, `C:\synthetic\native`) {
		t.Fatal("path segment boundary confused")
	}
	for _, bad := range [][]byte{
		[]byte(`{}`), append(append([]byte{}, raw...), []byte(`{}`)...), bytes.Replace(raw, []byte(`"configVersion":1`), []byte(`"configVersion":2`), 1),
		bytes.Replace(raw, []byte(`"configVersion":1`), []byte(`"configVersion":1,"configVersion":1`), 1),
		bytes.Replace(raw, []byte(`"configVersion":1`), []byte(`"configVersion":1,"secret":"forbidden"`), 1),
		bytes.Replace(raw, []byte(`"protectedRoots":[]`), []byte(`"protectedRoots":null`), 1),
		bytes.Replace(raw, []byte(`40000000-0000-4000-8000-000000000001`), []byte(`40000000-0000-1000-8000-000000000001`), 1),
		bytes.Replace(raw, []byte(`C:\\synthetic\\board`), []byte(`relative`), 1),
		bytes.Repeat([]byte(" "), 64*1024+1), []byte("\xff"),
	} {
		if _, e := parseConfig(bad); e == nil {
			t.Fatal("ambiguous/unsafe config accepted")
		}
	}
	var null map[string]any
	if json.Unmarshal(raw, &null) != nil {
		t.Fatal("fixture shape")
	}
	null["board"] = nil
	nullRaw, _ := json.Marshal(null)
	if c, e := parseConfig(nullRaw); e != nil || c.Board != nil {
		t.Fatal("explicit offline null board refused")
	}
}

func TestRootConfigPrefixDelegation(t *testing.T) {
	for _, args := range [][]string{{"--config", `C:\synthetic\runtime.json`, "--json", "board", "read"}, {"serve", "--config", `C:\synthetic\runtime.json`}, {"--json", "setup", "status"}} {
		q, e := parseRoot(args)
		if e != nil {
			t.Fatal("accepted root grammar refused")
		}
		if args[0] == "--config" && !reflect.DeepEqual(q.args, args[2:]) {
			t.Fatal("CLI arguments changed")
		}
	}
	for _, args := range [][]string{{"serve"}, {"serve", "--config", `C:\synthetic\runtime.json`, "--json"}, {"--config"}, {"--config", "relative", "board", "read"}, {"--config", `C:\synthetic\runtime.json`, "--config", `C:\synthetic\other.json`, "board", "read"}, {"--json", "--config", `C:\synthetic\runtime.json`, "board", "read"}} {
		if _, e := parseRoot(args); e == nil {
			t.Fatal("ambiguous root grammar accepted")
		}
	}
	for _, path := range []string{`relative`, `\\host\share\file`, `C:\unsafe\..\file`, `C:\unsafe\file:stream`, `C:\unsafe\NUL.json`, `C:\unsafe\trailing.`, `C:/unsafe/file`, `C:\unsafe\` + "bad\nname"} {
		if safeAbsolute(path) {
			t.Fatal("unsafe path accepted")
		}
	}
}

func TestRootFailuresKeepOriginalLookupUnknown(t *testing.T) {
	absentConfig := filepath.Join(t.TempDir(), "absent.json")
	for _, args := range [][]string{
		{"--json", "operation", "read", testCard, "--evidence", "missing"},
		{"--config", "relative", "--json", "operation", "read", testCard, "--evidence", "missing"},
		{"--config", absentConfig, "--json", "operation", "read", testCard, "--evidence", "missing"},
	} {
		var out, diagnostic bytes.Buffer
		if got := run(context.Background(), args, nil, &out, &diagnostic); got != 4 {
			t.Fatal("local entry failure downgraded lookup uncertainty")
		}
		if strings.Contains(out.String(), `C:\`) || strings.Contains(diagnostic.String(), `C:\`) {
			t.Fatal("private path escaped entry failure")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if run(ctx, []string{"setup", "init"}, nil, io.Discard, io.Discard) != 3 {
		t.Fatal("canceled invocation reached setup")
	}
}
