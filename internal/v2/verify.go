package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"
)

const maxSnapshotBytes = 96 << 20

var errInvalidSnapshot = errors.New("invalid native snapshot")

type verificationIssue struct {
	Field string `json:"field"`
	Code  string `json:"code"`
}

type verification struct {
	BoardID  string              `json:"boardId"`
	At       string              `json:"at"`
	Valid    bool                `json:"valid"`
	Revision *uint64             `json:"revision"`
	Issues   []verificationIssue `json:"issues"`
}

type snapshotError struct {
	issue   verificationIssue
	message string
}

func (e snapshotError) Error() string {
	return errInvalidSnapshot.Error() + ": " + e.issue.Field + ": " + e.message
}

func (e snapshotError) Unwrap() error { return errInvalidSnapshot }

func invalidSnapshot(field, message string) error {
	return snapshotError{issue: verificationIssue{Field: field, Code: "integrity_failure"}, message: message}
}

// VerifyOffline reads the native file without opening the writer or changing durable state.
func VerifyOffline(root, boardID string) (verification, error) {
	id, ok := normalizeID(boardID)
	if !ok {
		return verification{}, errors.New("invalid board ID")
	}
	return verifySnapshot(filepath.Join(root, "boards", id+".json"), id, time.Now()), nil
}

func verifySnapshot(path, boardID string, at time.Time) verification {
	value, err := readSnapshot(path, boardID)
	return snapshotVerification(boardID, at, value, err)
}

func snapshotVerification(boardID string, at time.Time, value snapshot, err error) verification {
	result := verification{
		BoardID: boardID, At: at.UTC().Format("2006-01-02T15:04:05.000000Z"),
		Valid: err == nil, Issues: []verificationIssue{},
	}
	if err == nil {
		result.Revision = &value.Board.Revision
		return result
	}

	var validation snapshotError
	switch {
	case errors.As(err, &validation):
		result.Issues = append(result.Issues, validation.issue)
	case errors.Is(err, fs.ErrNotExist):
		result.Issues = append(result.Issues, verificationIssue{"snapshot", "required"})
	default:
		result.Issues = append(result.Issues, verificationIssue{"snapshot", "integrity_failure"})
	}
	return result
}

func readSnapshot(path, boardID string) (snapshot, error) {
	var value snapshot
	directory, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, fs.ErrNotExist) {
		return value, fs.ErrNotExist
	}
	if err != nil || !directory.IsDir() {
		return value, invalidSnapshot("snapshot", "Native snapshot directory is unavailable.")
	}
	initial, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return value, fs.ErrNotExist
	}
	if err != nil || !initial.Mode().IsRegular() {
		return value, invalidSnapshot("snapshot", "Native snapshot must be a regular file.")
	}
	if initial.Size() > maxSnapshotBytes {
		return value, invalidSnapshot("snapshot", "Native snapshot exceeds the size limit.")
	}

	file, err := os.Open(path)
	if err != nil {
		return value, invalidSnapshot("snapshot", "Native snapshot cannot be opened.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return value, invalidSnapshot("snapshot", "Native snapshot changed during open.")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil {
		return value, invalidSnapshot("snapshot", "Native snapshot cannot be read.")
	}
	if len(data) > maxSnapshotBytes {
		return value, invalidSnapshot("snapshot", "Native snapshot exceeds the size limit.")
	}
	return parseSnapshot(data, boardID)
}

func parseSnapshot(data []byte, boardID string) (snapshot, error) {
	var value snapshot
	if !utf8.Valid(data) || json.Unmarshal(data, &value) != nil {
		return snapshot{}, invalidSnapshot("snapshot", "Native snapshot is not valid UTF-8 JSON.")
	}
	if err := validateSnapshot(value, boardID); err != nil {
		return snapshot{}, err
	}

	canonical, err := marshalCanonical(value)
	if err != nil || !matchesSnapshotJSON(data, canonical) {
		return snapshot{}, invalidSnapshot("snapshot", "Native snapshot fields do not match the closed format.")
	}
	return value, nil
}

// Comparing against the typed representation enforces required fields, exact field
// names and scalar types. A member is consumed once, so duplicates at any depth fail.
func matchesSnapshotJSON(data, canonical []byte) bool {
	expectedDecoder := json.NewDecoder(bytes.NewReader(canonical))
	expectedDecoder.UseNumber()
	var expected any
	if expectedDecoder.Decode(&expected) != nil {
		return false
	}

	actual := json.NewDecoder(bytes.NewReader(data))
	actual.UseNumber()
	if !consumeSnapshotJSON(actual, expected) {
		return false
	}
	_, err := actual.Token()
	return errors.Is(err, io.EOF)
}

