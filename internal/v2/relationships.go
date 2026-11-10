package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

type relationshipLink struct {
	SourceCardID string `json:"source_card_id"`
	TargetCardID string `json:"target_card_id"`
	Kind         string `json:"kind"`
}

type relationship struct {
	ID        string    `json:"id"`
	Revision  uint64    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	BoardID   string    `json:"board_id"`
	relationshipLink
}

func (r relationship) entityTag() string { return etag(r.ID, r.Revision) }

type relationshipSeed struct {
	targetID, kind string
}

func validRelationshipKind(kind string) bool {
	switch kind {
	case "blocks", "duplicates", "references", "relates_to":
		return true
	}
	return false
}

func readRelationshipKind(raw json.RawMessage) (string, error) {
	kind, err := textField(raw, "kind", 1, 13)
	if err != nil {
		return "", err
	}
	for _, c := range kind {
		if c > 127 {
			return "", invalid("kind", "Unknown relationship kind.")
		}
	}
	kind = strings.ToLower(kind)
	switch kind {
	case "blocked_by", "duplicated_by", "referenced_by":
		return kind, nil
	}
	if !validRelationshipKind(kind) {
		return "", invalid("kind", "Unknown relationship kind.")
	}
	return kind, nil
}

func relationshipFromInput(source, target, kind string) relationshipLink {
	switch kind {
	case "blocked_by":
		return relationshipLink{target, source, "blocks"}
	case "duplicated_by":
		return relationshipLink{target, source, "duplicates"}
	case "referenced_by":
		return relationshipLink{target, source, "references"}
	default:
		return relationshipLink{source, target, kind}
	}
}

func (link relationshipLink) key() relationshipLink {
	if link.Kind == "relates_to" && link.TargetCardID < link.SourceCardID {
		link.SourceCardID, link.TargetCardID = link.TargetCardID, link.SourceCardID
	}
	return link
}

func readRelationshipSeeds(raw json.RawMessage) ([]relationshipSeed, error) {
	var objects []json.RawMessage
	if json.Unmarshal(raw, &objects) != nil || objects == nil || len(objects) > 1000 {
		return nil, invalid("relationships", "Expected an array of at most 1000 relationships.")
	}
	seeds := make([]relationshipSeed, 0, len(objects))
	for i, object := range objects {
		seed, err := readRelationshipSeed(object)
		if err != nil {
			var input invalidField
			if !errors.As(err, &input) {
				return nil, err
			}
			return nil, invalid(fmt.Sprintf("relationships[%d].%s", i, input.Field), input.Message)
		}
		seeds = append(seeds, seed)
	}
	return seeds, nil
}

func readRelationshipSeed(raw json.RawMessage) (relationshipSeed, error) {
	var seed relationshipSeed
	fields, err := decodeObject(raw)
	if err != nil {
		return seed, err
	}
	if err := rejectFields(fields, []string{"target_card_id", "kind"}, false); err != nil {
		return seed, err
	}
	seed.targetID, err = inputID(fields["target_card_id"], "target_card_id")
	if err != nil {
		return seed, err
	}
	seed.kind, err = readRelationshipKind(fields["kind"])
	return seed, err
}

func relationshipCards(state *snapshot) map[string]*card {
	cards := make(map[string]*card, len(state.Cards))
	for i := range state.Cards {
		cards[state.Cards[i].ID] = &state.Cards[i]
	}
	return cards
}

// applyRelationshipSeeds adds links to the isolated card-creation candidate.
// All seed endpoints and duplicate keys are checked before any link is applied.
func applyRelationshipSeeds(state *snapshot, sourceID string, seeds []relationshipSeed, actor string, now time.Time) error {
	if len(seeds) == 0 {
		return nil
	}
	if !state.can(actor, "manage_relationships") {
		return errForbidden
	}
	cards := relationshipCards(state)
	keys := make(map[relationshipLink]bool, len(seeds))
	links := make([]relationshipLink, 0, len(seeds))
	for i, seed := range seeds {
		if cards[seed.targetID] == nil || sourceID == seed.targetID {
			return invalid(fmt.Sprintf("relationships[%d].target_card_id", i), "Expected a different card on this board.")
		}
		link := relationshipFromInput(sourceID, seed.targetID, seed.kind)
		key := link.key()
		if keys[key] {
			return invalid(fmt.Sprintf("relationships[%d]", i), "This relationship is repeated.")
		}
		keys[key] = true
		links = append(links, link)
	}
	for _, link := range links {
		addRelationship(state, cards, newID(), link, actor, now)
	}
	return nil
}

