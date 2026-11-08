package installation

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

func validVersion(s string) bool {
	if len(s) > 96 || !versionRE.MatchString(s) {
		return false
	}
	base := strings.SplitN(s, "+", 2)[0]
	_, pre, ok := strings.Cut(base, "-")
	if ok {
		for _, part := range strings.Split(pre, ".") {
			numeric := true
			for _, c := range part {
				numeric = numeric && c >= '0' && c <= '9'
			}
			if numeric && len(part) > 1 && part[0] == '0' {
				return false
			}
		}
	}
	return true
}

// parseIdentity consumes only complete private capture after actual native join.
// The manifest is comparison evidence, never a source for a claimed version.
func parseIdentity(stdout, stderr []byte, manifest string) ProbeObservation {
	if !utf8.Valid(stdout) || !utf8.Valid(stderr) {
		return ProbeObservation{"unknown", "PARSE_ERROR", nil, true}
	}
	if len(stderr) != 0 {
		return ProbeObservation{"unsupported", "PARSE_UNSUPPORTED", nil, true}
	}
	line := stdout
	if bytes.HasSuffix(line, []byte("\r\n")) {
		line = line[:len(line)-2]
	} else if bytes.HasSuffix(line, []byte("\n")) {
		line = line[:len(line)-1]
	}
	const prefix = "codex-cli "
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return ProbeObservation{"unsupported", "PARSE_UNSUPPORTED", nil, true}
	}
	version := string(line[len(prefix):])
	if !validVersion(version) || version != manifest {
		return ProbeObservation{"unsupported", "PARSE_UNSUPPORTED", nil, true}
	}
	return ProbeObservation{"supported", "OK", &version, true}
}

func nativeFailure(code string, joined bool) ProbeObservation {
	if !joined {
		return ProbeObservation{"incomplete", "DRAIN_INCOMPLETE", nil, false}
	}
	switch code {
	case "TOOL_MISSING", "PLATFORM_UNQUALIFIED":
		return ProbeObservation{"unavailable", code, nil, true}
	case "TOOL_UNSUPPORTED", "PARSE_UNSUPPORTED":
		return ProbeObservation{"unsupported", code, nil, true}
	case "OUTPUT_LIMIT", "TIMED_OUT", "CANCELLED", "PARSE_ERROR":
		return ProbeObservation{"unknown", code, nil, true}
	default:
		return ProbeObservation{"unknown", "EXIT_FAILED", nil, true}
	}
}

func (o ProbeObservation) Valid() bool {
	if !o.Joined {
		return o.State == "incomplete" && o.Code == "DRAIN_INCOMPLETE" && o.ToolVersion == nil
	}
	if o.State == "supported" {
		return o.Code == "OK" && o.ToolVersion != nil && validVersion(*o.ToolVersion)
	}
	if o.ToolVersion != nil {
		return false
	}
	switch o.State {
	case "unavailable":
		return o.Code == "TOOL_MISSING" || o.Code == "PLATFORM_UNQUALIFIED"
	case "unsupported":
		return o.Code == "TOOL_UNSUPPORTED" || o.Code == "PARSE_UNSUPPORTED"
	case "unknown":
		switch o.Code {
		case "EXIT_FAILED", "PARSE_ERROR", "OUTPUT_LIMIT", "TIMED_OUT", "CANCELLED":
			return true
		}
	}
	return false
}

func preparationObservation(ctx context.Context, o Observation) (ProbeObservation, error) {
	if ctx.Err() != nil {
		return cancellationObservation(ctx), nil
	}
	switch o.Code {
	case "TOOL_MISSING", "PLATFORM_UNQUALIFIED":
		return nativeFailure(o.Code, true), nil
	case "SOURCE_UNQUALIFIED", "PATH_UNQUALIFIED", "PUBLISHER_UNQUALIFIED":
		return nativeFailure("TOOL_UNSUPPORTED", true), nil
	case "FILE_CHANGED":
		return ProbeObservation{}, ErrIdentityChanged
	default:
		return nativeFailure("EXIT_FAILED", true), nil
	}
}
func cancellationObservation(ctx context.Context) ProbeObservation {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nativeFailure("TIMED_OUT", true)
	}
	return nativeFailure("CANCELLED", true)
}
