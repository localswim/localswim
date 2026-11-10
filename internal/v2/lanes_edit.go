package v2

import (
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"
)

func (x *request) createLane() {
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	boardID, err := inputID(obj["board_id"], "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	current, found := x.service.store.accepted(boardID)
	if !found || !slices.Contains(current.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !current.can(x.actor, "manage_workflow") {
		x.fail(errForbidden)
		return
	}
	fields := &workflowFields{path: "lane", fields: obj, err: rejectFields(obj,
		[]string{"id", "board_id", "board_etag", "label", "key", "role", "before_id", "after_id", "create_for", "connect_for"}, false)}
	boardTag := laneBoardTag(fields)
	if fields.err != nil {
		x.fail(fields.err)
		return
	}
	var item lane
	next, err := x.service.store.update(boardID, boardTag, func(current *snapshot) error {
		if !current.can(x.actor, "manage_workflow") {
			return errForbidden
		}
		var err error
		item, err = insertLane(current, fields, x.actor, time.Now())
		return err
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("Location", Prefix+"/lanes/"+item.ID)
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	x.respond(http.StatusCreated, item, item.entityTag())
}

func laneBoardTag(fields *workflowFields) string {
	tag := fields.string("board_etag", 18, 130, true)
	if !snapshotETagPattern.MatchString(tag) {
		fields.fail("board_etag", "must be one strong board entity tag")
	}
	return tag
}

func (x *request) renameLane(id string) {
	var boardID string
	for _, current := range x.service.store.list() {
		if slices.Contains(current.Board.LaneIDs, id) && slices.Contains(current.Board.UserIDs, x.actor) {
			if !current.can(x.actor, "manage_workflow") {
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
	laneTag, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	fields := &workflowFields{path: "lane", fields: obj, err: rejectFields(obj, []string{"board_etag", "label"}, false)}
	boardTag := laneBoardTag(fields)
	for _, name := range []string{"key", "retired", "approval_enabled", "reorder_enabled", "position"} {
		if _, found := obj[name]; found {
			fields.fail(name, "This patch field is unavailable.")
		}
	}
	label := fields.string("label", 1, 256, true)
	if fields.err != nil {
		x.fail(fields.err)
		return
	}
	var item lane
	next, err := x.service.store.update(boardID, boardTag, func(current *snapshot) error {
		if !current.can(x.actor, "manage_workflow") {
			return errForbidden
		}
		index := laneIndex(current.Lanes, id)
		if index < 0 {
			return errMissing
		}
		item = current.Lanes[index]
		if !matchesStrongTag(laneTag, item.entityTag()) {
			return errStale
		}
		if item.Label == label {
			return nil
		}
		now := laneEditTime(*current, time.Now())
		before := item.Label
		appendLaneAudit(current, x.actor, item.ID, "patch", "label", &before, &label, now)
		item.Label = label
		item.UpdatedAt, item.Revision = now, current.Board.Revision+1
		current.Lanes[index] = item
		return nil
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	x.respond(http.StatusOK, item, item.entityTag())
}

func insertLane(current *snapshot, fields *workflowFields, actor string, at time.Time) (lane, error) {
	now := laneEditTime(*current, at)
	id := newID()
	if _, found := fields.fields["id"]; found {
		id = fields.id("id")
	}
	label := fields.string("label", 1, 256, true)
	role := fields.enum("role", "ordinary", "ordinary", "completed", "blocked", "needs_input", "ready_for_review")
	key := fields.string("key", 1, 128, false)
	if _, present := fields.fields["key"]; !present {
		key = initialLaneKey(label, role)
		if key == "" {
			fields.fail("key", "is required when the label does not yield a key")
		}
	}
	if role == "completed" {
		fields.fail("role", "the board already has a Completed lane")
	}
	if key == "completed" {
		fields.fail("key", "the completed key is reserved")
	}
	anchor, after := laneAnchor(fields)
	users := make(map[string]string, len(current.Users))
	for _, member := range current.Users {
		users[member.ID] = member.Kind
	}
	createFor := fields.referenceList("create_for", users)
	connectFor := fields.referenceList("connect_for", users)
	if fields.err != nil {
		return lane{}, fields.err
	}
	if laneIdentityConflict(*current, id, key) {
		return lane{}, errIdentityConflict
	}

	slices.SortFunc(current.Lanes, func(a, b lane) int {
		switch {
		case a.Position < b.Position:
			return -1
		case a.Position > b.Position:
			return 1
		default:
			return 0
		}
	})
	index := len(current.Lanes)
	if anchor != "" {
		index = laneIndex(current.Lanes, anchor)
		if index < 0 {
			return lane{}, errMissing
		}
		if after {
			index++
		}
	}

	item := lane{ID: id, Revision: current.Board.Revision + 1, CreatedAt: now, UpdatedAt: now,
		BoardID: current.Board.ID, Key: key, Label: label, Role: role}
	activeCount := 0
	for _, existing := range current.Lanes {
		if !existing.Retired {
			activeCount++
		}
	}
	// Every added move grant contains three UUIDs. Refuse a change that
	// cannot fit even before encoding fields or including the rest of the snapshot.
	if len(connectFor) > 0 && activeCount > maxSnapshotBytes/(2*3*36*len(connectFor)) {
		return lane{}, invalid("connect_for", "Requested grants exceed the snapshot size limit.")
	}
	oldPolicy := current.Policy
	// Preserve future policy editing for boards that fit the published PUT limits.
	if len(oldPolicy.CreateGrants) <= maxPolicyGrantInputs && len(oldPolicy.MoveGrants) <= maxPolicyGrantInputs {
		if len(oldPolicy.CreateGrants)+len(createFor) > maxPolicyGrantInputs {
			return lane{}, invalid("create_for", "Requested grants would exceed the editable policy limit.")
		}
		if len(oldPolicy.MoveGrants)+2*activeCount*len(connectFor) > maxPolicyGrantInputs {
			return lane{}, invalid("connect_for", "Requested grants would exceed the editable policy limit.")
		}
	}
	for _, id := range createFor {
		current.Policy.CreateGrants = append(current.Policy.CreateGrants, createGrant{UserID: id, LaneID: item.ID})
	}
	for _, id := range connectFor {
		for _, other := range current.Lanes {
			if !other.Retired {
				current.Policy.MoveGrants = append(current.Policy.MoveGrants,
					moveGrant{UserID: id, FromLaneID: item.ID, ToLaneID: other.ID},
					moveGrant{UserID: id, FromLaneID: other.ID, ToLaneID: item.ID})
			}
		}
	}
	item.Position = insertLanePosition(current, index, actor, now)
	current.Lanes = slices.Insert(current.Lanes, index, item)
	current.Board.LaneIDs = make([]string, 0, len(current.Lanes))
	for _, entry := range current.Lanes {
		current.Board.LaneIDs = append(current.Board.LaneIDs, entry.ID)
	}
	if issue := validateWorkflowPaths(current.Lanes, current.Policy); issue != nil {
		return lane{}, *issue
	}
	appendLaneAudit(current, actor, item.ID, "create", "", nil, nil, now)
	before, afterPolicy := policyAuditValue(oldPolicy), policyAuditValue(current.Policy)
	if before != afterPolicy {
		current.Policy.ETag, current.Policy.UpdatedAt = etag(current.Policy.ID, current.Board.Revision+1), now
		field := policyGrantsField
		current.Activity = append(current.Activity, activity{
			ID: newID(), BoardID: current.Board.ID, Revision: current.Board.Revision + 1, At: now,
			ActorID: actor, Operation: "patch", ResourceType: "policy", ResourceID: current.Policy.ID,
			Field: &field, Before: &before, After: &afterPolicy,
		})
	}
	return item, nil
}

func laneAnchor(fields *workflowFields) (id string, after bool) {
	_, before := fields.fields["before_id"]
	_, after = fields.fields["after_id"]
	switch {
	case before && after:
		fields.fail("before_id", "conflicts with after_id")
	case before:
		id = fields.id("before_id")
	case after:
		id = fields.id("after_id")
	}
	return id, after
}

func laneIndex(lanes []lane, id string) int {
	return slices.IndexFunc(lanes, func(l lane) bool { return l.ID == id })
}

func laneIdentityConflict(current snapshot, id, key string) bool {
	return snapshotIdentityExists(&current, id) ||
		slices.ContainsFunc(current.Lanes, func(item lane) bool { return item.Key == key })
}
func laneEditTime(current snapshot, at time.Time) time.Time {
	at = canonicalTime(at)
	if at.Before(current.Board.UpdatedAt) {
		return current.Board.UpdatedAt
	}
	return at
}

func appendLaneAudit(current *snapshot, actor, id, operation, field string, before, after *string, now time.Time) {
	event := activity{
		ID: newID(), BoardID: current.Board.ID, Revision: current.Board.Revision + 1, At: now,
		ActorID: actor, Operation: operation, ResourceType: "lane", ResourceID: id, Before: before, After: after,
	}
	if field != "" {
		event.Field = &field
	}
	current.Activity = append(current.Activity, event)
}

func insertLanePosition(current *snapshot, index int, actor string, now time.Time) int {
	position := 0
	if index > 0 && current.Lanes[index-1].Position < math.MaxInt {
		position = current.Lanes[index-1].Position + 1
	}
	// Normally only the contiguous run after the insertion needs to shift.
	// Compact a sparse sequence when its existing positions cannot fit a shift.
	if index > 0 && (position == 0 || position > math.MaxInt-(len(current.Lanes)-index)) {
		for i := range current.Lanes {
			next := i
			if i >= index {
				next++
			}
			setLanePosition(current, i, next, actor, now)
		}
		return index
	}
	for i := index; i < len(current.Lanes) && current.Lanes[i].Position == position+(i-index); i++ {
		setLanePosition(current, i, position+(i-index)+1, actor, now)
	}
	return position
}

func setLanePosition(current *snapshot, index, position int, actor string, now time.Time) {
	item := &current.Lanes[index]
	if item.Position == position {
		return
	}
	before, after := strconv.Itoa(item.Position), strconv.Itoa(position)
	appendLaneAudit(current, actor, item.ID, "patch", "position", &before, &after, now)
	item.Position = position
	item.UpdatedAt, item.Revision = now, current.Board.Revision+1
}

func laneGrantAddition(old, next policy, laneID string) bool {
	if !preservesPolicyGrants(old, next) {
		return false
	}
	oldCreate := make(map[createGrant]bool, len(old.CreateGrants))
	for _, grant := range old.CreateGrants {
		if grant.LaneID == laneID {
			return false
		}
		oldCreate[grant] = true
	}
	for _, grant := range next.CreateGrants {
		if !oldCreate[grant] && grant.LaneID != laneID {
			return false
		}
	}
	oldMove := make(map[moveGrant]bool, len(old.MoveGrants))
	for _, grant := range old.MoveGrants {
		if grant.FromLaneID == laneID || grant.ToLaneID == laneID {
			return false
		}
		oldMove[grant] = true
	}
	for _, grant := range next.MoveGrants {
		if !oldMove[grant] && grant.FromLaneID != laneID && grant.ToLaneID != laneID {
			return false
		}
	}
	return true
}

func (x *request) laneResource(id string) {
	for _, current := range x.service.store.list() {
		if current.can(x.actor, "read") {
			if index := laneIndex(current.Lanes, id); index >= 0 {
				item := current.Lanes[index]
				x.respond(http.StatusOK, item, item.entityTag())
				return
			}
		}
	}
	x.fail(errMissing)
}