func consumeSnapshotJSON(actual *json.Decoder, expected any) bool {
	token, err := actual.Token()
	if err != nil {
		return false
	}
	switch value := expected.(type) {
	case map[string]any:
		if token != json.Delim('{') {
			return false
		}
		for actual.More() {
			key, err := actual.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			if !ok {
				return false
			}
			member, ok := value[name]
			if !ok || !consumeSnapshotJSON(actual, member) {
				return false
			}
			delete(value, name)
		}
		end, err := actual.Token()
		return err == nil && end == json.Delim('}') && len(value) == 0
	case []any:
		if token != json.Delim('[') {
			return false
		}
		for _, item := range value {
			if !consumeSnapshotJSON(actual, item) {
				return false
			}
		}
		end, err := actual.Token()
		return err == nil && end == json.Delim(']')
	default:
		return token == expected
	}
}

const maxSnapshotInteger = 9007199254740991

func validateSnapshot(value snapshot, boardID string) error {
	b := value.Board
	if value.Format != "localswim-snapshot" || value.FormatVersion != 1 {
		return invalidSnapshot("format", "Unsupported native snapshot format.")
	}
	if b.ID != boardID || !validSnapshotID(b.ID) {
		return invalidSnapshot("board.id", "Board identity does not match the snapshot filename.")
	}
	if !validSnapshotEntity(b.entity) || b.Revision > maxSnapshotInteger || b.ETag != etag(b.ID, b.Revision) {
		return invalidSnapshot("board", "Board version or entity metadata is invalid.")
	}
	if !snapshotText(b.Name, 1, 256) || !snapshotText(b.UserCSS, 0, 1048576) ||
		(b.Availability != "open" && b.Availability != "closed") {
		return invalidSnapshot("board", "Board metadata is invalid.")
	}
	if err := b.Notifications.validate(); err != nil {
		return invalidSnapshot("board.notifications", "Notification settings are invalid.")
	}
	if !snapshotOptionalText(b.Publishing.Repository, 2048) ||
		!snapshotOptionalText(b.Publishing.ErrorCode, 80) ||
		(b.Publishing.LastSuccessAt != nil && !validSnapshotTime(*b.Publishing.LastSuccessAt)) {
		return invalidSnapshot("board.publishing", "Publishing metadata is invalid.")
	}
	if value.Users == nil || value.Lanes == nil || value.Priorities == nil || value.Cards == nil ||
		value.Comments == nil || value.Relationships == nil || value.Attachments == nil || value.Activity == nil {
		return invalidSnapshot("snapshot", "Native collections must be arrays.")
	}
	if len(value.Comments)+len(value.Relationships)+len(value.Attachments) != 0 {
		return invalidSnapshot("snapshot", "Native snapshot contains unsupported resources.")
	}

	refs := snapshotReferences{
		ids:          map[string]bool{b.ID: true},
		users:        make(map[string]user, len(value.Users)),
		lanes:        make(map[string]lane, len(value.Lanes)),
		priorities:   make(map[string]bool, len(value.Priorities)),
		cards:        make(map[string]card, len(value.Cards)),
		capabilities: make(map[string]map[string]bool, len(value.Users)),
	}
	if err := refs.validateWorkflow(value); err != nil {
		return err
	}
	if err := refs.validateCards(value); err != nil {
		return err
	}
	if err := refs.validateAudit(value); err != nil {
		return err
	}
	counts, last := deriveAttention(value)
	if b.Attention != counts || !equalSnapshotTimes(b.LastCardEditAt, last) {
		return invalidSnapshot("board.attention", "Derived board metadata does not match cards and audit.")
	}
	return nil
}

type snapshotReferences struct {
	ids, priorities map[string]bool
	users           map[string]user
	lanes           map[string]lane
	cards           map[string]card
	capabilities    map[string]map[string]bool
}

func (refs snapshotReferences) claim(item entity) bool {
	if !validSnapshotEntity(item) || refs.ids[item.ID] {
		return false
	}
	refs.ids[item.ID] = true
	return true
}

