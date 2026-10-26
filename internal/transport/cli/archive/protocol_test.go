package archive

import (
	"bytes"
	"context"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/core"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"io"
	"net/http"
	"strings"
	"testing"
)

const testBoard = "00000000-0000-4000-8000-000000000001"
const testInc = "00000000-0000-4000-8000-000000000002"
const testActor = "00000000-0000-4000-8000-00000000000a"
const testCard = "00000000-0000-4000-8000-000000000010"

var testContext = evidence.Context{InstallationID: "10000000-0000-4000-8000-000000000001", Origin: "https://fixture.localhost:47443"}

func TestInvocationSelectorsAndOfflineSeparation(t *testing.T) {
	prefix := []string{"--json", "--installation", testContext.InstallationID, "--board", testBoard, "--incarnation", testInc, "--grant", "00000000-0000-4000-8000-000000000050"}
	for _, command := range [][]string{{"attachment", "add", testCard, "--input", "-", "--file", "f"}, {"attachment", "download", testCard, "--destination", "f"}, {"export", "create", "--destination", "f"}} {
		if _, code, _ := parse(append(append([]string{}, prefix...), command...)); code != "" {
			t.Fatal("valid grammar refused")
		}
	}
	for _, args := range [][]string{{"attachment", "add", testCard, "--input", "-", "--file", "f"}, {"--board", testBoard, "restore", "create", "--source", "f", "--destination", "g"}, {"restore", "status", "--destination", "g", "--request-id", "bad"}, append(append([]string{}, prefix...), "export", "create", "--destination", "f", "--destination", "g")} {
		if _, code, _ := parse(args); code == "" {
			t.Fatal("invalid grammar accepted")
		}
	}
}

type unavailableProvider struct{ opened int }

func (p *unavailableProvider) Open(context.Context, core.Selectors) (*core.Connection, error) {
	p.opened++
	return nil, &core.Failure{Code: "CREDENTIAL_UNAVAILABLE", Exit: 3}
}
func (*unavailableProvider) NativeOwner(context.Context, evidence.Record) (*core.Connection, error) {
	panic("native fallback forbidden")
}

func TestInvocationOrMissingConfigurationRefusesBeforeProvider(t *testing.T) {
	p := &unavailableProvider{}
	r := Runner{Provider: p}
	prefix := []string{"--json", "--installation", testContext.InstallationID, "--board", testBoard, "--incarnation", testInc, "--grant", "00000000-0000-4000-8000-000000000050"}
	for _, args := range [][]string{append(append([]string{}, prefix...), "export", "create", "--destination", "PRIVATE_PATH"), {"--json", "--board", "unsafe\nPRIVATE_PATH", "export", "create", "--destination", "x"}, {"--json", "restore", "status", "--destination", "x", "--request-id", "bad"}} {
		var out bytes.Buffer
		exit := r.Run(context.Background(), args, nil, &out, io.Discard)
		if exit != 1 {
			t.Fatal("local failure misclassified")
		}
		if _, e := T.Decode[T.NonterminalRefusal](out.Bytes()); e != nil {
			t.Fatal("JSON refusal invalid")
		}
		if bytes.Contains(out.Bytes(), []byte("PRIVATE_PATH")) || strings.Count(out.String(), "\n") < 1 || !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			t.Fatal("unsafe or incomplete output")
		}
	}
	if p.opened != 0 {
		t.Fatal("provider used before valid invocation/configuration")
	}
}

func TestBinaryCompletionHeadersRefuseAmbiguity(t *testing.T) {
	valid := http.Header{"Content-Type": []string{"application/octet-stream"}, "Content-Disposition": []string{`attachment; filename="inert.bin"`}, "Content-Length": []string{"3"}}
	if !binaryHeaders(valid, "application/octet-stream", []byte{0, 1, 2}, true) {
		t.Fatal("valid raw delivery headers refused")
	}
	for _, change := range []func(http.Header){func(h http.Header) { h.Add("Content-Type", "application/octet-stream") }, func(h http.Header) { h.Set("Content-Type", "text/html") }, func(h http.Header) { h.Set("Content-Encoding", "gzip") }, func(h http.Header) { h.Set("Content-Length", "4") }, func(h http.Header) { h.Set("Content-Disposition", "inline") }, func(h http.Header) { h.Del("Content-Disposition") }} {
		h := valid.Clone()
		change(h)
		if binaryHeaders(h, "application/octet-stream", []byte{0, 1, 2}, true) {
			t.Fatal("ambiguous or incomplete delivery accepted")
		}
	}
}
