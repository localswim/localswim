package v2

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

func (x *request) dispatch() {
	if !strings.HasPrefix(x.r.URL.Path, Prefix+"/") || strings.Contains(x.r.URL.EscapedPath(), "%") {
		x.fail(errMissing)
		return
	}
	resource, rawID, hasID := strings.Cut(strings.TrimPrefix(x.r.URL.Path, Prefix+"/"), "/")
	contentRoute := hasID && (resource == "attachments" || resource == "backups") && strings.HasSuffix(rawID, "/content")
	if contentRoute {
		rawID = strings.TrimSuffix(rawID, "/content")
	}
	if hasID && (rawID == "" || strings.Contains(rawID, "/")) {
		x.fail(errMissing)
		return
	}
	var id string
	if hasID {
		var ok bool
		id, ok = normalizeID(rawID)
		if !ok {
			x.fail(invalid("id", "Expected a UUIDv4."))
			return
		}
	}
	methods := []string{"GET", "HEAD"}
	switch resource {
	case "boards":
		if hasID {
			methods = append(methods, "PATCH")
		} else {
			methods = append(methods, "POST")
		}
	case "workspace":
		if hasID {
			x.fail(errMissing)
			return
		}
		methods = append(methods, "PATCH")
	case "cards":
		if hasID {
			methods = append(methods, "PATCH")
		} else {
			methods = append(methods, "POST", "PATCH")
		}
	case "backups":
		if !hasID {
			methods = []string{"POST"}
		}
	case "ticket-orders":
		if !hasID {
			x.fail(errMissing)
			return
		}
	case "events":
		if hasID {
			x.fail(errMissing)
			return
		}
		methods = []string{"GET"}
	case "lanes":
		if hasID {
			methods = append(methods, "PATCH")
		} else {
			methods = append(methods, "POST")
		}
	case "comments", "attachments":
		if !hasID {
			methods = append(methods, "POST")
		} else if resource == "attachments" && !contentRoute {
			methods = append(methods, "DELETE")
		}
	case "relationships":
		if hasID {
			methods = append(methods, "PATCH", "DELETE")
		} else {
			methods = append(methods, "POST")
		}
	case "approvals":
		if !hasID {
			methods = []string{"POST"}
		}
	case "browser-session":
		if hasID {
			x.fail(errMissing)
			return
		}
		methods = []string{"POST"}
	case "service", "users", "priorities", "session", "activities":
		if hasID {
			x.fail(errMissing)
			return
		}
	case "policies":
		if !hasID {
			x.fail(errMissing)
			return
		}
		methods = append(methods, "PUT")
	case "verifications", "snapshots":
		if !hasID {
			x.fail(errMissing)
			return
		}
	case "builds":
	default:
		x.fail(errMissing)
		return
	}
	methods = append(methods, "OPTIONS")
	if x.r.Method == "OPTIONS" {
		if resource == "policies" && x.r.Header.Get("Access-Control-Request-Method") != "" {
			if strings.EqualFold(x.r.Header.Get("Access-Control-Request-Method"), "PUT") {
				x.fail(errForbidden)
				return
			}
			x.options([]string{"GET", "HEAD", "OPTIONS"})
			return
		}
		x.options(methods)
		return
	}
	if (resource != "browser-session" || x.r.Method != "POST") && !x.authenticate() {
		return
	}
	if !slices.Contains(methods, x.r.Method) {
		x.w.Header().Set("Allow", strings.Join(methods, ", "))
		x.problem(405, "method_not_allowed", "This method is not available for the resource.", "", nil)
		return
	}
	if resource == "events" {
		if !acceptsMedia(x.r.Header.Values("Accept"), "text", "event-stream") {
			x.problem(406, "not_acceptable", "This resource returns an event stream.", "", nil)
			return
		}
		x.eventsResource()
		return
	}
	if contentRoute {
		if resource == "backups" {
			x.backupContent(id)
		} else {
			x.attachmentContent(id)
		}
		return
	}
	if !acceptsJSON(x.r.Header.Values("Accept")) {
		x.problem(406, "not_acceptable", "This resource returns JSON.", "", nil)
		return
	}
	switch resource {
	case "boards":
		switch {
		case hasID:
			x.boardResource(id)
		case x.r.Method == "POST":
			x.createBoard()
		default:
			x.listBoards()
		}
	case "workspace":
		x.workspaceResource()
	case "users", "priorities":
		x.workflowCollection(resource)
	case "lanes":
		switch {
		case x.r.Method == "POST":
			x.createLane()
		case x.r.Method == "PATCH":
			x.renameLane(id)
		case hasID:
			x.laneResource(id)
		default:
			x.workflowCollection(resource)
		}
	case "comments":
		if hasID {
			x.commentResource(id)
		} else {
			x.commentsCollection()
		}
	case "attachments":
		if hasID {
			x.attachmentResource(id)
		} else {
			x.attachmentsCollection()
		}
	case "relationships":
		if hasID {
			x.relationshipResource(id)
		} else {
			x.relationshipsCollection()
		}
	case "approvals":
		if hasID {
			x.approvalResource(id)
		} else {
			x.createApproval()
		}
	case "browser-session":
		x.bootstrapBrowser()
	case "session":
		x.sessionResource()
	case "activities":
		x.listActivities()
	case "cards":
		switch {
		case !hasID && x.r.Method == "PATCH":
			x.batchPriorities()
		case !hasID:
			x.cardsCollection()
		case x.r.Method == "PATCH":
			x.patchCard(id)
		default:
			x.cardResource(id)
		}
	case "policies":
		if x.r.Method == "PUT" {
			x.policyGrants(id)
		} else {
			x.policyResource(id)
		}
	case "verifications", "snapshots":
		x.recoveryResource(resource, id)
	case "backups":
		if hasID {
			x.backupResource(id)
		} else {
			x.createBackup()
		}
	case "ticket-orders":
		x.ticketOrderResource(id)
	case "service":
		x.serviceStatus()
	case "builds":
		x.buildsResource(id)
	}
}