func (refs snapshotReferences) validateWorkflow(value snapshot) error {
	b := value.Board
	for _, u := range value.Users {
		if !refs.claim(u.entity) || (u.Kind != "human" && u.Kind != "bot") ||
			!snapshotText(u.Label, 1, 256) || !snapshotText(u.Color, 0, 64) {
			return invalidSnapshot("users", "User identity or metadata is invalid.")
		}
		refs.users[u.ID] = u
	}
	if !matchSnapshotIDs(b.UserIDs, refs.users) {
		return invalidSnapshot("board.userIds", "Board users do not match native users.")
	}

	laneKeys := make(map[string]bool, len(value.Lanes))
	positions := make(map[uint64]bool, len(value.Lanes))
	completed := 0
	for _, l := range value.Lanes {
		if !refs.claim(l.entity) || l.BoardID != b.ID || !snapshotText(l.Key, 1, 128) ||
			!snapshotText(l.Label, 1, 256) || l.Position < 0 || laneKeys[l.Key] || positions[uint64(l.Position)] {
			return invalidSnapshot("lanes", "Lane identity or metadata is invalid.")
		}
		switch l.Role {
		case "completed":
			if l.Key != "completed" || l.Retired {
				return invalidSnapshot("lanes", "The Completed lane must be active with its protected key.")
			}
			completed++
		case "ordinary", "blocked", "needs_input", "ready_for_review":
			if l.Key == "completed" {
				return invalidSnapshot("lanes", "The Completed key is reserved.")
			}
		default:
			return invalidSnapshot("lanes", "Lane role is invalid.")
		}
		if l.ApprovalEnabled && l.Role != "ready_for_review" {
			return invalidSnapshot("lanes", "Approval is limited to Ready for Review lanes.")
		}
		refs.lanes[l.ID] = l
		laneKeys[l.Key] = true
		positions[uint64(l.Position)] = true
	}
	if completed != 1 || !matchSnapshotIDs(b.LaneIDs, refs.lanes) {
		return invalidSnapshot("board.laneIds", "Board lanes do not match native lanes or lack one Completed lane.")
	}

	priorityKeys := make(map[string]bool, len(value.Priorities))
	ranks := make(map[uint64]bool, len(value.Priorities))
	for _, p := range value.Priorities {
		if !refs.claim(p.entity) || p.BoardID != b.ID || !snapshotText(p.Key, 1, 128) ||
			!snapshotText(p.Label, 1, 256) || !snapshotText(p.Color, 0, 64) ||
			p.Rank < 0 || priorityKeys[p.Key] || ranks[uint64(p.Rank)] {
			return invalidSnapshot("priorities", "Priority identity or metadata is invalid.")
		}
		refs.priorities[p.ID] = true
		priorityKeys[p.Key] = true
		ranks[uint64(p.Rank)] = true
	}
	if !matchSnapshotIDs(b.PriorityIDs, refs.priorities) {
		return invalidSnapshot("board.priorityIds", "Board priorities do not match native priorities.")
	}
	return refs.validatePolicy(value.Policy, b)
}

func (refs snapshotReferences) validatePolicy(p policy, b board) error {
	if !refs.claim(p.entity) || p.ID != b.PolicyID || p.BoardID != b.ID || !refs.priorities[p.DefaultPriorityID] {
		return invalidSnapshot("policy", "Policy identity or default priority is invalid.")
	}
	if p.CreateGrants == nil || p.MoveGrants == nil || p.ResourceGrants == nil {
		return invalidSnapshot("policy", "Policy grant collections must be arrays.")
	}
	create := make(map[createGrant]bool, len(p.CreateGrants))
	for _, g := range p.CreateGrants {
		if refs.users[g.UserID].ID == "" || refs.lanes[g.LaneID].ID == "" || create[g] {
			return invalidSnapshot("policy.createGrants", "Create grant is duplicated or has an unknown reference.")
		}
		create[g] = true
	}
	move := make(map[moveGrant]bool, len(p.MoveGrants))
	for _, g := range p.MoveGrants {
		if refs.users[g.UserID].ID == "" || refs.lanes[g.FromLaneID].ID == "" || refs.lanes[g.ToLaneID].ID == "" ||
			g.FromLaneID == g.ToLaneID || move[g] {
			return invalidSnapshot("policy.moveGrants", "Move grant is duplicated or has an invalid reference.")
		}
		move[g] = true
	}
	for _, g := range p.ResourceGrants {
		if refs.users[g.UserID].ID == "" || refs.capabilities[g.UserID] != nil || g.Capabilities == nil {
			return invalidSnapshot("policy.resourceGrants", "Resource grant is duplicated or has an invalid actor or capability list.")
		}
		seen := make(map[string]bool, len(g.Capabilities))
		for _, capability := range g.Capabilities {
			if !isInitialCapability(capability) || seen[capability] ||
				(capability == "approve" && refs.users[g.UserID].Kind != "human") {
				return invalidSnapshot("policy.resourceGrants", "Resource capability is duplicated or unknown.")
			}
			seen[capability] = true
		}
		refs.capabilities[g.UserID] = seen
	}
	return nil
}

