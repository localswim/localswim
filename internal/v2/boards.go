package v2

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const maxBoardCSSLength = 1048576

func (x *request) listBoards() {
	store := x.service.store
	rows := make([]boardSummary, 0)
	for _, s := range store.list() {
		if row, visible := store.boardSummary(s.Board.ID, x.actor); visible {
			rows = append(rows, row)
		}
	}
	rows = store.appendFailedBoardSummaries(rows, x.actor)
	sortSummaries(rows)
	page(x, "boards", rows)
}

func (x *request) createBoard() {
	obj, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	if err = rejectFields(obj, []string{"id", "name", "workflow", "default_owner_id", "actor_presentation", "user_css"}, false); err != nil {
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
	if raw, ok := obj["user_css"]; ok {
		s.Board.UserCSS, err = exactCardText(raw, "user_css", 0, maxBoardCSSLength)
		if err != nil {
			x.fail(err)
			return
		}
	}
	if raw, ok := obj["actor_presentation"]; ok {
		s.Board.ActorPresentation, err = readActorPresentation(raw)
		if err != nil {
			x.fail(err)
			return
		}
	}
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
	x.respond(http.StatusCreated, s.Board, s.Board.entityTag())
}

func (x *request) boardResource(id string) {
	if x.service.store.canSeeFailedBoard(id, x.actor) {
		x.fail(errInvalidSnapshot)
		return
	}
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
	if strings.EqualFold(view, "ticket") {
		x.boardTicket(s)
		return
	}
	if strings.EqualFold(view, "lanes") {
		if !x.useBoard(id) {
			return
		}
		x.boardLanes(s)
		return
	}
	if view != "" && !strings.EqualFold(view, "summary") {
		x.fail(invalid("view", "This board view is unavailable."))
		return
	}
	value := x.service.store.boardPublishingStatus(s.Board)
	x.respondPublishing(200, value, value.entityTag(), value.Publishing)
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
	if err = rejectFields(obj, []string{"name", "notifications", "default_owner_id", "publishing", "availability", "actor_presentation", "user_css"}, false); err != nil {
		x.fail(err)
		return
	}
	if _, ok := obj["user_ids"]; ok {
		x.fail(invalid("user_ids", "This patch field is not available."))
		return
	}
	name, hasName := obj["name"]
	defaultOwner, hasDefaultOwner := obj["default_owner_id"]
	raw, hasNotifications := obj["notifications"]
	publishingInput, hasPublishing := obj["publishing"]
	availabilityInput, hasAvailability := obj["availability"]
	presentationInput, hasPresentation := obj["actor_presentation"]
	cssInput, hasCSS := obj["user_css"]
	managesBoard := hasName || hasDefaultOwner || hasPresentation || hasCSS
	hasSettings := managesBoard || hasNotifications || hasPublishing
	if !hasSettings && !hasAvailability {
		x.fail(invalid("body", "A supported change is required."))
		return
	}
	authorized := func(s snapshot) bool {
		return s.can(x.actor, "read") && (!managesBoard || s.can(x.actor, "manage_board")) &&
			(!hasNotifications || s.can(x.actor, "manage_notifications")) && (!hasPublishing || s.can(x.actor, "publish")) &&
			(!hasAvailability || s.can(x.actor, "close_board"))
	}
	if !authorized(s) {
		x.fail(errForbidden)
		return
	}
	var availability string
	if hasAvailability {
		value, err := textField(availabilityInput, "availability", 4, 6)
		if err != nil {
			x.fail(err)
			return
		}
		availability = strings.ToLower(value)
		if availability != "open" && availability != "closed" {
			x.fail(invalid("availability", "Expected open or closed."))
			return
		}
	}
	next, err := x.service.store.patchBoard(s.Board.ID, availability, matchingBoardTag(expected), hasSettings, authorized, func(s *snapshot) error {
		now := canonicalTime(time.Now())
		appendChange := func(field string, before, after *string) {
			s.Activity = append(s.Activity, activity{ID: newOperationID(), BoardID: s.Board.ID, Revision: s.Board.Revision + 1, At: now,
				ActorID: x.actor, Operation: "patch", ResourceType: "board", ResourceID: s.Board.ID, Field: &field, Before: before, After: after})
		}
		if hasAvailability && availability != s.Board.Availability {
			before := s.Board.Availability
			appendChange("availability", &before, &availability)
			s.Board.Availability = availability
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
		if hasPresentation {
			value, err := readActorPresentation(presentationInput)
			if err != nil {
				return err
			}
			before := s.Board.ActorPresentation
			if value != before {
				appendChange("actor_presentation", before.auditValue(), value.auditValue())
				s.Board.ActorPresentation = value
			}
		}
		if hasCSS {
			value, err := exactCardText(cssInput, "user_css", 0, maxBoardCSSLength)
			if err != nil {
				return err
			}
			before := s.Board.UserCSS
			if value != before {
				appendChange("user_css", &before, &value)
				s.Board.UserCSS = value
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
		if hasPublishing {
			current := s.Board.Publishing
			updated, err := patchPublishing(current, publishingInput)
			if err != nil {
				return err
			}
			if current.Enabled != updated.Enabled {
				before, after := strconv.FormatBool(current.Enabled), strconv.FormatBool(updated.Enabled)
				appendChange("publishing.enabled", &before, &after)
			}
			if !sameText(current.Repository, updated.Repository) {
				appendChange("publishing.repository", current.Repository, updated.Repository)
			}
			s.Board.Publishing = updated
		}
		return nil
	})
	if err != nil {
		x.fail(err)
		return
	}
	value := x.service.store.boardPublishingStatus(next.Board)
	x.w.Header().Set("X-Board-ETag", value.entityTag())
	x.respond(200, value, value.entityTag())
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
	if !x.useBoard(id) {
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
			x.respond(200, s.Policy, s.Policy.entityTag())
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
	x.w.Header().Set("Vary", x.w.Header().Get("Vary")+", Authorization")
	var hidden func(string) bool
	if x.actor != humanID {
		hidden = x.service.store.isFailedBoard
	}
	if x.r.Method == "GET" || x.r.Method == "HEAD" {
		result, err := x.service.workspace.read()
		if err != nil {
			x.fail(err)
			return
		}
		result, tag := workspaceHTTPView(result, hidden)
		x.respondPublishing(200, result, tag, result.Publishing)
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
	if _, configuresPublishing := fields["publishing"]; configuresPublishing && x.actor != humanID {
		x.fail(errForbidden)
		return
	}
	result, err := x.service.workspace.patch(func(current workspace) bool {
		_, tag := workspaceHTTPView(current, hidden)
		return matchesStrongTag(expected, tag)
	}, fields, func(id string) bool {
		if x.service.store.canSeeFailedBoard(id, x.actor) {
			return true
		}
		s, ok := x.service.store.accepted(id)
		return ok && s.can(x.actor, "read")
	}, hidden)
	if err != nil {
		x.fail(err)
		return
	}
	result, tag := workspaceHTTPView(result, hidden)
	x.respond(200, result, tag)
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
	state := x.service.lifecycle.snapshot(x.service.builds.service())
	state.Port = x.port
	x.respond(200, state, state.entityTag())
}
