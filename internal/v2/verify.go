package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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
	BoardID  string              `json:"board_id"`
	At       string              `json:"at"`
	Valid    bool                `json:"valid"`
	Revision *uint64             `json:"revision"`
	Issues   []verificationIssue `json:"issues"`
	Recovery *recoveryReport     `json:"recovery,omitempty"`
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
		BoardID: boardID, At: canonicalTime(at).Format(timestampLayout),
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
	saved, err := readSnapshotFile(path)
	if err != nil {
		return snapshot{}, err
	}
	return parseSnapshot(saved.data, boardID)
}

// snapshotFile captures bytes and metadata from the same opened file.
type snapshotFile struct {
	data []byte
	info os.FileInfo
}

func readSnapshotFile(path string) (snapshotFile, error) {
	directory, err := os.Lstat(filepath.Dir(path))
	if errors.Is(err, fs.ErrNotExist) {
		return snapshotFile{}, fs.ErrNotExist
	}
	if err != nil || !directory.IsDir() {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot directory is unavailable.")
	}
	initial, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return snapshotFile{}, fs.ErrNotExist
	}
	if err != nil || !initial.Mode().IsRegular() {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot must be a regular file.")
	}
	if initial.Size() > maxSnapshotBytes {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot exceeds the size limit.")
	}

	file, err := os.Open(path)
	if err != nil {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot cannot be opened.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot changed during open.")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSnapshotBytes+1))
	if err != nil {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot cannot be read.")
	}
	if len(data) > maxSnapshotBytes {
		return snapshotFile{}, invalidSnapshot("snapshot", "Native snapshot exceeds the size limit.")
	}
	return snapshotFile{data: data, info: opened}, nil
}

