package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	T "github.com/localswim/localswim/internal/contract/transport"
)

const activityWindow = T.ActivityWindowSeconds * time.Second

func activitySpan(from, through string) time.Duration {
	const utcSecond = "2006-01-02T15:04:05Z"
	f, ferr := time.Parse(utcSecond, from)
	t, terr := time.Parse(utcSecond, through)
	span := t.Sub(f)
	if ferr != nil || terr != nil || f.Year() < 1 || t.Year() < 1 || f.Format(utcSecond) != from || t.Format(utcSecond) != through || span <= 0 || span > activityWindow {
		return 0
	}
	return span
}

func readActivity(ctx context.Context, c *Connection, q invocation) result {
	status, wire, err := c.requestJSON(ctx, q.method, q.path, nil, T.ActivityReadResponseLimit)
	if err != nil {
		return refused("UNAVAILABLE", 3, nil)
	}
	if status != 200 {
		failure := classifyReadFailure(status, wire, nil)
		// Keep the ordinary status/code mapping without reflecting optional
		// server-supplied field names in the activity inspection output.
		return refused(diagnostic(failure.value), failure.exit, nil)
	}
	page, err := T.Decode[T.ActivityRead](wire)
	v := page.Result
	if err != nil || string(v.Identity.BoardID) != q.selected.BoardID || string(v.Identity.Incarnation) != q.selected.Incarnation {
		return refused("UNAVAILABLE", 3, nil)
	}
	if q.from != "" && (string(v.From) != q.from || string(v.Through) != q.through) {
		return refused("UNAVAILABLE", 3, nil)
	}
	if q.from == "" && q.cursor == "" && activitySpan(string(v.From), string(v.Through)) != activityWindow {
		return refused("UNAVAILABLE", 3, nil)
	}
	return result{page, 0}
}

func writeActivity(out *strings.Builder, v T.ActivityView) {
	fmt.Fprintf(out, "Activity for board/incarnation %s\nInterval: (%s, %s]\nBoard revision: %d; local event high-water: %d\n", originText(v.Identity), v.From, v.Through, v.Revision, v.LocalEventUpper)
	switch v.Coverage.Status {
	case "complete":
		out.WriteString("Coverage: complete\n")
	case "gapped":
		out.WriteString("Coverage: gapped (incomplete evidence)\n")
	case "expired":
		out.WriteString("Coverage: expired (the retention floor covers this interval)\n")
	}
	if v.Coverage.ActivityFloor != nil {
		fmt.Fprintf(out, "Activity floor: %s\n", *v.Coverage.ActivityFloor)
	}
	for _, g := range v.Coverage.Gaps {
		from := "unbounded"
		if g.FromExclusive != nil {
			from = string(*g.FromExclusive)
		}
		fmt.Fprintf(out, "  Gap %s %s (%s, %s]\n", originText(g.Origin), g.Reason, from, g.ThroughInclusive)
	}
	if len(v.Events) == 0 {
		if v.Coverage.Status == "complete" {
			out.WriteString("No activity in this interval.\n")
		} else {
			out.WriteString("No retained activity in this interval.\n")
		}
	}
	for _, event := range v.Events {
		actor := "unrecorded"
		if event.ActorID != nil {
			actor = string(*event.ActorID)
		}
		fmt.Fprintf(out, "  %s/%d %s at %s; board revision %d; actor %s (%s)\n", originText(event.Origin), event.EventID, event.Type, event.At, event.BoardRevision, event.ActorKind, actor)
	}
	if v.NextCursor != nil {
		fmt.Fprintf(out, "Next cursor: %s\n", *v.NextCursor)
	} else {
		out.WriteString("End of retained activity.\n")
	}
}
