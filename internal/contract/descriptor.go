package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Type is the single validation/generation descriptor. A missing optional field
// is permitted on wire and becomes null only in frozen semantic payloads.
type Type struct {
	Kind     string
	Nullable bool
	Fields   map[string]Field
	Enum     []string
	Min, Max uint64
	Element  *Type
	Format   string
}
type Field struct {
	Type     *Type
	Optional bool
}

func obj(fields map[string]Field) *Type { return &Type{Kind: "object", Fields: fields} }
func req(t *Type) Field                 { return Field{Type: t} }
func opt(t *Type) Field                 { return Field{Type: t, Optional: true} }
func enum(values ...string) *Type       { return &Type{Kind: "string", Enum: values} }
func number(min, max uint64) *Type      { return &Type{Kind: "integer", Min: min, Max: max} }
func text(format string) *Type          { return &Type{Kind: "string", Format: format} }

var uuid = text("uuid")
var digest = text("sha256")
var revision = number(0, MaxInteger)
var priority = enum("P0", "P1", "P2", "P3", "P4", "P5")
var rights = &Type{Kind: "array", Element: enum("approve", "attach", "comment", "create", "edit", "export", "grant-admin", "history-admin", "read", "read-own-outcome", "restore", "review-handoff"), Format: "sorted-unique"}
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var hashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var timePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

type Command string
type CommandDescriptor struct {
	Command       Command
	Target        string
	Method, Route string
	Input         *Type
	Recovery      bool
}

// Commands returns fresh descriptors so a consumer cannot alter validation.
func Commands() []CommandDescriptor {
	empty := obj(map[string]Field{})
	commands := []CommandDescriptor{
		{"card.create", "none", "POST", "/api/v1/boards/{boardId}/cards", obj(map[string]Field{"title": req(text("title")), "description": req(text("prose")), "priority": opt(priority)}), false},
		{"card.edit", "card", "PATCH", "/api/v1/boards/{boardId}/cards/{cardId}", obj(map[string]Field{"title": opt(text("title")), "description": opt(text("prose")), "priority": opt(priority), "lane": opt(enum("In Progress"))}), false},
		{"comment.add", "card", "POST", "/api/v1/boards/{boardId}/cards/{cardId}/comments", obj(map[string]Field{"text": req(text("comment"))}), false},
		{"attachment.add", "card", "POST", "/api/v1/boards/{boardId}/cards/{cardId}/attachments", obj(map[string]Field{"name": req(text("attachment-name")), "mediaLabel": req(text("media-label")), "byteLength": req(number(0, 10485760)), "sha256": req(digest)}), false},
		{"link.add", "card", "POST", "/api/v1/boards/{boardId}/cards/{cardId}/link-add", obj(map[string]Field{"kind": req(enum("blocking", "informational")), "targetCardId": req(uuid)}), false},
		{"link.remove", "card", "POST", "/api/v1/boards/{boardId}/cards/{cardId}/link-remove", obj(map[string]Field{"linkId": req(uuid)}), false},
		{"review-handoff", "card", "POST", "/api/v1/boards/{boardId}/cards/{cardId}/review-handoff", empty, false},
		{"approve", "card", "POST", "/api/v1/boards/{boardId}/cards/{cardId}/approve", obj(map[string]Field{"policyVersion": req(number(1, 1))}), false},
		{"grant.create", "none", "POST", "/api/v1/grants", obj(map[string]Field{"granteeActorId": req(uuid), "kind": req(enum("human", "automation")), "rights": req(rights)}), false},
		{"grant.revoke", "grant", "DELETE", "/api/v1/grants/{grantId}", empty, false},
		{"history.clear", "none", "POST", "/api/v1/boards/{boardId}/recovery/confirm", obj(map[string]Field{"throughTime": req(text("timestamp"))}), true},
		{"retention.set", "none", "POST", "/api/v1/boards/{boardId}/recovery/confirm", obj(map[string]Field{"historyDays": req(number(1, 3650)), "activityDays": req(number(1, 3650)), "outcomeDays": req(number(1, 3650))}), true},
	}
	for i := range commands {
		commands[i].Input = cloneType(commands[i].Input)
	}
	return commands
}

func cloneType(t *Type) *Type {
	c := *t
	c.Enum = append([]string(nil), t.Enum...)
	if t.Element != nil {
		c.Element = cloneType(t.Element)
	}
	if t.Fields != nil {
		c.Fields = map[string]Field{}
		for k, f := range t.Fields {
			c.Fields[k] = Field{Type: cloneType(f.Type), Optional: f.Optional}
		}
	}
	return &c
}
func descriptor(c Command) (CommandDescriptor, error) {
	for _, d := range Commands() {
		if d.Command == c {
			return d, nil
		}
	}
	return CommandDescriptor{}, fmt.Errorf("unsupported command")
}

