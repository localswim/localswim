package v2

import (
	"encoding/json"
	"regexp"
)

var actorAccentPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

type actorDecoration struct {
	Label  string `json:"label,omitempty"`
	Accent string `json:"accent,omitempty"`
}

type actorPresentation struct {
	Human actorDecoration `json:"human,omitzero"`
	Bot   actorDecoration `json:"bot,omitzero"`
}

func readActorPresentation(raw json.RawMessage) (actorPresentation, error) {
	root := readWorkflowFields(raw, "actor_presentation", true, "human", "bot")
	if root.err != nil {
		return actorPresentation{}, root.err
	}
	var result actorPresentation
	for _, entry := range []struct {
		kind       string
		decoration *actorDecoration
	}{{"human", &result.Human}, {"bot", &result.Bot}} {
		value, found := root.fields[entry.kind]
		if !found {
			continue
		}
		fields := readWorkflowFields(value, root.path+"."+entry.kind, true, "label", "accent")
		entry.decoration.Label = fields.displayText("label", 1, 256)
		if _, present := fields.fields["accent"]; present {
			entry.decoration.Accent = fields.string("accent", 7, 7, true)
			if !actorAccentPattern.MatchString(entry.decoration.Accent) {
				fields.fail("accent", "must be a six-digit hexadecimal color")
			}
		}
		if fields.err != nil {
			return actorPresentation{}, fields.err
		}
	}
	return result, nil
}

func (p actorPresentation) valid() bool {
	for _, decoration := range []actorDecoration{p.Human, p.Bot} {
		if !snapshotText(decoration.Label, 0, 256) ||
			(decoration.Accent != "" && !actorAccentPattern.MatchString(decoration.Accent)) {
			return false
		}
	}
	return true
}

func (p actorPresentation) auditValue() *string {
	encoded, _ := json.Marshal(p)
	value := string(encoded)
	return &value
}

func validActorPresentationAudit(value *string) bool {
	if value == nil {
		return false
	}
	parsed, err := readActorPresentation(json.RawMessage(*value))
	return err == nil && *parsed.auditValue() == *value
}
