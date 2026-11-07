package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func validNoticePair(target, delivery string) bool {
	return v4(target) && A.Validate(A.Digest(delivery)) == nil
}

func noticePath(selected Selectors, action, method string) string {
	path := "/api/v1/boards/" + selected.BoardID + "/notices/" + action
	if method == http.MethodGet {
		path += "?incarnation=" + url.QueryEscape(selected.Incarnation)
	}
	return path
}

func runNotices(ctx context.Context, c *Connection, q invocation, action string) result {
	var request any
	incarnation := A.UUID(q.selected.Incarnation)
	if q.method == http.MethodPost {
		status := requestNotices(ctx, c, q, "status", http.MethodGet, noticePath(q.selected, "status", http.MethodGet), nil)
		if status.exit != 0 {
			return status
		}
		view := status.value.(T.NoticeResponse).Result
		if action == "lookup" {
			request = T.NoticeLookupRequest{Incarnation: incarnation, TargetID: T.UUIDv4(q.noticeTarget), DeliveryID: A.Digest(q.noticeDelivery), ExpectedControlGeneration: view.ControlGeneration, ExpectedConfigurationRevision: view.ConfigurationRevision}
		} else if action == "retarget" {
			request = T.NoticeRetargetRequest{Incarnation: incarnation, TargetID: T.UUIDv4(q.noticeTarget), ExpectedControlGeneration: view.ControlGeneration, ExpectedConfigurationRevision: view.ConfigurationRevision}
		} else {
			request = T.NoticeControlRequest{Incarnation: incarnation, ExpectedControlGeneration: view.ControlGeneration, ExpectedConfigurationRevision: view.ConfigurationRevision}
		}
	}
	var body io.Reader
	if request != nil {
		wire, err := T.Readable(request)
		if err != nil {
			return refused("INVOCATION_OR_IO", 1, nil)
		}
		body = bytes.NewReader(wire)
	}
	return requestNotices(ctx, c, q, action, q.method, q.path, body)
}

func requestNotices(ctx context.Context, c *Connection, q invocation, action, method, path string, body io.Reader) result {
	unconfirmed := refused("UNAVAILABLE", 3, nil)
	if method == http.MethodPost {
		unconfirmed = result{noticeOutcomeUnknown{
			APIVersion: 1, Outcome: "unknown",
			Error: T.NoticeUnknownError{Code: "OUTCOME_UNKNOWN", Message: T.NoticeUnknownMessage},
		}, 4}
	}
	status, wire, err := c.requestJSON(ctx, method, path, body, T.NoticeResponseLimit)
	if err != nil {
		return unconfirmed
	}
	if status != http.StatusOK {
		if method == http.MethodPost && status == http.StatusServiceUnavailable {
			if unknown, err := T.Decode[T.NoticeOutcomeUnknown](wire); err == nil {
				return result{unknown, 4}
			}
			// Only a strict refusal envelope proves pre-admission non-effect.
			// A malformed, lost or unknown result must never become a refusal.
			if refusal, err := T.Decode[T.NonterminalRefusal](wire); err == nil {
				return refused(refusal.Error.Code, 2, nil)
			}
			return unconfirmed
		}
		failure := classifyReadFailure(status, wire, nil)
		if failure.exit != 2 {
			return unconfirmed
		}
		return refused(diagnostic(failure.value), failure.exit, nil)
	}
	response, err := T.Decode[T.NoticeResponse](wire)
	v := response.Result
	if err != nil || string(v.Identity.BoardID) != q.selected.BoardID ||
		string(v.Identity.Incarnation) != q.selected.Incarnation ||
		(action != "inbox" && len(v.Entries) != 0) || (action == "lookup") != (v.Lookup != nil) {
		return unconfirmed
	}
	if v.Lookup != nil && v.Lookup.Receipt != nil {
		p := v.Lookup.Receipt
		if string(p.TargetID) != q.noticeTarget || string(p.DeliveryID) != q.noticeDelivery {
			return unconfirmed
		}
	}
	return result{response, 0}
}

// A notice POST has no D03 operation ID. Report uncertainty without fabricating
// operation evidence or claiming that the service refused the request.
type noticeOutcomeUnknown = T.NoticeOutcomeUnknown

func writeNotices(out *strings.Builder, v T.NoticeView) {
	fmt.Fprintf(out, "Notices for board/incarnation %s\nState: %s\nControl generation: %s\n", originText(v.Identity), v.State, v.ControlGeneration)
	if v.Reason != nil {
		fmt.Fprintf(out, "Reason: %s\n", *v.Reason)
	}
	fmt.Fprintf(out, "Configuration revision: %d\n", v.ConfigurationRevision)
	if v.ControlOperation != nil {
		fmt.Fprintf(out, "Incomplete control: %s; read notices status for eventual actual state.\n", *v.ControlOperation)
	}
	if v.TargetID == nil {
		out.WriteString("Future target: unavailable\n")
	} else {
		fmt.Fprintf(out, "Future target: %s\nCheckpoint: %d\n", *v.TargetID, *v.Checkpoint)
	}
	fmt.Fprintf(out, "Gap count: %d\n", v.GapCount)
	if v.Last != nil {
		fmt.Fprintf(out, "Last original target: %s\nLast: %s\n", v.Last.TargetID, noticeCutText(v.Last.Class, v.Last.LowerEvent, v.Last.UpperEvent, v.Last.EventCount))
		if v.Last.DeliveryID != nil {
			fmt.Fprintf(out, "Last delivery: %s\n", *v.Last.DeliveryID)
		}
	}
	if v.Pending != nil {
		fmt.Fprintf(out, "Pending original target: %s\nPending: %s\n", v.Pending.TargetID, noticeCutText(v.Pending.Class, v.Pending.LowerEvent, v.Pending.UpperEvent, v.Pending.EventCount))
		if v.Pending.DeliveryID != nil {
			fmt.Fprintf(out, "Pending delivery: %s\n", *v.Pending.DeliveryID)
		}
	}
	if v.Lookup != nil {
		fmt.Fprintf(out, "Lookup: %s\n", v.Lookup.State)
		if v.Lookup.Receipt != nil {
			writeNoticePair(out, *v.Lookup.Receipt)
		}
	}
	for _, p := range v.Entries {
		writeNoticePair(out, p)
	}
}

func writeNoticePair(out *strings.Builder, p T.NoticePair) {
	fmt.Fprintf(out, "  Target %s, delivery %s, accepted %s\n  %s\n", p.TargetID, p.DeliveryID, p.AcceptedAt, noticeCutText(p.Class, p.LowerEvent, p.UpperEvent, p.EventCount))
}

func noticeCutText(class T.NoticeClass, lower A.Number, upper, count *A.Number) string {
	return fmt.Sprintf("%s, events (%d, %s], count %s", class, lower, noticeNumber(upper), noticeNumber(count))
}

func noticeNumber(n *A.Number) string {
	if n == nil {
		return "unavailable"
	}
	return strconv.FormatUint(uint64(*n), 10)
}