func (t *Type) validate(v any, path string) error {
	if t.Nullable && v == nil {
		return nil
	}
	switch t.Kind {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object", path)
		}
		for k := range m {
			if _, ok := t.Fields[k]; !ok {
				return fmt.Errorf("%s: unknown field %s", path, k)
			}
		}
		for k, f := range t.Fields {
			x, ok := m[k]
			if !ok {
				if f.Optional {
					continue
				}
				return fmt.Errorf("%s.%s: required", path, k)
			}
			if e := f.Type.validate(x, path+"."+k); e != nil {
				return e
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) {
			return fmt.Errorf("%s: expected Unicode string", path)
		}
		if len(t.Enum) > 0 {
			found := false
			for _, e := range t.Enum {
				found = found || s == e
			}
			if !found {
				return fmt.Errorf("%s: unsupported value", path)
			}
		}
		switch t.Format {
		case "uuid":
			if !uuidPattern.MatchString(s) || s == "00000000-0000-0000-0000-000000000000" {
				return fmt.Errorf("%s: invalid UUID", path)
			}
		case "sha256":
			if !hashPattern.MatchString(s) {
				return fmt.Errorf("%s: invalid SHA256", path)
			}
		case "timestamp":
			if !timePattern.MatchString(s) {
				return fmt.Errorf("%s: invalid UTC timestamp", path)
			}
			v, e := time.Parse("2006-01-02T15:04:05Z", s)
			if e != nil || v.Year() < 1 {
				return fmt.Errorf("%s: invalid UTC timestamp", path)
			}
		case "title":
			if n := utf8.RuneCountInString(s); n < 1 || n > 200 || strings.TrimSpace(s) == "" {
				return fmt.Errorf("%s: invalid title", path)
			}
		case "prose", "comment":
			if len(s) > 65536 || (t.Format == "comment" && strings.TrimSpace(s) == "") {
				return fmt.Errorf("%s: invalid prose", path)
			}
		case "attachment-name":
			if len(s) > 255 {
				return fmt.Errorf("%s: invalid attachment name", path)
			}
		case "media-label":
			if len(s) > 127 {
				return fmt.Errorf("%s: invalid media label", path)
			}
			for _, r := range s {
				if r > 127 {
					return fmt.Errorf("%s: expected ASCII", path)
				}
			}
		}
	case "integer":
		n, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("%s: expected integer", path)
		}
		u, e := integer(n)
		if e != nil || u < t.Min || u > t.Max {
			return fmt.Errorf("%s: integer range", path)
		}
	case "array":
		a, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array", path)
		}
		prior := ""
		for i, v := range a {
			if e := t.Element.validate(v, path); e != nil {
				return e
			}
			if t.Format == "sorted-unique" {
				s := v.(string)
				if i > 0 && s <= prior {
					return fmt.Errorf("%s: expected sorted unique values", path)
				}
				prior = s
			}
		}
	case "null":
		if v != nil {
			return fmt.Errorf("%s: expected null", path)
		}
	default:
		return fmt.Errorf("invalid descriptor")
	}
	return nil
}
func sortedFields(t *Type) []string {
	keys := make([]string, 0, len(t.Fields))
	for k := range t.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// RequestType describes transport metadata only. Route/actor remain independently
// resolved server inputs; boardId is submitted only by the grant routes.
func RequestType(d CommandDescriptor) *Type {
	target := &Type{Kind: "null"}
	cardRev := &Type{Kind: "null"}
	if d.Target == "card" {
		target = obj(map[string]Field{"cardId": req(uuid)})
		cardRev = revision
	}
	if d.Target == "grant" {
		target = obj(map[string]Field{"grantId": req(uuid)})
	}
	f := map[string]Field{"opId": req(uuid), "incarnation": req(uuid), "target": req(target), "expectedRevision": req(revision), "expectedCardRevision": req(cardRev), "input": req(d.Input)}
	if d.Target != "card" && !d.Recovery {
		f["expectedCardRevision"] = opt(cardRev)
	}
	if strings.HasPrefix(string(d.Command), "grant.") {
		f["boardId"] = req(uuid)
	}
	if d.Recovery {
		f["command"] = req(enum(string(d.Command)))
		f["previewHash"] = req(digest)
	}
	return cloneType(obj(f))
}
