package contract

// workflowType describes the complete wire definition. Graph and profile-aware
// identity validation remain in the shared archive policy validator.
func workflowType() *Type {
	laneID := text("lane-id")
	lane := obj(map[string]Field{"id": req(laneID), "label": req(text("workflow-label")), "role": req(enum("initial", "in_progress", "review", "completed", "waiting"))})
	edge := obj(map[string]Field{"from": req(laneID), "to": req(laneID), "actors": req(&Type{Kind: "array", Element: enum("automation", "human"), Max: 2, Format: "sorted-unique"})})
	ordinary := []string{"attach", "comment", "create", "edit", "export", "read", "read-own-outcome", "review-handoff"}
	return obj(map[string]Field{
		"attention":  opt(obj(map[string]Field{"blocked": req(&Type{Kind: "array", Element: laneID, Max: 12, Format: "sorted-unique"}), "needsTerry": req(&Type{Kind: "array", Element: laneID, Max: 12, Format: "sorted-unique"}), "readyForReview": req(&Type{Kind: "array", Element: laneID, Max: 1, Format: "sorted-unique"})})),
		"workflowId": req(uuid), "revision": req(number(1, MaxInteger)), "profile": opt(enum("default")),
		"lanes": req(&Type{Kind: "array", Element: lane, Max: 16}), "transitions": req(&Type{Kind: "array", Element: edge, Max: 64}),
		"actorRights": req(obj(map[string]Field{"human": req(&Type{Kind: "array", Element: enum(append([]string{"approve"}, ordinary...)...), Format: "sorted-unique"}), "automation": req(&Type{Kind: "array", Element: enum(ordinary...), Format: "sorted-unique"})})),
	})
}