func addRelationship(state *snapshot, cards map[string]*card, id string, link relationshipLink, actor string, now time.Time) {
	state.Relationships = append(state.Relationships, relationship{
		ID: id, Revision: state.Board.Revision + 1, CreatedAt: now, UpdatedAt: now,
		BoardID: state.Board.ID, relationshipLink: link,
	})
	after := relationshipAuditValue(link)
	recordRelationshipEdit(state, cards, id, "create", nil, &after, link, actor, now)
}

func recordRelationshipEdit(state *snapshot, cards map[string]*card, id, operation string, before, after *string,
	link relationshipLink, actor string, now time.Time) {
	revision := state.Board.Revision + 1
	for _, endpoint := range []*card{cards[link.SourceCardID], cards[link.TargetCardID]} {
		endpoint.Revision = revision
		endpoint.UpdatedAt = maxCardTime(now, endpoint.UpdatedAt)
	}
	state.Activity = append(state.Activity, activity{
		ID: newID(), BoardID: state.Board.ID, Revision: revision, At: now, ActorID: actor,
		Operation: operation, ResourceType: "relationship", ResourceID: id, Before: before, After: after,
	})
}

func relationshipAuditValue(link relationshipLink) string {
	data, _ := json.Marshal(link) // A closed struct containing only canonical ASCII strings.
	return string(data)
}

func (x *request) relationshipsCollection() {
	if x.r.Method == http.MethodPost {
		x.createRelationship()
		return
	}
	x.listRelationships()
}

