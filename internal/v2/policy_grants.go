package v2

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"
)

const policyGrantsField = "grants"
const maxPolicyGrantInputs = 1000

var errPolicyGrantConflict = errors.New("policy replacement must retain current grants and settings")

var policyGrantKeys = []string{"default_priority_id", "create_grants", "move_grants", "resource_grants"}

func (x *request) policyGrants(id string) {
	for _, header := range []string{"Origin", "Sec-Fetch-Site", "Sec-Fetch-Mode", "Sec-Fetch-Dest", "Sec-Fetch-User"} {
		if len(x.r.Header.Values(header)) != 0 {
			x.fail(errForbidden)
			return
		}
	}
	var boardID string
	for _, current := range x.service.store.list() {
		if current.Policy.ID == id && slices.Contains(current.Board.UserIDs, x.actor) {
			if !current.can(x.actor, "manage_permissions") {
				x.fail(errForbidden)
				return
			}
			boardID = current.Board.ID
			break
		}
	}
	if boardID == "" {
		x.fail(errMissing)
		return
	}
	policyTag, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	fields := &workflowFields{
		path: "policy", fields: obj,
		err: rejectFields(obj, append(slices.Clone(policyGrantKeys), "board_etag"), true),
	}
	boardTag := fields.string("board_etag", 18, 130, true)
	if !snapshotETagPattern.MatchString(boardTag) {
		fields.fail("board_etag", "must be one strong board entity tag")
	}
	if fields.err != nil {
		x.fail(fields.err)
		return
	}
	next, err := x.updateBoard(boardID, boardTag, func(current *snapshot) error {
		if !current.can(x.actor, "manage_permissions") {
			return errForbidden
		}
		if !matchesStrongTag(policyTag, current.Policy.entityTag()) {
			return errStale
		}
		grants, err := readBoardPolicy(fields, *current, maxPolicyGrantInputs)
		if err != nil {
			return err
		}
		return addPolicyGrants(current, grants, x.actor, time.Now())
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", next.Board.entityTag())
	x.respond(http.StatusOK, next.Policy, "")
}

func readBoardPolicy(fields *workflowFields, current snapshot, maxGrantItems int) (policy, error) {
	users := make(map[string]string, len(current.Users))
	for _, u := range current.Users {
		users[u.ID] = u.Kind
	}
	lanes := make(map[string]string, len(current.Lanes))
	for _, l := range current.Lanes {
		lanes[l.ID] = l.Role
	}
	priorities := make(map[string]bool, len(current.Priorities))
	for _, p := range current.Priorities {
		priorities[p.ID] = true
	}
	return readPolicyFieldsWithLimit(fields, users, lanes, priorities, "a board ", maxGrantItems)
}

func addPolicyGrants(current *snapshot, next policy, actorID string, now time.Time) error {
	old := current.Policy
	if !preservesPolicyGrants(old, next) {
		return errPolicyGrantConflict
	}
	if issue := validateWorkflowPaths(current.Lanes, next); issue != nil {
		return *issue
	}
	if len(old.CreateGrants) == len(next.CreateGrants) && len(old.MoveGrants) == len(next.MoveGrants) {
		return nil
	}

	next.entity, next.BoardID = old.entity, old.BoardID
	next.Revision = current.Board.Revision + 1
	now = canonicalTime(now)
	if now.Before(current.Board.UpdatedAt) {
		now = current.Board.UpdatedAt
	}
	next.UpdatedAt = now
	before, after := policyAuditValue(old), policyAuditValue(next)
	field := policyGrantsField
	current.Policy = next
	current.Activity = append(current.Activity, activity{
		ID: newOperationID(), BoardID: current.Board.ID, Revision: current.Board.Revision + 1, At: now,
		ActorID: actorID, Operation: "patch", ResourceType: "policy", ResourceID: old.ID,
		Field: &field, Before: &before, After: &after,
	})
	return nil
}

func preservesPolicyGrants(old, next policy) bool {
	return old.DefaultPriorityID == next.DefaultPriorityID &&
		slices.EqualFunc(old.ResourceGrants, next.ResourceGrants, func(a, b resourceGrant) bool {
			return a.UserID == b.UserID && slices.Equal(a.Capabilities, b.Capabilities)
		}) &&
		containsPolicyGrants(next.CreateGrants, old.CreateGrants) &&
		containsPolicyGrants(next.MoveGrants, old.MoveGrants)
}

func containsPolicyGrants[T comparable](all, retained []T) bool {
	seen := make(map[T]bool, len(all))
	for _, grant := range all {
		seen[grant] = true
	}
	for _, grant := range retained {
		if !seen[grant] {
			return false
		}
	}
	return true
}

func policyAuditValue(p policy) string {
	// These fields only contain strings, arrays, and structs with no custom encoders.
	data, _ := json.Marshal(map[string]any{
		"default_priority_id": p.DefaultPriorityID,
		"create_grants":       p.CreateGrants,
		"move_grants":         p.MoveGrants,
		"resource_grants":     p.ResourceGrants,
	})
	return string(data)
}

func checkedPolicyAuditEvent(value snapshot, event activity, newLane string) (policy, bool) {
	if event.ResourceID != value.Policy.ID || snapshotString(event.Field) != policyGrantsField ||
		event.Before == nil || event.After == nil {
		return policy{}, false
	}
	before, err := readBoardPolicy(readWorkflowFields([]byte(*event.Before), "policy", true, policyGrantKeys...), value, maxSnapshotBytes)
	if err != nil || policyAuditValue(before) != *event.Before {
		return policy{}, false
	}
	after, err := readBoardPolicy(readWorkflowFields([]byte(*event.After), "policy", true, policyGrantKeys...), value, maxSnapshotBytes)
	if err != nil || policyAuditValue(after) != *event.After || !preservesPolicyGrants(before, after) ||
		(len(before.CreateGrants) == len(after.CreateGrants) && len(before.MoveGrants) == len(after.MoveGrants)) {
		return policy{}, false
	}
	for _, grant := range before.ResourceGrants {
		if grant.UserID == event.ActorID {
			if newLane != "" {
				return after, slices.Contains(grant.Capabilities, "manage_workflow") && laneGrantAddition(before, after, newLane)
			}
			return after, slices.Contains(grant.Capabilities, "manage_permissions")
		}
	}
	return policy{}, false
}
