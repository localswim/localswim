package installation

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestIdentityExactGrammarAndManifestAgreement(t *testing.T) {
	versions := []string{"1.2.3", "1.2.3-alpha.0+build.001", "1.2.3-" + strings.Repeat("a", 90)}
	for _, version := range versions {
		for _, ending := range []string{"", "\n", "\r\n"} {
			out := parseIdentity([]byte("codex-cli "+version+ending), nil, version)
			if out.Code != "OK" || out.ToolVersion == nil || *out.ToolVersion != version || !out.Joined {
				t.Fatal(version, ending, out)
			}
		}
	}
	cases := []struct{ stdout, stderr, manifest string }{
		{"codex-cli 01.2.3\n", "", "01.2.3"}, {"codex-cli 1.2.3-01\n", "", "1.2.3-01"}, {"codex-cli 1.2.3\nextra\n", "", "1.2.3"},
		{"codex-cli 1.2.3 \n", "", "1.2.3"}, {"codex-cli 1.2.3\n", "warning\n", "1.2.3"}, {"codex-cli 1.2.4\n", "", "1.2.3"},
		{"Codex-cli 1.2.3", "", "1.2.3"}, {"codex-cli 1.2.3\n\n", "", "1.2.3"}, {"codex-cli 1.2.3-" + strings.Repeat("a", 91), "", "1.2.3"},
		{"codex-cli 1.2.3\r", "", "1.2.3"}, {"codex-cli 1.2.3\x00", "", "1.2.3"}, {"\xef\xbb\xbfcodex-cli 1.2.3", "", "1.2.3"},
	}
	for _, tc := range cases {
		out := parseIdentity([]byte(tc.stdout), []byte(tc.stderr), tc.manifest)
		if out.Code != "PARSE_UNSUPPORTED" || out.ToolVersion != nil {
			t.Fatal(tc, out)
		}
	}
	for _, parts := range [][2][]byte{{{0xc3}, nil}, {[]byte("codex-cli 1.2.3"), {0xff}}} {
		out := parseIdentity(parts[0], parts[1], "1.2.3")
		if out.Code != "PARSE_ERROR" || out.ToolVersion != nil {
			t.Fatal(out)
		}
	}
}
func TestNativeFailurePrecedenceAndFiniteMapping(t *testing.T) {
	for _, code := range []string{"OK", "CREATE_FAILED", "EXIT_UNKNOWN", "EXIT_FAILED", "TOOL_UNSUPPORTED", "OUTPUT_LIMIT", "TIMED_OUT", "CANCELLED", "PARSE_ERROR", "TOOL_MISSING", "PLATFORM_UNQUALIFIED"} {
		out := nativeFailure(code, false)
		if out.Code != "DRAIN_INCOMPLETE" || out.Joined || out.ToolVersion != nil {
			t.Fatal(code, out)
		}
	}
	for _, code := range []string{"CREATE_FAILED", "EXIT_UNKNOWN", "INSPECTION_UNKNOWN", "unexpected-private-detail"} {
		out := nativeFailure(code, true)
		if out.Code != "EXIT_FAILED" || !out.Joined || out.ToolVersion != nil {
			t.Fatal(code, out)
		}
	}
}

func TestPreparationUnknownAndDefinitivePolicyMappings(t *testing.T) {
	for _, code := range []string{"INSPECTION_UNKNOWN", "unexpected"} {
		out, err := preparationObservation(context.Background(), Unqualified(code))
		if err != nil || out.State != "unknown" || out.Code != "EXIT_FAILED" || !out.Joined {
			t.Fatal(out, err)
		}
	}
	for _, code := range []string{"SOURCE_UNQUALIFIED", "PATH_UNQUALIFIED", "PUBLISHER_UNQUALIFIED"} {
		out, err := preparationObservation(context.Background(), Unqualified(code))
		if err != nil || out.Code != "TOOL_UNSUPPORTED" || !out.Joined {
			t.Fatal(out, err)
		}
	}
	out, err := preparationObservation(context.Background(), Observation{"missing", "TOOL_MISSING"})
	if err != nil || out.Code != "TOOL_MISSING" || !out.Joined {
		t.Fatal(out, err)
	}
	if _, err = preparationObservation(context.Background(), Unqualified("FILE_CHANGED")); err != ErrIdentityChanged {
		t.Fatal(err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	out, err = preparationObservation(ctx, Unqualified("INSPECTION_UNKNOWN"))
	if err != nil || out.Code != "TIMED_OUT" {
		t.Fatal(out, err)
	}
}