func (x *request) listRelationships() {
	boardID, err := singleQuery(x.r, "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	boardID, ok := normalizeID(boardID)
	if !ok {
		x.fail(invalid("board_id", "Expected a UUIDv4."))
		return
	}
	known, exists := x.service.store.accepted(boardID)
	if !exists || !slices.Contains(known.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !known.can(x.actor, "read") {
		x.fail(errForbidden)
		return
	}
	cardID, err := singleQuery(x.r, "card_id")
	if err != nil {
		x.fail(err)
		return
	}
	query := x.r.URL.Query()
	if query.Has("card_id") {
		cardID, ok = normalizeID(cardID)
		if !ok {
			x.fail(invalid("card_id", "Expected a UUIDv4."))
			return
		}
	}
	for _, field := range []string{"limit", "cursor"} {
		if query.Has(field) && query.Get(field) == "" {
			x.fail(invalid(field, "Expected a nonempty value."))
			return
		}
	}
	state, err := x.service.store.readback(boardID)
	if err != nil {
		x.fail(err)
		return
	}
	if !state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	if cardID != "" && !slices.ContainsFunc(state.Cards, func(item card) bool { return item.ID == cardID }) {
		x.fail(errMissing)
		return
	}
	var rows []relationship
	for _, value := range state.Relationships {
		if cardID == "" || value.SourceCardID == cardID || value.TargetCardID == cardID {
			rows = append(rows, value)
		}
	}
	page(x, "relationships", rows)
}

func relationshipIndex(state *snapshot, id string) int {
	return slices.IndexFunc(state.Relationships, func(item relationship) bool { return item.ID == id })
}

func relationshipKeyExists(state *snapshot, link relationshipLink, except string) bool {
	key := link.key()
	return slices.ContainsFunc(state.Relationships, func(item relationship) bool {
		return item.ID != except && item.key() == key
	})
}

func (x *request) relationshipResource(id string) {
	for _, state := range x.service.store.list() {
		if !slices.Contains(state.Board.UserIDs, x.actor) || relationshipIndex(&state, id) < 0 {
			continue
		}
		switch x.r.Method {
		case http.MethodPatch, http.MethodDelete:
			if !state.can(x.actor, "manage_relationships") {
				x.fail(errForbidden)
				return
			}
			x.changeRelationship(state.Board.ID, id)
		default:
			if !state.can(x.actor, "read") {
				x.fail(errForbidden)
				return
			}
			x.readRelationship(state.Board.ID, id)
		}
		return
	}
	if x.r.Method == http.MethodGet || x.r.Method == http.MethodHead {
		if boardID := x.service.store.fencedBoard(); boardID != "" {
			if state, ok := x.service.store.accepted(boardID); ok && state.can(x.actor, "read") {
				x.readRelationship(boardID, id)
				return
			}
		}
	}
	x.fail(errMissing)
}

func (x *request) readRelationship(boardID, id string) {
	state, err := x.service.store.readback(boardID)
	if err != nil {
		x.fail(err)
		return
	}
	i := relationshipIndex(&state, id)
	if !state.can(x.actor, "read") || i < 0 {
		x.fail(errMissing)
		return
	}
	item := state.Relationships[i]
	x.respond(http.StatusOK, item, item.entityTag())
}

func relationshipBoardTag(tag, field string) (string, error) {
	if !snapshotETagPattern.MatchString(tag) {
		return "", invalid(field, "Expected one canonical entity tag.")
	}
	return tag, nil
}

func (x *request) requireRelationshipBoardTag(fields map[string]json.RawMessage) (string, error) {
	const field = "expected_board_etag"
	var tag string
	if x.r.Method == http.MethodDelete {
		const header = "X-Expected-Board-ETag"
		if len(x.r.Header.Values(header)) == 0 {
			return "", errRelationshipBoardTagRequired
		}
		return relationshipBoardTag(x.r.Header.Get(header), header)
	}
	raw, exists := fields[field]
	if !exists {
		return "", errRelationshipBoardTagRequired
	}
	if err := json.Unmarshal(raw, &tag); err != nil {
		return "", invalid(field, "Expected one canonical entity tag.")
	}
	return relationshipBoardTag(tag, field)
}

var errRelationshipBoardTagRequired = errors.New("relationship board precondition required")

func (x *request) failRelationshipInput(err error) {
	if errors.Is(err, errRelationshipBoardTagRequired) {
		field := "expected_board_etag"
		if x.r.Method == http.MethodDelete {
			field = "X-Expected-Board-ETag"
		}
		x.problem(http.StatusPreconditionRequired, "precondition_required", field+" is required.", "", nil)
		return
	}
	x.fail(err)
}

type relationshipCreation struct {
	id, boardID, boardTag string
	link                  relationshipLink
}

func (x *request) readRelationshipCreation() (relationshipCreation, error) {
	var in relationshipCreation
	fields, err := x.body()
	if err != nil {
		return in, err
	}
	allowed := []string{"id", "board_id", "expected_board_etag", "source_card_id", "target_card_id", "kind"}
	if err := rejectFields(fields, allowed, false); err != nil {
		return in, err
	}
	if raw, ok := fields["id"]; ok {
		in.id, err = inputID(raw, "id")
		if err != nil {
			return in, err
		}
	}
	in.boardID, err = inputID(fields["board_id"], "board_id")
	if err != nil {
		return in, err
	}
	in.boardTag, err = x.requireRelationshipBoardTag(fields)
	if err != nil {
		return in, err
	}
	source, err := inputID(fields["source_card_id"], "source_card_id")
	if err != nil {
		return in, err
	}
	target, err := inputID(fields["target_card_id"], "target_card_id")
	if err != nil {
		return in, err
	}
	kind, err := readRelationshipKind(fields["kind"])
	in.link = relationshipFromInput(source, target, kind)
	return in, err
}

func (x *request) createRelationship() {
	in, err := x.readRelationshipCreation()
	if err != nil {
		x.failRelationshipInput(err)
		return
	}
	state, exists := x.service.store.accepted(in.boardID)
	if !exists || !slices.Contains(state.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "manage_relationships") {
		x.fail(errForbidden)
		return
	}
	if in.id == "" {
		in.id = newID()
	}
	next, err := x.service.store.update(in.boardID, in.boardTag, func(s *snapshot) error {
		return in.apply(s, x.actor)
	})
	if err != nil {
		x.fail(err)
		return
	}
	value := next.Relationships[len(next.Relationships)-1]
	x.w.Header().Set("Location", Prefix+"/relationships/"+value.ID)
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	x.respond(http.StatusCreated, value, value.entityTag())
}

func (in relationshipCreation) apply(state *snapshot, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !state.can(actor, "manage_relationships") {
		return errForbidden
	}
	cards := relationshipCards(state)
	if cards[in.link.SourceCardID] == nil || cards[in.link.TargetCardID] == nil {
		return errMissing
	}
	if in.link.SourceCardID == in.link.TargetCardID {
		return invalid("target_card_id", "Expected a different card on this board.")
	}
	if snapshotIdentityExists(state, in.id) || relationshipKeyExists(state, in.link, "") {
		return errIdentityConflict
	}
	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	addRelationship(state, cards, in.id, in.link, actor, now)
	return nil
}

func (x *request) changeRelationship(boardID, id string) {
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	var fields map[string]json.RawMessage
	var kind string
	if x.r.Method == http.MethodPatch {
		fields, err = x.body()
		if err == nil {
			err = rejectFields(fields, []string{"expected_board_etag", "kind"}, false)
		}
		if err != nil {
			x.fail(err)
			return
		}
	}
	boardTag, err := x.requireRelationshipBoardTag(fields)
	if err != nil {
		x.failRelationshipInput(err)
		return
	}
	if x.r.Method == http.MethodPatch {
		kind, err = readRelationshipKind(fields["kind"])
		if err != nil {
			x.fail(err)
			return
		}
	}
	next, err := x.service.store.update(boardID, boardTag, func(s *snapshot) error {
		return editRelationship(s, id, expected, kind, x.actor)
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	if x.r.Method == http.MethodDelete {
		x.w.WriteHeader(http.StatusNoContent)
		return
	}
	value := next.Relationships[relationshipIndex(&next, id)]
	x.respond(http.StatusOK, value, value.entityTag())
}

// An empty kind is removal. The caller supplies the isolated candidate.
func editRelationship(state *snapshot, id, expected, kind, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !state.can(actor, "manage_relationships") {
		return errForbidden
	}
	i := relationshipIndex(state, id)
	if i < 0 {
		return errMissing
	}
	item := &state.Relationships[i]
	if !matchesStrongTag(expected, item.entityTag()) {
		return errStale
	}
	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	before := relationshipAuditValue(item.relationshipLink)
	if kind == "" {
		recordRelationshipEdit(state, relationshipCards(state), id, "remove", &before, nil, item.relationshipLink, actor, now)
		state.Relationships = slices.Delete(state.Relationships, i, i+1)
		return nil
	}
	link := relationshipFromInput(item.SourceCardID, item.TargetCardID, kind)
	if item.relationshipLink == link {
		return nil
	}
	if relationshipKeyExists(state, link, id) {
		return errIdentityConflict
	}
	item.relationshipLink = link
	item.Revision = state.Board.Revision + 1
	item.UpdatedAt = now
	after := relationshipAuditValue(link)
	recordRelationshipEdit(state, relationshipCards(state), id, "patch", &before, &after, link, actor, now)
	return nil
}

func (refs snapshotReferences) validateRelationships(value snapshot) error {
	keys := make(map[relationshipLink]bool, len(value.Relationships))
	for _, item := range value.Relationships {
		key := item.key()
		if !validSnapshotID(item.ID) || refs.ids[item.ID] || item.Revision == 0 ||
			item.Revision > value.Board.Revision || item.Revision > maxSnapshotInteger ||
			!validSnapshotTime(item.CreatedAt) || !validSnapshotTime(item.UpdatedAt) ||
			item.UpdatedAt.Before(item.CreatedAt) || item.BoardID != value.Board.ID ||
			!refs.validRelationshipLink(item.relationshipLink) || keys[key] {
			return invalidSnapshot("relationships", "Relationship identity, endpoints, or kind are invalid or repeated.")
		}
		refs.ids[item.ID] = true
		keys[key] = true
	}
	return nil
}

func (refs snapshotReferences) validRelationshipLink(link relationshipLink) bool {
	return link.SourceCardID != link.TargetCardID && refs.cards[link.SourceCardID].ID != "" &&
		refs.cards[link.TargetCardID].ID != "" && validRelationshipKind(link.Kind)
}

func (refs snapshotReferences) validateRelationshipAudit(value snapshot, cardCreations map[string]uint64) error {
	active := make(map[string]relationship, len(value.Relationships))
	for _, item := range value.Relationships {
		active[item.ID] = item
	}
	replay := make(map[string]relationship, len(active))
	keys := make(map[relationshipLink]bool, len(active))
	revisions := make(map[string]uint64, len(active))
	seenCards := make(map[string]bool, len(cardCreations))
	for _, event := range value.Activity {
		if event.ResourceType == "card" && event.Operation == "create" {
			seenCards[event.ResourceID] = true
		}
		if event.ResourceType != "relationship" {
			continue
		}
		id := event.ResourceID
		if !validSnapshotID(id) || (refs.ids[id] && active[id].ID == "") ||
			!refs.capabilities[event.ActorID]["manage_relationships"] || event.Field != nil ||
			event.Revision < 2 || revisions[id] >= event.Revision {
			return invalidSnapshot("activity", "Relationship audit identity, actor, or revision is invalid.")
		}
		item, wasActive := replay[id]
		if wasActive {
			if snapshotString(event.Before) != relationshipAuditValue(item.relationshipLink) || event.At.Before(item.UpdatedAt) {
				return invalidSnapshot("activity", "Relationship audit does not form a continuous history.")
			}
			delete(keys, item.key())
		}
		if event.Operation == "remove" && wasActive && event.After == nil {
			if err := refs.validateRelationshipEndpointVersions(item.relationshipLink, event); err != nil {
				return err
			}
			delete(replay, id)
			revisions[id] = event.Revision
			continue
		}
		var link relationshipLink
		if event.After == nil || len(*event.After) > 140 || json.Unmarshal([]byte(*event.After), &link) != nil ||
			*event.After != relationshipAuditValue(link) || !refs.validRelationshipLink(link) || keys[link.key()] {
			return invalidSnapshot("activity", "Relationship audit link is invalid or repeated.")
		}
		switch event.Operation {
		case "create":
			if revisions[id] != 0 || event.Before != nil || !seenCards[link.SourceCardID] || !seenCards[link.TargetCardID] ||
				(cardCreations[link.SourceCardID] == event.Revision && cardCreations[link.TargetCardID] == event.Revision) {
				return invalidSnapshot("activity", "Relationship creation audit is invalid.")
			}
			item = relationship{ID: id, Revision: event.Revision, CreatedAt: event.At, UpdatedAt: event.At,
				BoardID: value.Board.ID, relationshipLink: link}
		case "patch":
			if !wasActive || item.key() == link.key() || !sameRelationshipEndpoints(item.relationshipLink, link) {
				return invalidSnapshot("activity", "Relationship replacement audit is invalid.")
			}
			item.relationshipLink = link
			item.UpdatedAt = event.At
			item.Revision = event.Revision
		default:
			return invalidSnapshot("activity", "Relationship audit operation is invalid.")
		}
		if err := refs.validateRelationshipEndpointVersions(link, event); err != nil {
			return err
		}
		revisions[id] = event.Revision
		keys[link.key()] = true
		replay[id] = item
	}
	if len(replay) != len(active) {
		return invalidSnapshot("activity", "Relationship audit does not match the active relationships.")
	}
	for id, item := range replay {
		current := active[id]
		if current.ID != id || current.Revision != item.Revision || current.relationshipLink != item.relationshipLink ||
			!current.CreatedAt.Equal(item.CreatedAt) || !current.UpdatedAt.Equal(item.UpdatedAt) {
			return invalidSnapshot("activity", "Relationship audit does not match the active relationships.")
		}
	}
	return nil
}

func (refs snapshotReferences) validateRelationshipEndpointVersions(link relationshipLink, event activity) error {
	for _, id := range []string{link.SourceCardID, link.TargetCardID} {
		endpoint := refs.cards[id]
		if endpoint.Revision < event.Revision || endpoint.UpdatedAt.Before(event.At) {
			return invalidSnapshot("activity", "Relationship audit is newer than an endpoint card version.")
		}
	}
	return nil
}

func sameRelationshipEndpoints(before, after relationshipLink) bool {
	return before.SourceCardID == after.SourceCardID && before.TargetCardID == after.TargetCardID ||
		before.SourceCardID == after.TargetCardID && before.TargetCardID == after.SourceCardID
}
