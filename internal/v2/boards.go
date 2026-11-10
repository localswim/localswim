package v2

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (x *request) listBoards() {
	rows := make([]boardSummary, 0)
	for _, s := range x.service.store.list() {
		if !s.can(x.actor, "read") {
			continue
		}
		_, err := x.service.store.durable(s.Board.ID)
		rows = append(rows, summarize(s, err))
	}
	sortSummaries(rows)
	page(x, "boards", rows)
}

func (x *request) createBoard() {
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err = rejectFields(obj, []string{"id", "name", "workflow", "default_owner_id"}, false); err != nil {
		x.fail(err)
		return
	}
	name, err := textField(obj["name"], "name", 1, 256)
	if err != nil {
		x.fail(err)
		return
	}
	id := newID()
	if raw, ok := obj["id"]; ok {
		id, err = inputID(raw, "id")
		if err != nil {
			x.fail(err)
			return
		}
	}
	now := canonicalTime(time.Now())
	var wf workflow
	if raw, ok := obj["workflow"]; ok {
		wf, err = customWorkflow(raw, id, now)
		if err != nil {
			x.fail(err)
			return
		}
	} else {
		wf = defaultWorkflow(id, now, x.service.principals)
	}
	s := freshSnapshot(id, name, wf, x.actor, now)
	if raw, ok := obj["default_owner_id"]; ok {
		s.Board.DefaultOwnerID, err = inputID(raw, "default_owner_id")
		if err != nil {
			x.fail(err)
			return
		}
	}
	if !slices.Contains(s.Board.UserIDs, s.Board.DefaultOwnerID) {
		x.fail(invalid("default_owner_id", "Expected a user on this board."))
		return
	}
	if err = x.service.store.create(s); err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("Location", Prefix+"/boards/"+id)
	x.respond(http.StatusCreated, s.Board, s.Board.ETag)
}

