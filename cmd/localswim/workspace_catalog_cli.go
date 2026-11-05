package main

import (
	"fmt"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"strconv"
)

func writeWorkspaceCatalog(out io.Writer, v T.WorkspaceCatalog) error {
	if _, err := fmt.Fprintf(out, "Workspace %s (config revision %d)\n", v.WorkspaceID, v.ConfigRevision); err != nil {
		return err
	}
	for _, e := range v.Entries {
		if _, err := fmt.Fprintf(out, "%s/%s\t%s / %s\t%s, %s", e.ProjectKey, e.BoardKey, strconv.QuoteToGraphic(string(e.ProjectLabel)), strconv.QuoteToGraphic(string(e.Label)), e.Availability, e.Freshness); err != nil {
			return err
		}
		if e.AgeSeconds != nil {
			if _, err := fmt.Fprintf(out, " (age %ds)", *e.AgeSeconds); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
		if e.Identity != nil {
			if _, err := fmt.Fprintf(out, "  Board %s incarnation %s epoch %s\n", e.Identity.BoardID, e.Identity.Incarnation, *e.MemberEpoch); err != nil {
				return err
			}
		}
		if e.AttentionContext != nil {
			if _, err := fmt.Fprintf(out, "  Policy %d, %s\n", e.AttentionContext.PolicyVersion, e.AttentionContext.Source); err != nil {
				return err
			}
		}
		for _, category := range []struct {
			label string
			count T.CatalogCount
		}{{"Blocked", e.Attention.Blocked}, {"Needs Terry", e.Attention.NeedsTerry}, {"Ready for Review", e.Attention.ReadyForReview}} {
			value := "Unavailable"
			switch category.count.State {
			case "known":
				value = fmt.Sprint(*category.count.Value)
			case "not_configured":
				value = "Not configured"
			case "stale":
				value = fmt.Sprintf("%d (stale, age %ds)", *category.count.Value, *e.AgeSeconds)
			}
			if _, err := fmt.Fprintf(out, "  %s  %s\n", category.label, value); err != nil {
				return err
			}
		}
	}
	return nil
}
