package transport

import (
	"fmt"
	"reflect"
	"regexp"
	"unicode/utf8"

	A "github.com/localswim/localswim/internal/contract/archive"
)

var cardSearchUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var cardSearchCursor = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func cardSearchQuery(q CardSearchQuery) error {
	if !utf8.ValidString(q.Q) || len(q.Q) > 1024 {
		return fmt.Errorf("invalid card query")
	}
	if q.Owner != nil && *q.Owner != "unassigned" && (!cardSearchUUID.MatchString(*q.Owner) || *q.Owner == "00000000-0000-0000-0000-000000000000") {
		return fmt.Errorf("invalid card owner filter")
	}
	return nil
}

func cardSearchView(v CardSearchView) error {
	if len(v.Items) > 20 || (v.Revision == 0 && len(v.Items) != 0) {
		return fmt.Errorf("invalid card summary page")
	}
	if v.NextCursor != nil && (len(v.Items) != 20 || len(*v.NextCursor) > 2048 || !cardSearchCursor.MatchString(*v.NextCursor)) {
		return fmt.Errorf("invalid card continuation")
	}
	seenIDs, seenTickets := map[A.UUID]bool{}, map[A.Positive]bool{}
	for i, row := range v.Items {
		if seenIDs[row.CardID] || seenTickets[row.Ticket] {
			return fmt.Errorf("duplicate card summary")
		}
		seenIDs[row.CardID], seenTickets[row.Ticket] = true, true
		if i > 0 {
			prior := v.Items[i-1]
			if prior.Priority > row.Priority || (prior.Priority == row.Priority && (prior.Ticket > row.Ticket || (prior.Ticket == row.Ticket && prior.CardID >= row.CardID))) {
				return fmt.Errorf("card summary order")
			}
		}
		if (v.Query.Archive == "active" && row.Archived) || (v.Query.Archive == "archived" && !row.Archived) ||
			(v.Query.Lane != nil && row.Lane != *v.Query.Lane) || (v.Query.Priority != nil && row.Priority != *v.Query.Priority) {
			return fmt.Errorf("card summary filter")
		}
		if v.Query.Owner != nil {
			if (*v.Query.Owner == "unassigned" && row.OwnerID != nil) || (*v.Query.Owner != "unassigned" && (row.OwnerID == nil || string(*row.OwnerID) != *v.Query.Owner)) {
				return fmt.Errorf("card summary owner filter")
			}
		}
	}
	return nil
}

// Structural bounds are generated alongside Go's cross-row checks. A partial
// summary never passes through complete BoardView reference validation.
func cardSearchSchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	switch t {
	case reflect.TypeFor[CardSummary]():
		p["title"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 200, "x-nonblank": true}
	case reflect.TypeFor[CardSearchQuery]():
		p["q"] = map[string]any{"type": "string", "x-max-utf8-bytes": 1024}
		p["owner"] = map[string]any{"anyOf": []any{map[string]any{"type": "null"}, map[string]any{"const": "unassigned"}, map[string]any{"type": "string", "pattern": cardSearchUUID.String(), "not": map[string]any{"const": "00000000-0000-0000-0000-000000000000"}}}}
	case reflect.TypeFor[CardSearchView]():
		items := p["items"].(map[string]any)
		items["maxItems"] = 20
		p["nextCursor"] = map[string]any{"anyOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "string", "minLength": 1, "maxLength": 2048, "pattern": cardSearchCursor.String()}}}
	}
}
