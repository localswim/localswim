package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

const (
	probeStatusKind  = "recipient tool status"
	probeVersionKind = "recipient tool probe-version"
	probeStopKind    = "recipient tool stop"
)

func isIdentityProbe(kind string) bool {
	return kind == probeStatusKind || kind == probeVersionKind || kind == probeStopKind
}

func probeRefused(code string, exit int) result {
	return result{T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: code, Message: "Request refused"}}, exit}
}

func probeUnknown() result {
	return result{T.ProbeOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: T.ProbeUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.ProbeUnknownMessage}}, 4}
}

func runIdentityProbe(ctx context.Context, c *Connection, q invocation) result {
	unavailable := probeRefused("UNAVAILABLE", 3)
	identity := A.Identity{BoardID: A.UUID(q.selected.BoardID), Incarnation: A.UUID(q.selected.Incarnation)}
	wire, failure := identityProbeRequest(ctx, c, http.MethodGet, recipientPath(q.selected, "status"), nil, T.RecipientResponseLimit)
	if failure != nil {
		return *failure
	}
	status, err := T.Decode[T.RecipientResponse](wire)
	v := status.Result
	if err != nil || v.Identity != identity || v.State != "configured" || v.ConfigurationGeneration == nil {
		return unavailable
	}
	runtime, configuration := v.RuntimeGeneration, *v.ConfigurationGeneration

	generation := q.probeGeneration
	if q.kind != probeStopKind {
		query := url.Values{"incarnation": {q.selected.Incarnation}, "expectedRuntimeGeneration": {string(runtime)}, "expectedConfigurationGeneration": {string(configuration)}}
		wire, failure = identityProbeRequest(ctx, c, http.MethodGet, recipientPath(q.selected, "tool/probe-control")+"?"+query.Encode(), nil, T.ProbeResponseLimit)
		if failure != nil {
			return *failure
		}
		control, err := T.Decode[T.ProbeControlResponse](wire)
		if err != nil || T.ValidateProbeControlContext(control, identity, runtime, configuration) != nil {
			return unavailable
		}
		if q.kind == probeStatusKind {
			return result{control, 0}
		}
		if control.Result.Phase != "ready" {
			return probeRefused("CONTROL_BUSY", 2)
		}
		generation = control.Result.ProbeGeneration
	}

	// Freeze one request after successful preflight. No retry, fallback or status
	// read follows a submitted POST, regardless of its response.
	body, err := T.Readable(T.ProbeRequest{Incarnation: identity.Incarnation, ExpectedRuntimeGeneration: runtime, ExpectedConfigurationGeneration: configuration, ProbeGeneration: generation})
	if err != nil {
		return probeRefused("INVOCATION_OR_IO", 1)
	}
	if ctx.Err() != nil {
		return unavailable
	}
	wire, failure = identityProbeRequest(ctx, c, http.MethodPost, q.path, bytes.NewReader(body), T.ProbeResponseLimit)
	if failure != nil {
		return *failure
	}
	if q.kind == probeStopKind {
		stop, err := T.Decode[T.ProbeStopResponse](wire)
		if err != nil || T.ValidateProbeStopContext(stop, identity, runtime, configuration, generation) != nil {
			return probeUnknown()
		}
		if stop.Result.Joined {
			return result{stop, 0}
		}
		return result{stop, 4}
	}
	probe, err := T.Decode[T.ProbeResult](wire)
	if err != nil || T.ValidateProbeResultContext(probe, identity, runtime, configuration, generation) != nil {
		return probeUnknown()
	}
	switch probe.State {
	case "supported":
		return result{probe, 0}
	case "unsupported", "unavailable":
		return result{probe, 3}
	default:
		return result{probe, 4}
	}
}