func (refs snapshotReferences) validateCards(value snapshot) error {
	tickets := make(map[uint64]bool, len(value.Cards))
	for _, c := range value.Cards {
		ticket := uint64(c.Ticket)
		l := refs.lanes[c.LaneID]
		if !refs.claim(c.entity) || c.BoardID != value.Board.ID || ticket == 0 ||
			ticket > maxSnapshotInteger || tickets[ticket] || l.ID == "" || (l.Retired && !c.Archived) ||
			!refs.priorities[c.PriorityID] || (c.OwnerID != nil && refs.users[*c.OwnerID].ID == "") {
			return invalidSnapshot("cards", "Card identity, ticket, or workflow reference is invalid.")
		}
		if !snapshotText(c.Title, 1, 1024) || !snapshotText(c.Description, 0, 4194304) ||
			c.ChildIDs == nil || c.CoverAttachmentID != nil || c.CommentCount != 0 || c.AttachmentCount != 0 ||
			c.BlockingDepth != 0 || c.BlockedCardCount != 0 {
			return invalidSnapshot("cards", "Card metadata is invalid or refers to unsupported resources.")
		}
		if (c.CompletedAt != nil) != (l.Role == "completed") ||
			(c.CompletedAt != nil && !validSnapshotTime(*c.CompletedAt)) {
			return invalidSnapshot("cards.completedAt", "Card completion timestamp does not match its lane.")
		}
		tickets[ticket] = true
		refs.cards[c.ID] = c
	}

	children := make(map[string][]string)
	for _, c := range value.Cards {
		if c.ParentID != nil {
			if refs.cards[*c.ParentID].ID == "" {
				return invalidSnapshot("cards.parentId", "Card parent does not exist.")
			}
			children[*c.ParentID] = append(children[*c.ParentID], c.ID)
		}
	}
	for _, c := range value.Cards {
		slices.Sort(children[c.ID])
		if !slices.Equal(c.ChildIDs, children[c.ID]) {
			return invalidSnapshot("cards.childIds", "Card child identities do not match parent links.")
		}
	}
	if refs.parentCycle() {
		return invalidSnapshot("cards.parentId", "Card parent links contain a cycle.")
	}
	return nil
}

// Each parent chain is checked once, even when many cards share its ancestors.
func (refs snapshotReferences) parentCycle() bool {
	const visiting, done = 1, 2
	marks := make(map[string]int, len(refs.cards))
	for id := range refs.cards {
		current := id
		for current != "" && marks[current] == 0 {
			marks[current] = visiting
			current = snapshotString(refs.cards[current].ParentID)
		}
		if marks[current] == visiting {
			return true
		}
		for current = id; marks[current] == visiting; current = snapshotString(refs.cards[current].ParentID) {
			marks[current] = done
		}
	}
	return false
}

