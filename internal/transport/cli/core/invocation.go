// Package core implements explicit board CLI commands without executable wiring.
package core

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/localswim/localswim/internal/contract"
	"github.com/localswim/localswim/internal/store/schema"
)

type Selectors struct{ InstallationID, BoardID, Incarnation, GrantID string }
type invocation struct {
	page                                          int
	selected                                      Selectors
	json, native, lookup                          bool
	kind, id, input, file, method, path           string
	archive, cursor, query, lane, owner, priority string
	command                                       contract.Command
}

func uuid(s string) bool { return schema.ValidUUID(s) }
func v4(s string) bool {
	return regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(s)
}

func parse(args []string) (q invocation, code string, exit int) {
	// Recognize lookup intent even when invocation validation fails, so no local
	// selector/flag failure can classify an original submission as noncommitted.
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "operation" && args[i+1] == "read" {
			q.lookup = true
		}
	}
	p, err := ParsePrefix(args)
	q.selected, q.json = p.Selected, p.JSON
	q.page = 1
	if err != nil {
		code, exit := failure(err)
		return q, code, exit
	}
	seen := p.seen
	args = p.Args
	i := 0
	if i+2 > len(args) {
		return q, "INVOCATION_OR_IO", 1
	}
	group, action := args[i], args[i+1]
	i += 2
	q.kind = group + " " + action
	switch q.kind {
	case "card list", "card search":
		q.method = "GET"
		q.archive = "active"
	case "board read":
		q.method = "GET"
	case "card read", "card history", "card children", "card relationships", "operation read":
		q.method = "GET"
	case "card create":
		q.command = "card.create"
	case "card edit", "card archive", "card unarchive", "card set-parent", "card clear-parent", "card relationships-replace":
		q.command = "card.edit"
	case "comment add":
		q.command = "comment.add"
	case "card link-add":
		q.command = "link.add"
	case "card link-remove":
		q.command = "link.remove"
	case "card review-handoff":
		q.command = "review-handoff"
	case "card approve":
		q.command = "approve"
	default:
		return q, "INVOCATION_OR_IO", 1
	}
	if q.kind != "board read" && q.kind != "card create" && q.kind != "card list" && q.kind != "card search" {
		if i >= len(args) || !uuid(args[i]) {
			return q, "INVOCATION_OR_IO", 1
		}
		q.id = args[i]
		i++
	}
	for i < len(args) {
		k := args[i]
		i++
		if seen[k] {
			return q, "INVOCATION_OR_IO", 1
		}
		seen[k] = true
		if k == "--native-owner" && q.lookup {
			q.native = true
			continue
		}
		if i >= len(args) {
			return q, "INVOCATION_OR_IO", 1
		}
		if k == "--page" && (q.kind == "card children" || q.kind == "card relationships") {
			n, e := strconv.Atoi(args[i])
			if e != nil || n < 1 || strconv.Itoa(n) != args[i] || q.json {
				return q, "INVOCATION_OR_IO", 1
			}
			q.page = n
		} else if k == "--archive" && (q.kind == "card list" || q.kind == "card search") {
			if args[i] != "active" && args[i] != "archived" && args[i] != "all" {
				return q, "INVOCATION_OR_IO", 1
			}
			q.archive = args[i]
		} else if k == "--cursor" && (q.kind == "card list" || q.kind == "card search") {
			if len(args[i]) == 0 || len(args[i]) > 2048 {
				return q, "INVOCATION_OR_IO", 1
			}
			for _, c := range args[i] {
				if c < 0x21 || c > 0x7e {
					return q, "INVOCATION_OR_IO", 1
				}
			}
			q.cursor = args[i]
		} else if k == "--query" && q.kind == "card search" {
			if !utf8.ValidString(args[i]) || len(args[i]) > 1024 {
				return q, "INVOCATION_OR_IO", 1
			}
			q.query = args[i]
		} else if k == "--lane" && q.kind == "card search" {
			switch args[i] {
			case "Ready for Work", "In Progress", "Ready for Review", "Completed":
			default:
				return q, "INVOCATION_OR_IO", 1
			}
			q.lane = args[i]
		} else if k == "--owner" && q.kind == "card search" {
			if args[i] != "unassigned" && !uuid(args[i]) {
				return q, "INVOCATION_OR_IO", 1
			}
			q.owner = args[i]
		} else if k == "--priority" && q.kind == "card search" {
			v := args[i]
			if len(v) != 2 || v[0] != 'P' || v[1] < '0' || v[1] > '5' {
				return q, "INVOCATION_OR_IO", 1
			}
			q.priority = v
		} else if k == "--input" && q.command != "" {
			q.input = args[i]
		} else if k == "--evidence" && q.lookup {
			q.file = args[i]
		} else {
			return q, "INVOCATION_OR_IO", 1
		}
		i++
	}
	if err := ValidateSelectors(q.selected, q.native); err != nil {
		code, exit := failure(err)
		return q, code, exit
	}
	if (q.command != "" && q.input == "") || (q.lookup && q.file == "") {
		return q, "INVOCATION_OR_IO", 1
	}
	base := "/api/v1/boards/" + q.selected.BoardID
	q.path = base
	switch q.kind {
	case "card list", "card search":
		values := url.Values{"archive": {q.archive}}
		if q.kind == "card search" {
			values.Set("q", q.query)
		}
		if q.lane != "" {
			values.Set("lane", q.lane)
		}
		if q.owner != "" {
			values.Set("owner", q.owner)
		}
		if q.priority != "" {
			values.Set("priority", q.priority)
		}
		if q.cursor != "" {
			values.Set("cursor", q.cursor)
		}
		q.path = base + "/cards?" + values.Encode()
	case "card read", "card edit":
		q.path = base + "/cards/" + q.id
	case "card history":
		q.path = base + "/cards/" + q.id + "/history"
	case "operation read":
		q.path = base + "/operations/" + q.id
	}
	if q.command != "" {
		for _, d := range contract.Commands() {
			if d.Command == q.command {
				q.method = d.Method
				q.path = strings.ReplaceAll(strings.ReplaceAll(d.Route, "{boardId}", q.selected.BoardID), "{cardId}", q.id)
			}
		}
	}
	return q, "", 0
}