func parseSnapshot(data []byte, boardID string) (snapshot, error) {
	var value snapshot
	if !utf8.Valid(data) || unmarshalCanonical(data, &value) != nil {
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
	if value.Format != "localswim-snapshot" || value.FormatVersion != 2 {
		return invalidSnapshot("format", "Unsupported native snapshot format.")
	}
	if b.ID != boardID || !validSnapshotID(b.ID) {
		return invalidSnapshot("board.id", "Board identity does not match the snapshot filename.")
	}
	if !validSnapshotEntity(b.entity) || b.Revision > maxSnapshotInteger {
		return invalidSnapshot("board", "Board version or entity metadata is invalid.")
	}
	if !snapshotText(b.Name, 1, 256) || !snapshotText(b.UserCSS, 0, maxBoardCSSLength) ||
		(b.Availability != "open" && b.Availability != "closed") {
		return invalidSnapshot("board", "Board metadata is invalid.")
	}
	if (b.Publishing.Repository != nil && *b.Publishing.Repository != "origin") ||
		b.Publishing.LastSuccessAt != nil || b.Publishing.Pending || b.Publishing.ErrorCode != nil {
		return invalidSnapshot("board.publishing", "Publishing metadata is invalid.")
	}
	if value.Users == nil || value.Lanes == nil || value.Priorities == nil || value.Cards == nil ||
		value.Comments == nil || value.Relationships == nil || value.Attachments == nil || value.Activity == nil {
		return invalidSnapshot("snapshot", "Native collections must be arrays.")
	}

	refs := snapshotReferences{
		ids:          map[string]bool{b.ID: true},
		users:        make(map[string]user, len(value.Users)),
		lanes:        make(map[string]lane, len(value.Lanes)),
		priorities:   make(map[string]bool, len(value.Priorities)),
		cards:        make(map[string]card, len(value.Cards)),
		comments:     make(map[string]comment, len(value.Comments)),
		attachments:  make(map[string]attachment, len(value.Attachments)),
		capabilities: make(map[string]map[string]bool, len(value.Users)),
	}
	if err := refs.validateWorkflow(value); err != nil {
		return err
	}
	if err := refs.validateCards(value); err != nil {
		return err
	}
	if err := refs.validateRelationships(value); err != nil {
		return err
	}
	if err := refs.validateCardChildren(value); err != nil {
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
	comments        map[string]comment
	attachments     map[string]attachment
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
		if !refs.claim(entity{ID: u.ID, Revision: u.Revision, CreatedAt: u.CreatedAt, UpdatedAt: u.CreatedAt}) || u.Revision != 1 || (u.Kind != "human" && u.Kind != "bot") ||
			!snapshotText(u.Label, 1, 256) || !snapshotText(u.Color, 0, 64) {
			return invalidSnapshot("users", "User identity or metadata is invalid.")
		}
		refs.users[u.ID] = u
	}
	if refs.users[b.DefaultOwnerID].ID == "" {
		return invalidSnapshot("board.default_owner_id", "Default owner must be a user on this board.")
	}
	if !matchSnapshotIDs(b.UserIDs, refs.users) {
		return invalidSnapshot("board.user_ids", "Board users do not match native users.")
	}
	if !b.ActorPresentation.valid() {
		return invalidSnapshot("board.actor_presentation", "Actor presentation is invalid.")
	}

	laneKeys := make(map[string]bool, len(value.Lanes))
	positions := make(map[uint64]bool, len(value.Lanes))
	completed := 0
	for _, l := range value.Lanes {
		if !validSnapshotID(l.ID) || refs.ids[l.ID] || l.Revision == 0 || l.Revision > b.Revision ||
			!validSnapshotTime(l.CreatedAt) || !validSnapshotTime(l.UpdatedAt) || l.UpdatedAt.Before(l.CreatedAt) ||
			l.BoardID != b.ID || !snapshotText(l.Key, 1, 128) ||
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
		if (l.ApprovalEnabled || l.BulkApprovalComment != "") && l.Role != "ready_for_review" {
			return invalidSnapshot("lanes", "Approval is limited to Ready for Review lanes.")
		}
		if !snapshotText(l.BulkApprovalComment, 0, maxBulkApprovalComment) {
			return invalidSnapshot("lanes.bulk_approval_comment", "Bulk approval comment is invalid.")
		}
		if !snapshotText(l.Notes, 0, maxLaneNotes) {
			return invalidSnapshot("lanes.notes", "Lane notes are invalid.")
		}
		refs.ids[l.ID] = true
		refs.lanes[l.ID] = l
		laneKeys[l.Key] = true
		positions[uint64(l.Position)] = true
	}
	if completed != 1 || !matchSnapshotIDs(b.LaneIDs, refs.lanes) {
		return invalidSnapshot("board.lane_ids", "Board lanes do not match native lanes or lack one Completed lane.")
	}

	priorityKeys := make(map[string]bool, len(value.Priorities))
	ranks := make(map[uint64]bool, len(value.Priorities))
	for _, p := range value.Priorities {
		if !refs.claim(p.entity) || p.Revision != 1 || p.BoardID != b.ID || !snapshotText(p.Key, 1, 128) ||
			!snapshotText(p.Label, 1, 256) || !snapshotText(p.Color, 0, 64) ||
			p.Rank < 0 || priorityKeys[p.Key] || ranks[uint64(p.Rank)] {
			return invalidSnapshot("priorities", "Priority identity or metadata is invalid.")
		}
		refs.priorities[p.ID] = true
		priorityKeys[p.Key] = true
		ranks[uint64(p.Rank)] = true
	}
	if !matchSnapshotIDs(b.PriorityIDs, refs.priorities) {
		return invalidSnapshot("board.priority_ids", "Board priorities do not match native priorities.")
	}
	if err := refs.validatePolicy(value.Policy, b); err != nil {
		return err
	}
	if issue := validateWorkflowPaths(value.Lanes, value.Policy); issue != nil {
		return invalidSnapshot(issue.Field, issue.Message)
	}
	return nil
}

func (refs snapshotReferences) validatePolicy(p policy, b board) error {
	if !refs.claim(p.entity) || p.Revision > b.Revision || p.ID != b.PolicyID || p.BoardID != b.ID || !refs.priorities[p.DefaultPriorityID] {
		return invalidSnapshot("policy", "Policy identity or default priority is invalid.")
	}
	if p.CreateGrants == nil || p.MoveGrants == nil || p.ResourceGrants == nil {
		return invalidSnapshot("policy", "Policy grant collections must be arrays.")
	}
	create := make(map[createGrant]bool, len(p.CreateGrants))
	for _, g := range p.CreateGrants {
		if refs.users[g.UserID].ID == "" || refs.lanes[g.LaneID].ID == "" || create[g] {
			return invalidSnapshot("policy.create_grants", "Create grant is duplicated or has an unknown reference.")
		}
		create[g] = true
	}
	move := make(map[moveGrant]bool, len(p.MoveGrants))
	for _, g := range p.MoveGrants {
		if refs.users[g.UserID].ID == "" || refs.lanes[g.FromLaneID].ID == "" || refs.lanes[g.ToLaneID].ID == "" ||
			g.FromLaneID == g.ToLaneID || move[g] {
			return invalidSnapshot("policy.move_grants", "Move grant is duplicated or has an invalid reference.")
		}
		move[g] = true
	}
	for _, g := range p.ResourceGrants {
		if refs.users[g.UserID].ID == "" || refs.capabilities[g.UserID] != nil || g.Capabilities == nil {
			return invalidSnapshot("policy.resource_grants", "Resource grant is duplicated or has an invalid actor or capability list.")
		}
		seen := make(map[string]bool, len(g.Capabilities))
		for _, capability := range g.Capabilities {
			if !isInitialCapability(capability) || seen[capability] ||
				(capability == "approve" && refs.users[g.UserID].Kind != "human") {
				return invalidSnapshot("policy.resource_grants", "Resource capability is duplicated or unknown.")
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
		if !validSnapshotCard(c, value.Board.Revision) || refs.ids[c.ID] || c.BoardID != value.Board.ID || ticket == 0 ||
			ticket > maxSnapshotInteger || tickets[ticket] || l.ID == "" || (l.Retired && !c.Archived) ||
			!refs.priorities[c.PriorityID] || refs.users[c.OwnerID].ID == "" {
			return invalidSnapshot("cards", "Card identity, ticket, or workflow reference is invalid.")
		}
		if !snapshotText(c.Title, 1, 1024) || !snapshotText(c.Description, 0, 4194304) ||
			c.ChildIDs == nil ||
			c.BlockingDepth != 0 || c.BlockedCardCount != 0 {
			return invalidSnapshot("cards", "Card metadata is invalid or refers to unsupported resources.")
		}
		if (c.CompletedAt != nil) != (l.Role == "completed") ||
			(c.CompletedAt != nil && !validSnapshotTime(*c.CompletedAt)) {
			return invalidSnapshot("cards.completed_at", "Card completion timestamp does not match its lane.")
		}
		refs.ids[c.ID] = true
		tickets[ticket] = true
		refs.cards[c.ID] = c
	}

	children := make(map[string][]string)
	for _, c := range value.Cards {
		if c.ParentID != nil {
			if refs.cards[*c.ParentID].ID == "" {
				return invalidSnapshot("cards.parent_id", "Card parent does not exist.")
			}
			children[*c.ParentID] = append(children[*c.ParentID], c.ID)
		}
	}
	for _, c := range value.Cards {
		slices.Sort(children[c.ID])
		if !slices.Equal(c.ChildIDs, children[c.ID]) {
			return invalidSnapshot("cards.child_ids", "Card child identities do not match parent links.")
		}
	}
	if refs.parentCycle() {
		return invalidSnapshot("cards.parent_id", "Card parent links contain a cycle.")
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

func validSnapshotOperationID(id string) bool {
	canonical, ok := normalizeOperationID(id)
	return ok && canonical == id
}

func validSnapshotEntity(item entity) bool {
	return validSnapshotID(item.ID) && item.Revision > 0 && item.Revision <= maxSnapshotInteger &&
		validSnapshotTime(item.CreatedAt) && validSnapshotTime(item.UpdatedAt) &&
		!item.UpdatedAt.Before(item.CreatedAt)
}

func validSnapshotCard(item card, boardRevision uint64) bool {
	return validSnapshotID(item.ID) && item.Revision > 0 && item.Revision <= boardRevision &&
		validSnapshotTime(item.CreatedAt) && validSnapshotTime(item.UpdatedAt) &&
		!item.UpdatedAt.Before(item.CreatedAt)
}

func validSnapshotTime(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0 && value.Nanosecond() == 0 && !value.IsZero() && value.Year() >= 0 && value.Year() <= 9999
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

	// Availability and opt-in settings must match replay from board creation.
	disabled, open := "false", "open"
	latest := map[snapshotAuditField]*string{
		{"board", b.ID, "availability"}:          &open,
		{"board", b.ID, "publishing.enabled"}:    &disabled,
		{"board", b.ID, "publishing.repository"}: nil,
	}
	changed := make(map[snapshotAuditField]bool)
	replayPolicy := initialAuditedPolicy(value)
	initialLanes := initialLaneIDs(value.Lanes, replayPolicy)
	if !validLaneKeyHistory(value, initialLanes) {
		return invalidSnapshot("activity", "Lane key history contains a duplicate key.")
	}
	laneOrigins := originalCardLanes(value)
	activeLaneOccupancy := make(map[string]int, len(value.Lanes))
	detached := make(map[string]uint64)
	priorityRanks := make(map[string]int, len(value.Priorities))
	for _, item := range value.Priorities {
		priorityRanks[item.ID] = item.Rank
	}
	if !policyReferencesKnownLanes(replayPolicy, func(id string) bool { return initialLanes[id] }) {
		return invalidSnapshot("activity", "The original policy references a lane that did not yet exist.")
	}
	laneRevisions := make(map[string]uint64, len(value.Lanes))
	for _, item := range value.Lanes {
		if initialLanes[item.ID] && !item.CreatedAt.Equal(b.CreatedAt) {
			return invalidSnapshot("lanes.created_at", "Initial lane creation does not match board creation.")
		}
	}
	initializeLane := func(id string, revision uint64) {
		laneRevisions[id] = revision
		latest[snapshotAuditField{"lane", id, "retired"}] = &disabled
		latest[snapshotAuditField{"lane", id, "approval_enabled"}] = &disabled
		latest[snapshotAuditField{"lane", id, "reorder_enabled"}] = &disabled
		empty := ""
		latest[snapshotAuditField{"lane", id, "bulk_approval_comment"}] = &empty
	}
	for id := range initialLanes {
		initializeLane(id, 1)
	}
	cardCreations := make(map[string]uint64)
	approvalCompletions := make(map[string]time.Time)
	resourceCreations := make(map[string]uint64)
	laneExists := func(id string) bool { return initialLanes[id] || resourceCreations[id] != 0 }
	commentCreations, attachmentCreations := 0, 0
	var newLane string
	var approvalComment string
	var approvalEnd int
	var priorityTrigger bool
	var previousRevision uint64
	var previousActor string
	var recoveryRevision uint64
	policyRevision := uint64(1)
	for index, event := range value.Activity {
		if !validSnapshotOperationID(event.ID) || refs.ids[event.ID] ||
			event.BoardID != b.ID || !validSnapshotTime(event.At) {
			return invalidSnapshot("activity", "Audit identity or metadata is invalid.")
		}
		if event.BulkApprovalComment != "" && (event.Operation != approvalOperation || event.ResourceType != "card" ||
			snapshotString(event.Field) != "lane_id") {
			return invalidSnapshot("activity.bulk_approval_comment", "Bulk approval comment requires an approval move.")
		}
		refs.ids[event.ID] = true
		trustedBootstrap := event.Revision == 1 && (event.ActorID == humanID || event.ActorID == botID)
		recovery := validRecoveryAudit(event, b.ID, previousRevision, refs.users[event.ActorID])
		if refs.users[event.ActorID].ID == "" && !trustedBootstrap && !recovery {
			return invalidSnapshot("activity.actor_id", "Audit actor is unknown.")
		}
		if event.Revision < previousRevision || (event.Revision > previousRevision+1 && !recovery) ||
			event.Revision > b.Revision || (event.Revision == previousRevision && event.ActorID != previousActor) {
			return invalidSnapshot("activity.revision", "Audit does not cover consecutive board revisions.")
		}
		if event.Revision != previousRevision {
			clear(changed)
			newLane = ""
			approvalComment = ""
			approvalEnd = 0
			priorityTrigger = false
		}
		previousRevision, previousActor = event.Revision, event.ActorID
		if recovery {
			evidence, _ := recoveryEventEvidence(event)
			if evidence.SourceBoardID != nil {
				latest[snapshotAuditField{"board", b.ID, "publishing.enabled"}] = &disabled
				latest[snapshotAuditField{"board", b.ID, "publishing.repository"}] = nil
			}
			recoveryRevision = event.Revision
			if index+1 < len(value.Activity) && value.Activity[index+1].Revision == event.Revision {
				return invalidSnapshot("activity", "Recovery must be the only event in its revision.")
			}
			continue
		}
		if index == 0 {
			continue
		}
		if event.Revision == 1 {
			item := refs.lanes[event.ResourceID]
			if item.ID == "" || event.ResourceType != "lane" || event.Operation != "create" ||
				!item.CreatedAt.Equal(b.CreatedAt) || !event.At.Equal(b.CreatedAt) ||
				event.Before != nil {
				return invalidSnapshot("activity", "Initial lane creation audit is invalid.")
			}
			if event.Field != nil {
				field := snapshotAuditField{"lane", item.ID, *event.Field}
				isLaneOptIn := field.name == "reorder_enabled" || field.name == "approval_enabled" && item.Role == "ready_for_review"
				if !initialLanes[item.ID] || !isLaneOptIn || changed[field] || snapshotString(event.After) != "true" ||
					!refs.capabilities[event.ActorID]["manage_workflow"] {
					return invalidSnapshot("activity", "Initial lane setting audit is invalid.")
				}
				latest[field], changed[field] = event.After, true
				continue
			}
			if initialLanes[item.ID] || event.After != nil {
				return invalidSnapshot("activity", "Initial lane creation audit is invalid.")
			}
			initialLanes[item.ID] = true
			initializeLane(item.ID, 1)
			continue
		}

		if event.Operation == "create" && event.ResourceType == "card" {
			item, found := refs.cards[event.ResourceID]
			parentField := snapshotAuditField{"card", event.ResourceID, "parent_id"}
			_, priorParentEdit := latest[parentField]
			if !found || cardCreations[item.ID] != 0 || priorParentEdit || event.Revision == 1 ||
				!refs.capabilities[event.ActorID]["create_card"] || !event.At.Equal(item.CreatedAt) ||
				event.Field != nil || event.Before != nil || event.After != nil {
				return invalidSnapshot("activity", "Card creation audit is invalid.")
			}
			cardCreations[item.ID] = event.Revision
			latest[parentField] = nil
			origin := laneOrigins[item.ID]
			if snapshotString(latest[snapshotAuditField{"lane", origin, "retired"}]) != "false" ||
				!slices.Contains(replayPolicy.CreateGrants, createGrant{UserID: event.ActorID, LaneID: origin}) {
				return invalidSnapshot("activity", "Card creation requires an active lane and a creation grant.")
			}
			latest[snapshotAuditField{"card", item.ID, "lane_id"}] = &origin
			activeLaneOccupancy[origin]++
			latest[snapshotAuditField{"card", item.ID, "archived"}] = &disabled
			latest[snapshotAuditField{"card", item.ID, "cover_attachment_id"}] = nil
			continue
		}
		if event.ResourceType == "relationship" {
			if (event.Operation == "create" || event.Operation == "patch") && event.After != nil && len(*event.After) <= 140 {
				var link relationshipLink
				if json.Unmarshal([]byte(*event.After), &link) == nil && link.Kind == "blocks" {
					priorityTrigger = true
				}
			}
			continue
		}
		if event.ResourceType == "attachment" && event.Operation == "detach" {
			item := refs.attachments[event.ResourceID]
			parent := refs.cards[item.CardID]
			if item.ID == "" || item.Active || detached[item.ID] != 0 || resourceCreations[item.ID] == 0 ||
				resourceCreations[item.ID] >= event.Revision || !refs.capabilities[event.ActorID]["manage_attachments"] ||
				event.Field != nil || event.Before != nil || event.After != nil ||
				!item.UpdatedAt.Equal(event.At) ||
				!validDetachAuditShape(value.Activity, index, item) ||
				parent.Revision < event.Revision || parent.UpdatedAt.Before(event.At) ||
				snapshotString(latest[snapshotAuditField{"card", item.CardID, "cover_attachment_id"}]) == item.ID {
				return invalidSnapshot("activity", "Attachment detach audit is invalid.")
			}
			detached[item.ID] = event.Revision
			continue
		}
		if event.ResourceType == "comment" && event.ResourceID != approvalComment &&
			(value.Activity[index-1].Revision == event.Revision ||
				(index+1 < len(value.Activity) && value.Activity[index+1].Revision == event.Revision)) {
			return invalidSnapshot("activity", "Only an approval may commit a comment with another edit.")
		}
		if event.ResourceType == "comment" || event.ResourceType == "attachment" ||
			(event.ResourceType == "lane" && event.Operation == "create") {
			if resourceCreations[event.ResourceID] != 0 || !refs.validResourceCreation(event, cardCreations, event.ResourceID == approvalComment) {
				return invalidSnapshot("activity", "Resource creation audit is invalid.")
			}
			resourceCreations[event.ResourceID] = event.Revision
			switch event.ResourceType {
			case "comment":
				commentCreations++
			case "attachment":
				attachmentCreations++
			case "lane":
				if newLane != "" || initialLanes[event.ResourceID] {
					return invalidSnapshot("activity", "Lane creation must introduce one new lane in a revision.")
				}
				newLane = event.ResourceID
				initializeLane(event.ResourceID, event.Revision)
			}
			continue
		}

		field := snapshotAuditField{event.ResourceType, event.ResourceID, snapshotString(event.Field)}
		valid := false
		switch {
		case event.ResourceType == "policy":
			var after policy
			after, valid = checkedPolicyAuditEvent(value, event, newLane)
			valid = valid && policyReferencesKnownLanes(after, laneExists)
			if valid {
				replayPolicy = after
				policyRevision = event.Revision
			}
		case event.ResourceType == "card" && field.name == "lane_id":
			createdAt := cardCreations[field.resourceID]
			valid = createdAt > 0 && createdAt < event.Revision && validCardLaneAudit(replayPolicy.MoveGrants, event) &&
				snapshotString(latest[snapshotAuditField{"lane", snapshotString(event.After), "retired"}]) == "false"
			if valid && event.Operation == approvalOperation {
				if approvalEnd == 0 {
					approvalEnd = refs.approvalAuditEnd(value, index, latest, activeLaneOccupancy)
				}
				valid = index < approvalEnd
				if valid {
					approvalComment = value.Activity[index+1].ResourceID
				}
			}
		case event.ResourceType == "card" && field.name == "priority_id":
			before, fromExists := priorityRanks[snapshotString(event.Before)]
			after, toExists := priorityRanks[snapshotString(event.After)]
			createdAt := cardCreations[field.resourceID]
			valid = createdAt > 0 && createdAt <= event.Revision && fromExists && toExists
			if event.Operation == "promote" {
				laneID := snapshotString(latest[snapshotAuditField{"card", field.resourceID, "lane_id"}])
				valid = valid && priorityTrigger && after < before && refs.lanes[laneID].Role != "completed"
			} else {
				valid = valid && createdAt < event.Revision && refs.capabilities[event.ActorID]["set_priority"]
			}
		case event.ResourceType == "card" && field.name == "ticket":
			_, beforeValid := ticketAuditValue(event.Before)
			_, afterValid := ticketAuditValue(event.After)
			createdAt := cardCreations[field.resourceID]
			valid = event.Operation == ticketOrderOperation && createdAt > 0 && createdAt < event.Revision &&
				refs.capabilities[event.ActorID]["reorder_tickets"] && beforeValid && afterValid
		case event.ResourceType == "card" && field.name == "cover_attachment_id":
			createdAt := cardCreations[field.resourceID]
			attachedAt := resourceCreations[snapshotString(event.After)]
			valid = createdAt > 0 && createdAt < event.Revision &&
				(event.After == nil || (attachedAt > 0 && attachedAt < event.Revision && detached[*event.After] == 0)) &&
				refs.validCardAuditValue(event.ActorID, field, event.Before) &&
				refs.validCardAuditValue(event.ActorID, field, event.After)
		case event.ResourceType == "card" && field.name == "archived":
			createdAt := cardCreations[field.resourceID]
			laneID := snapshotString(latest[snapshotAuditField{"card", field.resourceID, "lane_id"}])
			valid = createdAt > 0 && createdAt < event.Revision &&
				refs.validCardAuditValue(event.ActorID, field, event.Before) &&
				refs.validCardAuditValue(event.ActorID, field, event.After) &&
				(snapshotString(event.After) != "false" || snapshotString(latest[snapshotAuditField{"lane", laneID, "retired"}]) == "false")
		case event.ResourceType == "lane" && field.name == "retired":
			approvalFlag := snapshotAuditField{"lane", event.ResourceID, "approval_enabled"}
			valid = laneExists(event.ResourceID) && activeLaneOccupancy[event.ResourceID] == 0 &&
				snapshotString(event.Before) == "false" && snapshotString(event.After) == "true" &&
				(!changed[approvalFlag] || snapshotString(latest[approvalFlag]) == "false") &&
				validRetirementAuditShape(value.Activity, index) &&
				refs.validLaneAuditValue(event.ActorID, field, event.After) &&
				validHistoricalRetirement(value.Lanes, replayPolicy, event.ResourceID, laneExists, latest)
		case event.ResourceType == "lane" && (field.name == "approval_enabled" || field.name == "bulk_approval_comment"):
			valid = laneExists(event.ResourceID) &&
				snapshotString(latest[snapshotAuditField{"lane", event.ResourceID, "retired"}]) == "false" &&
				refs.validLaneAuditValue(event.ActorID, field, event.Before) &&
				refs.validLaneAuditValue(event.ActorID, field, event.After)
		default:
			valid = (event.ResourceType != "lane" || laneExists(event.ResourceID)) &&
				refs.validAuditField(b.ID, event.ActorID, field, event.Before) &&
				refs.validAuditField(b.ID, event.ActorID, field, event.After)
		}
		isApproval := event.Operation == approvalOperation && field.name == "lane_id" && index < approvalEnd
		isPromotion := event.Operation == "promote" && field.resourceType == "card" && field.name == "priority_id"
		isTicketOrder := event.Operation == ticketOrderOperation && field.resourceType == "card" && field.name == "ticket"
		isReopen := event.Operation == reopenOperation && field.resourceType == "card" && field.name == "lane_id" &&
			refs.lanes[snapshotString(event.Before)].Role == "completed" &&
			refs.lanes[snapshotString(event.After)].Role != "completed"
		if (event.Operation != "patch" && !isApproval && !isPromotion && !isReopen && !isTicketOrder) || event.Revision == 1 || changed[field] ||
			!valid || equalSnapshotStrings(event.Before, event.After) {
			return invalidSnapshot("activity", "Audit patch actor, field, or values are invalid.")
		}
		if prior, seen := latest[field]; seen && !equalSnapshotStrings(prior, event.Before) {
			return invalidSnapshot("activity", "Audit patch values do not form a continuous history.")
		}
		if event.ResourceType == "card" && (isApproval || isReopen || isTicketOrder || field.name == "archived" || field.name == "cover_attachment_id" || field.name == "priority_id") {
			item := refs.cards[event.ResourceID]
			if item.Revision < event.Revision || item.UpdatedAt.Before(event.At) {
				return invalidSnapshot("cards.revision", "Card version does not cover its latest edits.")
			}
		}
		if event.ResourceType == "card" && field.name == "priority_id" && !isPromotion {
			priorityTrigger = true
		}
		if event.ResourceType == "card" && field.name == "archived" {
			laneID := snapshotString(latest[snapshotAuditField{"card", event.ResourceID, "lane_id"}])
			if snapshotString(event.After) == "true" {
				activeLaneOccupancy[laneID]--
			} else {
				activeLaneOccupancy[laneID]++
			}
		}
		if event.ResourceType == "card" && field.name == "lane_id" {
			if refs.lanes[snapshotString(event.Before)].Role == "completed" {
				priorityTrigger = true
			}
			if snapshotString(latest[snapshotAuditField{"card", event.ResourceID, "archived"}]) == "false" {
				activeLaneOccupancy[snapshotString(event.Before)]--
				activeLaneOccupancy[snapshotString(event.After)]++
			}
			if isApproval {
				approvalCompletions[event.ResourceID] = event.At
			} else {
				delete(approvalCompletions, event.ResourceID)
			}
		}
		changed[field] = true
		latest[field] = event.After
		if event.ResourceType == "lane" {
			laneRevisions[event.ResourceID] = event.Revision
		}
	}
	for _, item := range value.Lanes {
		if item.Revision != max(laneRevisions[item.ID], recoveryRevision) {
			return invalidSnapshot("lanes.revision", "Lane revision does not match its latest audit.")
		}
		if !initialLanes[item.ID] && resourceCreations[item.ID] == 0 {
			return invalidSnapshot("activity", "Lane creation audit is missing.")
		}
	}
	for id, item := range refs.cards {
		if item.Revision < recoveryRevision {
			return invalidSnapshot("cards.revision", "Card revision predates recovery.")
		}
		if cardCreations[id] == 0 && (item.Archived || item.CoverAttachmentID != nil) {
			return invalidSnapshot("activity", "Archive and cover state require a card creation audit.")
		}
	}
	for id, at := range approvalCompletions {
		completed := refs.cards[id].CompletedAt
		if completed == nil || !completed.Equal(at) {
			return invalidSnapshot("cards.completed_at", "Card completion does not match its approval audit.")
		}
	}
	if commentCreations != len(refs.comments) || attachmentCreations != len(refs.attachments) {
		return invalidSnapshot("activity", "Child resource creation audit is missing.")
	}
	for _, item := range value.Attachments {
		if item.Active == (detached[item.ID] != 0) {
			return invalidSnapshot("attachments", "Attachment active state does not match its audit.")
		}
		if item.Revision != max(resourceCreations[item.ID], detached[item.ID], recoveryRevision) {
			return invalidSnapshot("attachments.revision", "Attachment revision does not match its latest audit.")
		}
	}
	if value.Policy.Revision != max(policyRevision, recoveryRevision) {
		return invalidSnapshot("policy.revision", "Policy version does not match its latest audit.")
	}
	if previousRevision != b.Revision {
		return invalidSnapshot("activity.revision", "Audit does not reach the board revision.")
	}
	for field, after := range latest {
		var current *string
		if field.resourceType == "policy" {
			grants := policyAuditValue(value.Policy)
			current = &grants
		} else {
			current = refs.currentAuditField(b, field)
		}
		if !equalSnapshotStrings(after, current) {
			return invalidSnapshot("activity", "Audit patch history does not match current state.")
		}
	}
	if err := refs.validateRelationshipAudit(value, cardCreations, recoveryRevision); err != nil {
		return err
	}
	if err := validateTicketOrderAudit(value); err != nil {
		return err
	}
	return validatePriorityFloorAudit(value)
}

func (refs snapshotReferences) validAuditField(boardID, actorID string, field snapshotAuditField, value *string) bool {
	capabilities := refs.capabilities[actorID]
	switch field.resourceType {
	case "board":
		if field.resourceID != boardID || !capabilities["read"] {
			return false
		}
		switch field.name {
		case "actor_presentation":
			return capabilities["manage_board"] && validActorPresentationAudit(value)
		case "default_owner_id":
			return capabilities["manage_board"] && value != nil && refs.users[*value].ID != ""
		case "name":
			return capabilities["manage_board"] && snapshotText(snapshotString(value), 1, 256)
		case "user_css":
			return capabilities["manage_board"] && value != nil && snapshotText(*value, 0, maxBoardCSSLength)
		case "availability":
			return capabilities["close_board"] && (snapshotString(value) == "open" || snapshotString(value) == "closed")
		case "publishing.enabled":
			return capabilities["publish"] && (snapshotString(value) == "true" || snapshotString(value) == "false")
		case "publishing.repository":
			return capabilities["publish"] && (value == nil || *value == "origin")
		}
	case "card":
		return refs.validCardAuditValue(actorID, field, value)
	case "lane":
		return refs.validLaneAuditValue(actorID, field, value)
	}
	return false
}

func (refs snapshotReferences) currentAuditField(b board, field snapshotAuditField) *string {
	if field.resourceType == "card" {
		return refs.currentCardAuditValue(field)
	}
	if field.resourceType == "lane" {
		return refs.currentLaneAuditValue(field)
	}
	switch field.name {
	case "actor_presentation":
		return b.ActorPresentation.auditValue()
	case "default_owner_id":
		return &b.DefaultOwnerID
	case "name":
		return &b.Name
	case "user_css":
		return &b.UserCSS
	case "availability":
		return &b.Availability
	case "publishing.enabled":
		enabled := strconv.FormatBool(b.Publishing.Enabled)
		return &enabled
	case "publishing.repository":
		return b.Publishing.Repository
	default:
		return nil
	}
}