func matchSnapshotIDs[T any](ids []string, expected map[string]T) bool {
	if ids == nil || len(ids) != len(expected) {
		return false
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if _, ok := expected[id]; !ok || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func validSnapshotID(id string) bool {
	canonical, ok := normalizeID(id)
	return ok && canonical == id
}

func validSnapshotEntity(item entity) bool {
	return validSnapshotID(item.ID) && snapshotETagPattern.MatchString(item.ETag) &&
		validSnapshotTime(item.CreatedAt) && validSnapshotTime(item.UpdatedAt) &&
		!item.UpdatedAt.Before(item.CreatedAt)
}

func validSnapshotTime(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0 && !value.IsZero() && value.Year() >= 0 && value.Year() <= 9999
}

func equalSnapshotTimes(a, b *time.Time) bool {
	return a == nil && b == nil || a != nil && b != nil && a.Equal(*b)
}

func snapshotText(value string, min, max int) bool {
	length := utf8.RuneCountInString(value)
	return utf8.ValidString(value) && length >= min && length <= max
}

func snapshotOptionalText(value *string, max int) bool {
	return value == nil || snapshotText(*value, 0, max)
}

func snapshotString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func equalSnapshotStrings(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

var snapshotETagPattern = regexp.MustCompile(`^"[A-Za-z0-9_-]{16,128}"$`)

type snapshotAuditField struct {
	resourceType, resourceID, name string
}

func (refs snapshotReferences) validateAudit(value snapshot) error {
	b := value.Board
	if len(value.Activity) == 0 {
		return invalidSnapshot("activity", "Board creation audit is required.")
	}

	created := value.Activity[0]
	if created.Revision != 1 || created.Operation != "create" ||
		created.ResourceType != "board" || created.ResourceID != b.ID ||
		!created.At.Equal(b.CreatedAt) || created.Field != nil || created.Before != nil || created.After != nil {
		return invalidSnapshot("activity", "Board creation audit is invalid.")
	}

	// Board creation fixes the initial notification settings, so both fields
	// must match replay even when no notification patch was recorded.
	disabled := "false"
	latest := map[snapshotAuditField]*string{
		{"board", b.ID, "notifications.enabled"}:        &disabled,
		{"board", b.ID, "notifications.targetThreadId"}: nil,
	}
	changed := make(map[snapshotAuditField]bool)
	var previousRevision uint64
	var previousActor string
	for index, event := range value.Activity {
		if !validSnapshotID(event.ID) || refs.ids[event.ID] ||
			event.BoardID != b.ID || !validSnapshotTime(event.At) {
			return invalidSnapshot("activity", "Audit identity or metadata is invalid.")
		}
		refs.ids[event.ID] = true
		trustedBootstrap := index == 0 && (event.ActorID == humanID || event.ActorID == botID)
		if refs.users[event.ActorID].ID == "" && !trustedBootstrap {
			return invalidSnapshot("activity.actorId", "Audit actor is unknown.")
		}
		if event.Revision < previousRevision || event.Revision > previousRevision+1 ||
			event.Revision > b.Revision || (event.Revision == previousRevision && event.ActorID != previousActor) {
			return invalidSnapshot("activity.revision", "Audit does not cover consecutive board revisions.")
		}
		if event.Revision != previousRevision {
			clear(changed)
		}
		previousRevision, previousActor = event.Revision, event.ActorID
		if index == 0 {
			continue
		}

		field := snapshotAuditField{event.ResourceType, event.ResourceID, snapshotString(event.Field)}
		if event.Operation != "patch" || event.Revision == 1 || changed[field] ||
			!refs.validAuditField(b.ID, event.ActorID, field, event.Before) ||
			!refs.validAuditField(b.ID, event.ActorID, field, event.After) ||
			equalSnapshotStrings(event.Before, event.After) {
			return invalidSnapshot("activity", "Audit patch actor, field, or values are invalid.")
		}
		if prior, seen := latest[field]; seen && !equalSnapshotStrings(prior, event.Before) {
			return invalidSnapshot("activity", "Audit patch values do not form a continuous history.")
		}
		changed[field] = true
		latest[field] = event.After
	}
	if previousRevision != b.Revision {
		return invalidSnapshot("activity.revision", "Audit does not reach the board revision.")
	}
	for field, after := range latest {
		if !equalSnapshotStrings(after, refs.currentAuditField(b, field)) {
			return invalidSnapshot("activity", "Audit patch history does not match current state.")
		}
	}
	return nil
}

func (refs snapshotReferences) validAuditField(boardID, actorID string, field snapshotAuditField, value *string) bool {
	capabilities := refs.capabilities[actorID]
	switch field.resourceType {
	case "board":
		if field.resourceID != boardID || !capabilities["read"] {
			return false
		}
		switch field.name {
		case "name":
			return capabilities["manage_board"] && snapshotText(snapshotString(value), 1, 256)
		case "notifications.enabled":
			return capabilities["manage_notifications"] &&
				(snapshotString(value) == "true" || snapshotString(value) == "false")
		case "notifications.targetThreadId":
			return capabilities["manage_notifications"] && (value == nil || threadIDPattern.MatchString(*value))
		}
	case "card":
		return capabilities["manage_relationships"] && refs.cards[field.resourceID].ID != "" && field.name == "parentId" &&
			(value == nil || *value != field.resourceID && refs.cards[*value].ID != "")
	}
	return false
}

func (refs snapshotReferences) currentAuditField(b board, field snapshotAuditField) *string {
	if field.resourceType == "card" {
		return refs.cards[field.resourceID].ParentID
	}
	switch field.name {
	case "name":
		return &b.Name
	case "notifications.enabled":
		enabled := strconv.FormatBool(b.Notifications.Enabled)
		return &enabled
	default:
		return b.Notifications.TargetThreadID
	}
}
