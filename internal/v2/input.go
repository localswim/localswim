package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errStale = errors.New("stale revision")
var errForbidden = errors.New("operation forbidden")
var errMissing = errors.New("resource missing")

type invalidField struct{ Field, Message string }

func (e invalidField) Error() string      { return e.Field + ": " + e.Message }
func invalid(field, message string) error { return invalidField{field, message} }

func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, invalid("body", "Expected UTF-8 JSON.")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanJSON(d, 0); err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, invalid("body", "Expected one JSON object.")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, invalid("body", "Expected a JSON object.")
	}
	return obj, nil
}
func scanJSON(d *json.Decoder, depth int) error {
	if depth > 128 {
		return invalid("body", "JSON nesting limit exceeded.")
	}
	tok, err := d.Token()
	if err != nil {
		return invalid("body", "Malformed JSON.")
	}
	switch tok {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return invalid("body", "Malformed JSON object.")
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return invalid("body", "Repeated JSON member.")
			}
			seen[name] = true
			if err := scanJSON(d, depth+1); err != nil {
				return err
			}
		}
	case json.Delim('['):
		for d.More() {
			if err := scanJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return nil
	}
	if _, err := d.Token(); err != nil {
		return invalid("body", "Malformed JSON.")
	}
	return nil
}

var protectedFields = []string{
	"actor", "actor_id", "by", "is_admin", "credentials", "token", "created_at", "updated_at",
	"revision", "etag", "ticket", "id", "expected_board_etag", "expected_card_etag", "board_etag", "card_etag",
	"board_id", "policy_id", "role", "kind", "created_by", "updated_by",
}

func rejectFields(fields map[string]json.RawMessage, allowed []string, closed bool) error {
	for key := range fields {
		if slices.Contains(allowed, key) {
			continue
		}
		if closed {
			return invalid("body", "Unrecognized member.")
		}
		for _, protected := range protectedFields {
			if strings.EqualFold(key, protected) {
				return invalid(protected, "Protected member.")
			}
		}
	}
	return nil
}

func textField(raw json.RawMessage, field string, minLength, maxLength int) (string, error) {
	var value *string
	if json.Unmarshal(raw, &value) != nil || value == nil {
		return "", invalid(field, "Expected a string.")
	}
	n := utf8.RuneCountInString(*value)
	if n < minLength || n > maxLength {
		return "", invalid(field, "String length is out of range.")
	}
	return *value, nil
}

func inputID(raw json.RawMessage, field string) (string, error) {
	text, err := textField(raw, field, 36, 36)
	if err != nil {
		return "", err
	}
	id, ok := normalizeID(text)
	if !ok {
		return "", invalid(field, "Expected a UUIDv4.")
	}
	return id, nil
}

func inputOperationID(raw json.RawMessage, field string) (string, error) {
	text, err := textField(raw, field, 36, 36)
	if err != nil {
		return "", err
	}
	id, ok := normalizeOperationID(text)
	if !ok {
		return "", invalid(field, "Expected a UUIDv7.")
	}
	return id, nil
}

type conditionTag struct {
	opaque string
	weak   bool
}

func parseTagList(value string) ([]conditionTag, bool) {
	if len(value) > 1024 {
		return nil, false
	}
	rest := strings.Trim(value, " \t")
	if rest == "*" {
		return []conditionTag{{opaque: "*"}}, true
	}
	var tags []conditionTag
	for rest != "" {
		weak := strings.HasPrefix(rest, "W/")
		if weak {
			rest = rest[2:]
		}
		if len(rest) < 2 || rest[0] != '"' {
			return nil, false
		}
		end := strings.IndexByte(rest[1:], '"')
		if end < 0 {
			return nil, false
		}
		end++
		for j := 1; j < end; j++ {
			if rest[j] < 0x21 || rest[j] == 0x7f {
				return nil, false
			}
		}
		tags = append(tags, conditionTag{opaque: rest[:end+1], weak: weak})
		rest = strings.Trim(rest[end+1:], " \t")
		if rest == "" {
			return tags, true
		}
		if rest[0] != ',' {
			return nil, false
		}
		rest = strings.TrimLeft(rest[1:], " \t")
	}
	return nil, false
}

func matchesStrongTag(condition, current string) bool {
	tags, ok := parseTagList(condition)
	if !ok {
		return false
	}
	for _, tag := range tags {
		if !tag.weak && tag.opaque == current {
			return true
		}
	}
	return false
}

// expectedBoardRevision decodes an already validated single board precondition.
// Zero cannot match a persisted board revision.
func expectedBoardRevision(tag, id string) uint64 {
	prefix := "\"" + id + "-"
	if !strings.HasPrefix(tag, prefix) || !strings.HasSuffix(tag, "\"") {
		return 0
	}
	value, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(tag, prefix), "\""), 10, 64)
	if err != nil || etag(id, value) != tag {
		return 0
	}
	return value
}

var snapshotETagPattern = regexp.MustCompile(`^"[A-Za-z0-9_-]{16,128}"$`)

func matchingBoardTag(expected string) func(snapshot) bool {
	return func(current snapshot) bool { return matchesStrongTag(expected, current.Board.entityTag()) }
}

func matchingSnapshotTag(expected string) func(snapshot) bool {
	return func(current snapshot) bool { return matchesStrongTag(expected, snapshotRepresentationTag(current)) }
}

func matchingServiceTag(expected string) func(serviceStatus) bool {
	return func(current serviceStatus) bool { return matchesStrongTag(expected, current.entityTag()) }
}
