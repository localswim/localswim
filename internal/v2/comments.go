package v2

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

var errCommentCardETagRequired = errors.New("comment creation requires expected_card_etag")

type commentCreation struct {
	id, cardID, expected, text string
}

func (x *request) commentsCollection() {
	if x.r.Method == http.MethodPost {
		x.createComment()
		return
	}
	x.listComments()
}

func (x *request) commentResource(id string) {
	for _, state := range x.service.store.list() {
		if !slices.Contains(state.Board.UserIDs, x.actor) {
			continue
		}
		for _, value := range state.Comments {
			if value.ID == id {
				if !state.can(x.actor, "read") {
					x.fail(errForbidden)
					return
				}
				x.respond(http.StatusOK, value, value.entityTag())
				return
			}
		}
	}
	x.fail(errMissing)
}

func (x *request) listComments() {
	boardID, err := singleQuery(x.r, "board_id")
	if err != nil {
		x.fail(err)
		return
	}
	boardID, valid := normalizeID(boardID)
	if !valid {
		x.fail(invalid("board_id", "Expected a UUIDv4."))
		return
	}
	state, exists := x.service.store.accepted(boardID)
	if !exists || !slices.Contains(state.Board.UserIDs, x.actor) {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "read") {
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
		cardID, valid = normalizeID(cardID)
		if !valid {
			x.fail(invalid("card_id", "Expected a UUIDv4."))
			return
		}
		if !slices.ContainsFunc(state.Cards, func(c card) bool { return c.ID == cardID }) {
			x.fail(errMissing)
			return
		}
	}
	for _, field := range []string{"limit", "cursor"} {
		if query.Has(field) && query.Get(field) == "" {
			x.fail(invalid(field, "Expected a nonempty value."))
			return
		}
	}

	rows := []comment{}
	for _, value := range state.Comments {
		if cardID == "" || value.CardID == cardID {
			rows = append(rows, value)
		}
	}
	// Preserve newest commit order when server timestamps are equal.
	slices.Reverse(rows)
	slices.SortStableFunc(rows, func(a, b comment) int { return b.CreatedAt.Compare(a.CreatedAt) })
	page(x, "comments", rows)
}

func readCommentCreation(fields map[string]json.RawMessage) (commentCreation, error) {
	var in commentCreation
	if err := rejectFields(fields, []string{"id", "card_id", "expected_card_etag", "text"}, false); err != nil {
		return in, err
	}
	for key := range fields {
		if strings.EqualFold(key, "author_id") {
			return in, invalid("author_id", "Protected member.")
		}
	}
	var err error
	in.cardID, err = inputID(fields["card_id"], "card_id")
	if err != nil {
		return in, err
	}
	if raw, exists := fields["id"]; exists {
		in.id, err = inputID(raw, "id")
		if err != nil {
			return in, err
		}
	}
	raw, exists := fields["expected_card_etag"]
	if !exists {
		return in, errCommentCardETagRequired
	}
	in.expected, err = textField(raw, "expected_card_etag", 2, 1024)
	if err != nil {
		return in, err
	}
	if tags, ok := parseTagList(in.expected); !ok || len(tags) != 1 || tags[0].weak || tags[0].opaque != in.expected {
		return in, invalid("expected_card_etag", "Expected one strong entity tag.")
	}
	in.text, err = exactCardText(fields["text"], "text", 1, 4194304)
	return in, err
}

func (x *request) createComment() {
	fields, err := x.body()
	if err != nil {
		x.fail(err)
		return
	}
	in, err := readCommentCreation(fields)
	if err != nil {
		x.fail(err)
		return
	}
	current, _, exists := x.findCard(in.cardID)
	if !exists {
		x.fail(errMissing)
		return
	}
	if !current.can(x.actor, "comment") {
		x.fail(errForbidden)
		return
	}
	if in.id == "" {
		in.id = newID()
	}
	next, err := x.service.store.updateChecked(current.Board.ID, nil, func(state *snapshot) error {
		return in.apply(state, x.actor)
	})
	if err != nil {
		x.fail(err)
		return
	}
	value := next.Comments[len(next.Comments)-1]
	x.w.Header().Set("Location", Prefix+"/comments/"+value.ID)
	x.w.Header().Set("X-Board-ETag", next.Board.ETag)
	x.respond(http.StatusCreated, value, value.entityTag())
}

func (in commentCreation) apply(state *snapshot, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !state.can(actor, "comment") {
		return errForbidden
	}
	index := slices.IndexFunc(state.Cards, func(c card) bool { return c.ID == in.cardID })
	if index < 0 {
		return errMissing
	}
	parent := &state.Cards[index]
	if !matchesStrongTag(in.expected, parent.entityTag()) {
		return errStale
	}
	if snapshotIdentityExists(state, in.id) {
		return errIdentityConflict
	}

	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	revision := state.Board.Revision + 1
	value := comment{ID: in.id, CreatedAt: now, UpdatedAt: now, BoardID: state.Board.ID,
		CardID: parent.ID, Text: in.text, AuthorID: actor}
	state.Comments = append(state.Comments, value)
	parent.CommentCount++
	parent.Revision = revision
	parent.UpdatedAt = now
	state.Activity = append(state.Activity, activity{ID: newID(), BoardID: state.Board.ID,
		Revision: revision, At: now, ActorID: actor, Operation: "comment", ResourceType: "comment", ResourceID: value.ID})
	return nil
}