func (x *request) boardResource(id string) {
	s, ok := x.service.store.accepted(id)
	if !ok || !slices.Contains(s.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !s.can(x.actor, "read") {
		if x.r.Method == "PATCH" {
			x.fail(errForbidden)
		} else {
			x.fail(errMissing)
		}
		return
	}
	if x.r.Method == "PATCH" {
		x.patchBoard(s)
		return
	}
	view, err := singleQuery(x.r, "view")
	if err != nil {
		x.fail(err)
		return
	}
	if view != "" && !strings.EqualFold(view, "summary") {
		x.fail(invalid("view", "This board view is unavailable."))
		return
	}
	x.respond(200, s.Board, s.Board.ETag)
}

func (x *request) patchBoard(s snapshot) {
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err = rejectFields(obj, []string{"name", "notifications", "default_owner_id"}, false); err != nil {
		x.fail(err)
		return
	}
	for _, name := range []string{"publishing", "availability", "user_ids", "user_css"} {
		if _, ok := obj[name]; ok {
			x.fail(invalid(name, "This patch field is not available."))
			return
		}
	}
	name, hasName := obj["name"]
	defaultOwner, hasDefaultOwner := obj["default_owner_id"]
	raw, hasNotifications := obj["notifications"]
	if !hasName && !hasNotifications && !hasDefaultOwner {
		x.fail(invalid("body", "A supported change is required."))
		return
	}
	authorized := func(s snapshot) bool {
		return s.can(x.actor, "read") && (!(hasName || hasDefaultOwner) || s.can(x.actor, "manage_board")) &&
			(!hasNotifications || s.can(x.actor, "manage_notifications"))
	}
	if !authorized(s) {
		x.fail(errForbidden)
		return
	}
	next, err := x.service.store.update(s.Board.ID, expected, func(s *snapshot) error {
		if !authorized(*s) {
			return errForbidden
		}
		now := canonicalTime(time.Now())
		appendChange := func(field string, before, after *string) {
			s.Activity = append(s.Activity, activity{ID: newID(), BoardID: s.Board.ID, Revision: s.Board.Revision + 1, At: now,
				ActorID: x.actor, Operation: "patch", ResourceType: "board", ResourceID: s.Board.ID, Field: &field, Before: before, After: after})
		}
		if hasName {
			value, err := textField(name, "name", 1, 256)
			if err != nil {
				return err
			}
			before := s.Board.Name
			if value != before {
				appendChange("name", &before, &value)
				s.Board.Name = value
			}
		}
		if hasDefaultOwner {
			owner, err := inputID(defaultOwner, "default_owner_id")
			if err != nil {
				return err
			}
			if !slices.Contains(s.Board.UserIDs, owner) {
				return invalid("default_owner_id", "Expected a user on this board.")
			}
			before := s.Board.DefaultOwnerID
			if before != owner {
				appendChange("default_owner_id", &before, &owner)
				s.Board.DefaultOwnerID = owner
			}
		}
		if hasNotifications {
			current := s.Board.Notifications
			updated, err := patchNotifications(current, raw)
			if err != nil {
				return err
			}
			if current.Enabled != updated.Enabled {
				before, after := strconv.FormatBool(current.Enabled), strconv.FormatBool(updated.Enabled)
				appendChange("notifications.enabled", &before, &after)
			}
			if !sameText(current.TargetThreadID, updated.TargetThreadID) {
				appendChange("notifications.target_thread_id", current.TargetThreadID, updated.TargetThreadID)
			}
			s.Board.Notifications = updated
		}
		return nil
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.respond(200, next.Board, next.Board.ETag)
}

func sameText(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

func (x *request) workflowCollection(resource string) {
	query, err := singleQuery(x.r, "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	id, ok := normalizeID(query)
	if !ok {
		x.fail(invalid("board_id", "A board UUID is required."))
		return
	}
	s, ok := x.service.store.accepted(id)
	if !ok || !s.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	switch resource {
	case "users":
		page(x, "users", s.Users)
	case "lanes":
		page(x, "lanes", s.Lanes)
	case "priorities":
		page(x, "priorities", s.Priorities)
	}
}
func (x *request) policyResource(id string) {
	for _, s := range x.service.store.list() {
		if s.Policy.ID == id && s.can(x.actor, "read") {
			x.respond(200, s.Policy, s.Policy.ETag)
			return
		}
	}
	x.fail(errMissing)
}

func (x *request) recoveryResource(resource, id string) {
	s, ok := x.service.store.accepted(id)
	if !ok || !slices.Contains(s.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !s.can(x.actor, "read") {
		x.fail(errForbidden)
		return
	}
	durable, err := x.service.store.durable(id)
	if resource == "verifications" {
		report := snapshotVerification(id, canonicalTime(time.Now()), durable, err)
		x.respond(200, report, "")
		return
	}
	if err != nil {
		x.fail(errInvalidSnapshot)
		return
	}
	if !durable.can(x.actor, "read") {
		x.fail(errForbidden)
		return
	}
	x.respond(200, durable, "")
}

func (x *request) workspaceResource() {
	if x.r.Method == "GET" || x.r.Method == "HEAD" {
		result, err := x.service.workspace.read()
		if err != nil {
			x.fail(err)
			return
		}
		x.respond(200, result, result.ETag)
		return
	}
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	result, err := x.service.workspace.patch(expected, fields, func(id string) bool {
		s, ok := x.service.store.accepted(id)
		return ok && s.can(x.actor, "read")
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.respond(200, result, result.ETag)
}

func (x *request) buildsResource(id string) {
	items := x.service.builds.builds()
	if id == "" {
		if items == nil {
			items = []build{}
		}
		page(x, "builds", items)
		return
	}
	for _, item := range items {
		if item.ID == id {
			x.respond(200, item, "")
			return
		}
	}
	x.fail(errMissing)
}

func (x *request) serviceStatus() {
	state := x.service.builds.service()
	state.Port = x.port
	x.respond(200, state, state.ETag)
}
