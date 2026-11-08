package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

func recipientPath(selected Selectors, action string) string {
	path := "/api/v1/boards/" + selected.BoardID + "/recipient/" + action
	if action == "status" {
		path += "?incarnation=" + url.QueryEscape(selected.Incarnation)
	}
	return path
}
func recipientUnknown() result {
	return result{T.RecipientOutcomeUnknown{APIVersion: 1, Outcome: "unknown", Error: T.RecipientUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.RecipientUnknownMessage}}, 4}
}
func recipientRequestID() (T.UUIDv4, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return T.UUIDv4(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])), nil
}
func runRecipient(ctx context.Context, c *Connection, q invocation, input *T.RecipientInput) result {
	if q.kind == installationInspectKind {
		return runInstallationInspect(ctx, c, q)
	}
	status := requestRecipient(ctx, c, q, nil)
	if q.kind == "recipient status" || status.exit != 0 {
		return status
	}
	view := status.value.(T.RecipientResponse).Result
	if view.State == "configured" {
		return refused("CONFIGURATION_FIXED", 2, nil)
	}
	if view.State != "unconfigured" {
		return refused("UNAVAILABLE", 3, nil)
	}
	if input == nil {
		return refused("INVALID_INPUT", 2, nil)
	}
	id, e := recipientRequestID()
	if e != nil {
		return refused("UNAVAILABLE", 3, nil)
	}
	// Freeze one invocation-local original. It is never regenerated or replayed.
	original := T.RecipientConfigureRequest{Incarnation: A.UUID(q.selected.Incarnation), ExpectedRuntimeGeneration: view.RuntimeGeneration, ExpectedConfigurationRevision: 0, RequestID: id, Input: *input}
	return requestRecipient(ctx, c, q, &original)
}
func requestRecipient(ctx context.Context, c *Connection, q invocation, original *T.RecipientConfigureRequest) result {
	method, path := http.MethodGet, recipientPath(q.selected, "status")
	unconfirmed := refused("UNAVAILABLE", 3, nil)
	var body *bytes.Reader
	if original != nil {
		wire, e := T.Readable(*original)
		if e != nil {
			return refused("INVALID_INPUT", 2, nil)
		}
		body = bytes.NewReader(wire)
		method, path = http.MethodPost, recipientPath(q.selected, "configure")
		unconfirmed = recipientUnknown()
	}
	var reader io.Reader
	if body != nil {
		reader = body
	}
	status, headers, wire, e := c.requestRaw(ctx, method, path, reader, "application/json", "application/json", T.RecipientResponseLimit)
	media, _, mediaErr := mime.ParseMediaType(headers.Get("Content-Type"))
	if mediaErr != nil || media != "application/json" || headers.Get("Cache-Control") != "no-store" {
		return unconfirmed
	}
	if e != nil {
		return unconfirmed
	}
	if status != 200 {
		refusal, err := T.Decode[T.NonterminalRefusal](wire)
		if err == nil && refusal.OpID == nil && refusal.Conflict == nil && refusal.Error.Field == nil && refusal.Error.Message == "Request refused" {
			pair := fmt.Sprintf("%d/%s", status, refusal.Error.Code)
			switch pair {
			case "400/INVALID_INPUT", "401/UNAUTHENTICATED", "403/FORBIDDEN", "409/CONTROL_STALE", "409/CONTROL_BUSY", "409/CONFIGURATION_FIXED":
				return refused(refusal.Error.Code, 2, nil)
			}
		}
		return unconfirmed
	}
	response, err := T.Decode[T.RecipientResponse](wire)
	v := response.Result
	if err != nil || string(v.Identity.BoardID) != q.selected.BoardID || string(v.Identity.Incarnation) != q.selected.Incarnation {
		return unconfirmed
	}
	if original != nil && (v.State != "configured" || v.ConfigurationRevision != 1 || v.ConfigurationGeneration == nil || v.RequestID == nil || v.Input == nil || v.RuntimeGeneration != original.ExpectedRuntimeGeneration || *v.RequestID != original.RequestID || *v.Input != original.Input) {
		return unconfirmed
	}
	return result{response, 0}
}
func writeRecipient(out *strings.Builder, v T.RecipientView) {
	fmt.Fprintf(out, "Recipient policy for board/incarnation %s\nState: %s\nConfiguration revision: %d\n", originText(v.Identity), v.State, v.ConfigurationRevision)
	if v.Reason != nil {
		fmt.Fprintf(out, "Reason: %s\n", *v.Reason)
	}
	if v.State == "configured" && v.Input != nil && v.ConfigurationGeneration != nil {
		fmt.Fprintf(out, "Saved; delivery is not activated\nRecipient: %s %s\nPolicy: %s\nConfiguration generation: %s\n", v.Input.Recipient.Kind, v.Input.Recipient.ThreadID, v.Input.Policy, *v.ConfigurationGeneration)
	} else {
		out.WriteString("Status is a current observation; it does not settle a lost original request.\n")
	}
}
