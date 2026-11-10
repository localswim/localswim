package v2

import (
	"encoding/json"
	"regexp"
	"strings"
)

var threadIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type notifications struct {
	Enabled        bool    `json:"enabled"`
	TargetThreadID *string `json:"targetThreadId"`
}

func (value notifications) validate() error {
	if value.TargetThreadID != nil && !threadIDPattern.MatchString(*value.TargetThreadID) {
		return invalid("notifications.targetThreadId", "must be a canonical external chat UUID")
	}
	return nil
}

func patchNotifications(current notifications, raw json.RawMessage) (notifications, error) {
	fields, err := decodeObject(raw)
	if err != nil {
		return current, invalid("notifications", "must be an object with unique members")
	}
	if len(fields) == 0 {
		return current, invalid("notifications", "must contain enabled or targetThreadId")
	}

	next := current
	for name, value := range fields {
		switch name {
		case "enabled":
			var enabled *bool
			if err := json.Unmarshal(value, &enabled); err != nil || enabled == nil {
				return current, invalid("notifications.enabled", "must be a boolean")
			}
			next.Enabled = *enabled
		case "targetThreadId":
			var target *string
			if err := json.Unmarshal(value, &target); err != nil {
				return current, invalid("notifications.targetThreadId", "must be an external chat UUID or null")
			}
			if target != nil {
				canonical := strings.ToLower(*target)
				next.TargetThreadID = &canonical
			} else {
				next.TargetThreadID = nil
			}
		default:
			return current, invalid("notifications", "contains an unsupported member")
		}
	}
	if err := next.validate(); err != nil {
		return current, err
	}
	return next, nil
}