func identityProbeRequest(ctx context.Context, c *Connection, method, path string, body io.Reader, limit int64) ([]byte, *result) {
	unconfirmed := probeRefused("UNAVAILABLE", 3)
	contentType := ""
	if method == http.MethodPost {
		unconfirmed = probeUnknown()
		contentType = "application/json"
	}
	status, headers, wire, err := c.requestRawWithFraming(ctx, method, path, body, contentType, "application/json", limit, true)
	media, params, mediaErr := mime.ParseMediaType(headers.Get("Content-Type"))
	charset, hasCharset := params["charset"]
	if err != nil || mediaErr != nil || media != "application/json" || len(headers.Values("Content-Type")) != 1 || len(headers.Values("Cache-Control")) != 1 || headers.Get("Cache-Control") != "no-store" ||
		len(params) != 0 && !(len(params) == 1 && hasCharset && strings.EqualFold(charset, "utf-8")) {
		return nil, &unconfirmed
	}
	if status == http.StatusOK {
		return wire, nil
	}
	if method == http.MethodPost && status == http.StatusServiceUnavailable {
		return nil, &unconfirmed
	}
	refusal, err := T.Decode[T.NonterminalRefusal](wire)
	// The general refusal type allows optional opId. This endpoint never does,
	// including an explicit null opId. Decode above also rejects extra fields.
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(wire, &fields) != nil || len(fields) != 4 || refusal.OpID != nil || refusal.Conflict != nil || refusal.Error.Field != nil || refusal.Error.Message != "Request refused" {
		return nil, &unconfirmed
	}
	if _, present := fields["opId"]; present {
		return nil, &unconfirmed
	}
	switch fmt.Sprintf("%d/%s", status, refusal.Error.Code) {
	case "400/INVALID_INPUT", "401/UNAUTHENTICATED", "403/FORBIDDEN", "404/NOT_FOUND", "405/INVALID_INPUT", "409/CONTROL_STALE", "409/CONTROL_BUSY", "409/IDENTITY_CHANGED", "503/UNAVAILABLE":
		known := result{refusal, 2}
		return nil, &known
	default:
		return nil, &unconfirmed
	}
}

func writeIdentityProbe(out result, q invocation, stdout, stderr io.Writer) int {
	var text []byte
	var err error
	if q.json {
		text, err = T.Readable(out.value)
	} else {
		var readable strings.Builder
		switch v := out.value.(type) {
		case T.ProbeControlResponse:
			fmt.Fprintf(&readable, "Identity check control: %s\nProbe generation: %s\n", v.Result.Phase, v.Result.ProbeGeneration)
			readable.WriteString("Current control status only; no version result.\n")
		case T.ProbeResult:
			switch v.State {
			case "supported":
				fmt.Fprintf(&readable, "Installed version: %s\n", *v.ToolVersion)
			case "unavailable":
				readable.WriteString("Installed version unavailable.\n")
			case "unsupported":
				readable.WriteString("Installed version check unsupported.\n")
			case "unknown":
				readable.WriteString("Installed version check could not complete.\n")
			case "incomplete":
				readable.WriteString("Installed version check is still stopping.\n")
			}
			fmt.Fprintf(&readable, "Code: %s\n", v.Code)
		case T.ProbeStopResponse:
			if v.Result.Joined {
				readable.WriteString("Identity check cleanup joined; no version result.\n")
			} else {
				readable.WriteString("Identity check cleanup incomplete.\n")
			}
		case T.ProbeOutcomeUnknown:
			fmt.Fprintln(&readable, T.ProbeUnknownMessage)
		case T.NonterminalRefusal:
			if out.exit == 2 {
				fmt.Fprintf(&readable, "Identity check request refused: %s.\n", v.Error.Code)
			} else {
				fmt.Fprintf(&readable, "Identity check unavailable: %s.\n", v.Error.Code)
			}
		}
		text = []byte(readable.String())
	}
	if err == nil && stdout != nil {
		n, writeErr := stdout.Write(text)
		if writeErr == nil && n == len(text) {
			if stderr != nil && out.exit != 0 {
				fmt.Fprintln(stderr, identityProbeDiagnostic(out.value))
			}
			return out.exit
		}
	}
	// A failed output after a validated result cannot establish the delivered
	// outcome. A validated refusal remains a refusal even if output failed.
	switch out.value.(type) {
	case T.ProbeResult, T.ProbeStopResponse, T.ProbeOutcomeUnknown:
		if stderr != nil {
			fmt.Fprintln(stderr, "OUTCOME_UNKNOWN")
		}
		return 4
	default:
		if out.exit == 2 {
			if stderr != nil {
				fmt.Fprintln(stderr, identityProbeDiagnostic(out.value))
			}
			return 2
		}
		if stderr != nil {
			fmt.Fprintln(stderr, "INVOCATION_OR_IO")
		}
		return 1
	}
}

func identityProbeDiagnostic(v any) string {
	switch v := v.(type) {
	case T.ProbeResult:
		return string(v.Code)
	case T.ProbeStopResponse:
		return "DRAIN_INCOMPLETE"
	case T.ProbeOutcomeUnknown:
		return v.Error.Code
	default:
		return diagnostic(v)
	}
}
