package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

// boardAdvisory projects a complete, validated current read. It confers no
// authority: the server checks current state again on a deliberate start.
func boardAdvisory(b T.BoardView) string {
	cards := make(map[A.UUID]A.Card, len(b.Cards))
	actors := make(map[A.UUID]A.Actor, len(b.Actors))
	approvals := make(map[A.UUID]A.Approval, len(b.Approvals))
	for _, c := range b.Cards {
		cards[c.CardID] = c
	}
	for _, a := range b.Actors {
		actors[a.ActorID] = a
	}
	for _, a := range b.Approvals {
		approvals[a.ApprovalID] = a
	}
	current := func(r A.CardRef) bool {
		return r.BoardID == b.Identity.BoardID && r.Incarnation == b.Identity.Incarnation
	}
	satisfied := func(c A.Card) bool {
		if c.Archived || c.Lane != "Completed" || c.CurrentApprovalID == nil || c.Revision < 2 {
			return false
		}
		a, ok := approvals[*c.CurrentApprovalID]
		h, human := actors[a.ActorID]
		return ok && a.CardID == c.CardID && human && h.Kind == "human" && a.PolicyVersion == b.PolicyVersion && a.ReviewedCardRevision == c.Revision-1
	}
	ordered := append([]A.Card(nil), b.Cards...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.Ticket != b.Ticket {
			return a.Ticket < b.Ticket
		}
		return a.CardID < b.CardID
	})
	var out strings.Builder
	out.WriteString("Dependency advisory (current snapshot; assigned priorities unchanged):\n")
	for _, c := range ordered {
		if c.Archived {
			continue
		}
		fmt.Fprintf(&out, "%s\n", advisoryCard(c))
		var pressure, prerequisites []string
		seen := map[A.UUID]bool{}
		allSatisfied := true
		for _, l := range b.Links {
			if !l.Active || l.Kind != "blocking" {
				continue
			}
			if current(l.Target) && l.Target.CardID == c.CardID && !seen[l.CardID] {
				if dependent, ok := cards[l.CardID]; ok {
					pressure = append(pressure, advisoryCard(dependent))
					seen[l.CardID] = true
				}
			}
			if l.CardID != c.CardID {
				continue
			}
			target, ok := cards[l.Target.CardID]
			if !current(l.Target) || !ok {
				prerequisites = append(prerequisites, fmt.Sprintf("target %s/%s/%s — unavailable or foreign target (not satisfied)", l.Target.BoardID, l.Target.Incarnation, l.Target.CardID))
				allSatisfied = false
			} else if satisfied(target) {
				prerequisites = append(prerequisites, advisoryCard(target)+" — satisfied")
			} else {
				prerequisites = append(prerequisites, advisoryCard(target)+" — awaiting human acceptance (not satisfied)")
				allSatisfied = false
			}
		}
		writeAdvisoryRows(&out, "Dependency pressure", pressure)
		writeAdvisoryRows(&out, "Prerequisites/readiness", prerequisites)
		if len(prerequisites) != 0 {
			state := "not satisfied"
			if allSatisfied {
				state = "satisfied"
			}
			fmt.Fprintf(&out, "  Prerequisites %s (advisory; server rechecks at deliberate start).\n", state)
		}
	}
	return out.String()
}

func writeAdvisoryRows(out *strings.Builder, label string, rows []string) {
	if len(rows) == 0 {
		fmt.Fprintf(out, "  %s: none.\n", label)
		return
	}
	sort.Strings(rows)
	for _, row := range rows {
		fmt.Fprintf(out, "  %s: %s\n", label, row)
	}
}

func advisoryCard(c A.Card) string {
	archived := ""
	if c.Archived {
		archived = "; archived"
	}
	return fmt.Sprintf("#%d %s (%s; %s%s)", c.Ticket, advisoryText(c.Title), c.Priority, c.Lane, archived)
}

// Keep accepted prose inert in terminals, including escape and bidi controls.
func advisoryText(s string) string {
	var out strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			out.WriteRune(r)
		} else {
			q := strconv.QuoteRune(r)
			out.WriteString(q[1 : len(q)-1])
		}
	}
	return out.String()
}
