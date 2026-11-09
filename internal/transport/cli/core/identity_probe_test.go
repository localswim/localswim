package core

import (
	"bytes"
	"io"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestIdentityProbeGrammarDoesNotAcquireAuthority(t *testing.T) {
	for _, tail := range [][]string{
		{"tool", "stop"},
		{"tool", "stop", "--probe-generation", testGrant, "--probe-generation", testGrant},
		{"tool", "stop", "--probe-generation", "f0000000-0000-7000-8000-000000000000"},
		{"tool", "status", "--probe-generation", testGrant},
		{"tool", "status", "operation", "read"},
		{"tool", "probe-version", "--path", "private"},
		{"tool", "probe-version", "--input", "-"},
		{"tool", "probe-version", "--native-owner"},
	} {
		p := &provider{}
		var stderr bytes.Buffer
		if got := (Runner{Provider: p}).Run(t.Context(), args(append([]string{"recipient"}, tail...)...), inspectionUnreadInput{}, io.Discard, &stderr); got != 1 || p.opened != 0 || stderr.String() != "INVOCATION_OR_IO\n" {
			t.Fatal("invalid identity command reached authority or returned a nonlocal outcome")
		}
	}
	q, code, exit := parse(args("recipient", "tool", "stop", "--probe-generation", testGrant))
	if code != "" || exit != 0 || q.kind != probeStopKind || q.probeGeneration != testGrant || q.path != recipientPath(selected, "tool/stop") {
		t.Fatal("Stop must carry its explicit generation")
	}
}

func TestIdentityProbeOutputFailurePreservesPostUncertainty(t *testing.T) {
	identity := recipientTestView().Identity
	version := T.ProbeToolVersion("1.2.3")
	probe := T.ProbeResult{Version: 1, Identity: identity, RuntimeGeneration: testGrant, ConfigurationGeneration: testBoard, ProbeGeneration: testActor, Kind: "identity", State: "supported", Code: "OK", ToolVersion: &version, Joined: true}
	for _, jsonMode := range []bool{true, false} {
		for _, row := range []struct {
			result     result
			want       int
			diagnostic string
		}{
			{result{probe, 0}, 4, "OUTCOME_UNKNOWN\n"},
			{probeUnknown(), 4, "OUTCOME_UNKNOWN\n"},
			{probeRefused("FORBIDDEN", 2), 2, "FORBIDDEN\n"},
			{probeRefused("UNAVAILABLE", 3), 1, "INVOCATION_OR_IO\n"},
		} {
			var stderr bytes.Buffer
			if exit := writeIdentityProbe(row.result, invocation{kind: probeVersionKind, json: jsonMode}, nil, &stderr); exit != row.want || stderr.String() != row.diagnostic {
				t.Fatal("failed output changed probe outcome semantics")
			}
		}
	}
}
