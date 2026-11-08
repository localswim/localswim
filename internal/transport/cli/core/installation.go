package core

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"strings"

	T "github.com/localswim/localswim/internal/contract/transport"
)

const installationInspectKind = "recipient tool inspect"

func installationUnavailable() result {
	return result{T.NonterminalRefusal{APIVersion: 1, Outcome: "refused", Error: T.Error{Code: "UNAVAILABLE", Message: "Request refused"}}, 3}
}

// An inspection has no original operation, persistent evidence or retry. The
// status read obtains only this invocation's runtime generation.
func runInstallationInspect(ctx context.Context, c *Connection, q invocation) result {
	wire, failure := installationRead(ctx, c, recipientPath(q.selected, "status"), T.RecipientResponseLimit)
	if failure != nil {
		return *failure
	}
	status, err := T.Decode[T.RecipientResponse](wire)
	if err != nil || string(status.Result.Identity.BoardID) != q.selected.BoardID || string(status.Result.Identity.Incarnation) != q.selected.Incarnation {
		return installationUnavailable()
	}
	generation := status.Result.RuntimeGeneration
	query := url.Values{"incarnation": {q.selected.Incarnation}, "expectedRuntimeGeneration": {string(generation)}}
	path := recipientPath(q.selected, "tool/inspect") + "?" + query.Encode()
	wire, failure = installationRead(ctx, c, path, T.InstallationInspectionResponseLimit)
	if failure != nil {
		return *failure
	}
	observation, err := T.Decode[T.InstallationInspectionResponse](wire)
	v := observation.Result
	if err != nil || string(v.Identity.BoardID) != q.selected.BoardID || string(v.Identity.Incarnation) != q.selected.Incarnation || v.RuntimeGeneration != generation {
		return installationUnavailable()
	}
	return result{observation, 0}
}

func installationRead(ctx context.Context, c *Connection, path string, limit int64) ([]byte, *result) {
	unavailable := installationUnavailable()
	status, headers, wire, err := c.requestRaw(ctx, http.MethodGet, path, nil, "", "application/json", limit)
	media, params, mediaErr := mime.ParseMediaType(headers.Get("Content-Type"))
	if err != nil || mediaErr != nil || media != "application/json" || len(headers.Values("Content-Type")) != 1 || len(headers.Values("Cache-Control")) != 1 || headers.Get("Cache-Control") != "no-store" || params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		return nil, &unavailable
	}
	if status == http.StatusOK {
		return wire, nil
	}
	refusal, err := T.Decode[T.NonterminalRefusal](wire)
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(wire, &fields) != nil || len(fields) != 4 || refusal.OpID != nil || refusal.Conflict != nil || refusal.Error.Field != nil || refusal.Error.Message != "Request refused" {
		return nil, &unavailable
	}
	if _, present := fields["opId"]; present {
		return nil, &unavailable
	}
	switch fmt.Sprintf("%d/%s", status, refusal.Error.Code) {
	case "400/INVALID_INPUT", "401/UNAUTHENTICATED", "403/FORBIDDEN", "409/CONTROL_STALE", "409/CONTROL_BUSY", "409/IDENTITY_CHANGED", "503/UNAVAILABLE", "405/INVALID_INPUT", "404/NOT_FOUND":
		known := result{refusal, 2}
		return nil, &known
	default:
		return nil, &unavailable
	}
}

func writeInstallationInspect(out *strings.Builder, v T.InstallationInspectionResult) {
	fmt.Fprintf(out, "Installation inspection for board/incarnation %s\n", originText(v.Identity))
	switch v.State {
	case "candidate":
		out.WriteString("Candidate present; no command was run.\n")
	case "missing":
		out.WriteString("No candidate in the supported installation location.\n")
	case "unqualified":
		out.WriteString("Qualification not established.\n")
		if v.Code == "INSPECTION_UNKNOWN" || v.Code == "INSPECTION_TIMED_OUT" {
			out.WriteString("Inspection could not complete.\n")
		} else {
			out.WriteString("This installation was not qualified by the supported inspection policy.\n")
		}
	}
	fmt.Fprintf(out, "Code: %s\n", v.Code)
}