func acceptsJSON(values []string) bool {
	return acceptsMedia(values, "application", "json")
}

func acceptsMedia(values []string, major, subtype string) bool {
	if len(values) == 0 {
		return true
	}
	specificity := -1
	quality := float64(0)
	for _, line := range values {
		for token := range strings.SplitSeq(line, ",") {
			media, params, _ := strings.Cut(token, ";")
			rank := slices.Index([]string{"*/*", major + "/*", major + "/" + subtype}, strings.ToLower(strings.TrimSpace(media)))
			if rank < 0 || rank < specificity {
				continue
			}
			q := float64(1)
			for pair := range strings.SplitSeq(params, ";") {
				k, v, ok := strings.Cut(strings.TrimSpace(pair), "=")
				if ok && strings.EqualFold(k, "q") {
					parsed, err := strconv.ParseFloat(v, 64)
					if err != nil || !(parsed >= 0 && parsed <= 1) {
						q = 0
					} else {
						q = parsed
					}
				}
			}
			if rank > specificity || q > quality {
				quality = q
			}
			specificity = rank
		}
	}
	return quality > 0
}

func singleQuery(r *http.Request, field string) (string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", invalid(field, "Malformed query string.")
	}
	entries := query[field]
	if len(entries) > 1 {
		return "", invalid(field, "Repeated query parameter.")
	}
	if len(entries) == 1 {
		return entries[0], nil
	}
	return "", nil
}

func page[T any](x *request, resource string, items []T, filters ...string) {
	limitValue, err := singleQuery(x.r, "limit")
	if err != nil {
		x.fail(err)
		return
	}
	limit := 50
	if limitValue != "" {
		limit, err = strconv.Atoi(limitValue)
		if err != nil || limit < 1 || limit > 200 {
			x.fail(invalid("limit", "Expected an integer between 1 and 200."))
			return
		}
	}
	cursor, err := singleQuery(x.r, "cursor")
	if err != nil {
		x.fail(err)
		return
	}
	if items == nil {
		items = []T{}
	}
	start := 0
	var scope string
	if cursor != "" || len(items) > limit {
		var selection any = items
		if len(filters) != 0 {
			selection = struct {
				Filters []string
				Items   []T
			}{filters, items}
		}
		data, err := marshalCanonical(selection)
		if err != nil {
			x.fail(err)
			return
		}
		hash := sha256.Sum256(data)
		scope = fmt.Sprintf("%x", hash[:16])
	}
	if cursor != "" {
		if len(cursor) > 2048 {
			x.fail(invalid("cursor", "Invalid or stale cursor."))
			return
		}
		raw, decodeErr := base64.RawURLEncoding.Strict().DecodeString(cursor)
		token, pos, ok := strings.Cut(string(raw), ":")
		start, err = strconv.Atoi(pos)
		if decodeErr != nil || err != nil || !ok || token != scope || start <= 0 || start >= len(items) {
			x.fail(invalid("cursor", "Invalid or stale cursor."))
			return
		}
	}
	end := min(start+limit, len(items))
	var nextCursor *string
	if end < len(items) {
		next := base64.RawURLEncoding.EncodeToString([]byte(scope + ":" + strconv.Itoa(end)))
		nextCursor = &next
	}
	x.respond(200, map[string]any{resource: items[start:end], "next_cursor": nextCursor}, "")
}
