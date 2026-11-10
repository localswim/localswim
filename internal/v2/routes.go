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
	case "service", "users", "lanes", "priorities":
		if hasID {
			x.fail(errMissing)
			return
		}
	case "verifications", "snapshots", "policies":
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
		x.options(methods)
		return
	}
	if !x.authenticate() {
		return
	}
	if !slices.Contains(methods, x.r.Method) {
		x.w.Header().Set("Allow", strings.Join(methods, ", "))
		x.problem(405, "method_not_allowed", "This method is not available for the resource.", "", nil)
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
	case "users", "lanes", "priorities":
		x.workflowCollection(resource)
	case "policies":
		x.policyResource(id)
	case "verifications", "snapshots":
		x.recoveryResource(resource, id)
	case "service":
		x.serviceStatus()
	case "builds":
		x.buildsResource(id)
	}
}

func acceptsJSON(values []string) bool {
	if len(values) == 0 {
		return true
	}
	specificity := -1
	quality := float64(0)
	for _, line := range values {
		for token := range strings.SplitSeq(line, ",") {
			media, params, _ := strings.Cut(token, ";")
			rank := slices.Index([]string{"*/*", "application/*", "application/json"}, strings.ToLower(strings.TrimSpace(media)))
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

func page[T any](x *request, resource string, items []T) {
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
		data, err := marshalCanonical(items)
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
