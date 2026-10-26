package archive

import (
	"strings"

	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/transport/cli/core"
)

type invocation struct {
	selected                                            core.Selectors
	json                                                bool
	kind, id, input, file, destination, source, request string
}

func validID(s string) bool { return schema.ValidUUID(s) }

func parse(args []string) (q invocation, code string, exit int) {
	prefix, err := core.ParsePrefix(args)
	q.selected = prefix.Selected
	q.json = prefix.JSON
	if err != nil {
		code, exit := fixedFailure(err)
		return q, code, exit
	}
	args = prefix.Args
	i := 0
	seen := map[string]bool{}
	bad := func() (invocation, string, int) { return q, "INVOCATION_OR_IO", 1 }
	if i+2 > len(args) {
		return bad()
	}
	q.kind = args[i] + " " + args[i+1]
	i += 2
	switch q.kind {
	case "attachment add", "attachment download":
		if i == len(args) || !validID(args[i]) {
			return bad()
		}
		q.id = args[i]
		i++
	case "export create", "restore create", "restore status":
	default:
		return bad()
	}
	for i < len(args) {
		k := args[i]
		i++
		if seen[k] || i == len(args) {
			return bad()
		}
		seen[k] = true
		v := args[i]
		i++
		switch {
		case k == "--input" && q.kind == "attachment add":
			q.input = v
		case k == "--file" && q.kind == "attachment add":
			q.file = v
		case k == "--destination" && q.kind != "attachment add":
			q.destination = v
		case k == "--source" && q.kind == "restore create":
			q.source = v
		case k == "--request-id" && q.kind == "restore status":
			q.request = v
		default:
			return bad()
		}
	}
	if strings.HasPrefix(q.kind, "restore ") {
		if q.selected != (core.Selectors{}) || q.destination == "" || q.kind == "restore create" && q.source == "" || q.kind == "restore status" && !validID(q.request) {
			return bad()
		}
		return q, "", 0
	}
	if err := core.ValidateSelectors(q.selected, false); err != nil {
		code, exit := fixedFailure(err)
		return q, code, exit
	}
	if q.kind == "attachment add" && (q.file == "" || q.input == "") || q.kind != "attachment add" && q.destination == "" {
		return bad()
	}
	return q, "", 0
}
