package v2

import (
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"
	"time"
)

type V1ImportedCard struct {
	OldTicket uint64 `json:"old_ticket"`
	OldCardID string `json:"old_card_id"`
	NewTicket uint64 `json:"new_ticket"`
	NewCardID string `json:"new_card_id"`
}

type V1ImportResult struct {
	BoardID                    string           `json:"board_id"`
	Revision                   uint64           `json:"revision"`
	Cards                      []V1ImportedCard `json:"cards"`
	DetachedAttachmentsOmitted int              `json:"detached_attachments_omitted"`
	CompletedDatesDefaulted    int              `json:"completed_dates_defaulted"`
	PolicyAnnotationsOmitted   int              `json:"policy_annotations_omitted"`
}

const v1ImportOperation = "import_v1"

type v1ImportedDates struct {
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

func readV1ImportedDates(event activity, item card, origin lane) (v1ImportedDates, bool) {
	var dates v1ImportedDates
	if event.After == nil || len(*event.After) > 128 || unmarshalCanonical([]byte(*event.After), &dates) != nil ||
		!dates.CreatedAt.Equal(item.CreatedAt) || dates.CreatedAt.After(event.At) || (dates.CompletedAt != nil) != (origin.Role == "completed") ||
		(dates.CompletedAt != nil && (!validSnapshotTime(*dates.CompletedAt) || dates.CompletedAt.After(event.At))) {
		return dates, false
	}
	canonical, err := marshalCanonical(dates)
	return dates, err == nil && string(canonical) == *event.After
}

func v1ReplayPrefix(value snapshot) []activity {
	for _, event := range value.Activity {
		if event.Revision > 2 {
			break
		}
		if event.Operation == v1ImportOperation {
			end, _ := ticketAuditValue(event.After)
			for index, item := range value.Activity {
				if item.Revision > end {
					return value.Activity[:index]
				}
			}
			return value.Activity
		}
	}
	return nil
}

func v1ReplayRecoveryCompatible(current, candidate snapshot) bool {
	prefix := v1ReplayPrefix(current)
	if !reflect.DeepEqual(prefix, v1ReplayPrefix(candidate)) {
		return false
	}
	if len(prefix) == 0 {
		return true
	}
	currentOrigins, candidateOrigins := originalCardLanes(current), originalCardLanes(candidate)
	for _, event := range prefix {
		if event.ResourceType == "card" && event.Operation == "create" && currentOrigins[event.ResourceID] != candidateOrigins[event.ResourceID] {
			return false
		}
	}
	return true
}

// Only this initial creation prefix may seed final lanes directly and retain
// original resource attribution. Subsequent edits use ordinary audit checks.
func validV1ReplayEvent(event activity) bool {
	field := snapshotString(event.Field)
	switch event.ResourceType {
	case "card":
		return event.Operation == "create" || event.Operation == "patch" &&
			(field == "parent_id" || field == "cover_attachment_id" || field == "archived")
	case "lane":
		return event.Operation == "patch" && (field == "retired" || field == "bulk_approval_comment")
	case "comment":
		return event.Operation == "comment"
	case "attachment":
		return event.Operation == "attach"
	case "relationship":
		return event.Operation == "create" && event.Field == nil
	}
	return false
}

// ImportV1 replays current V1 board content into one new board in an offline
// V2 workspace. It uses new identities and never submits a live board edit.
func ImportV1(dataDirectory, attachmentDirectory, boardID string, source io.Reader) (V1ImportResult, error) {
	if dataDirectory == "" {
		return V1ImportResult{}, invalid("data", "An explicit V2 data directory is required.")
	}
	if boardID == "" {
		boardID = newID()
	} else {
		var ok bool
		if boardID, ok = normalizeID(boardID); !ok {
			return V1ImportResult{}, invalid("board_id", "Expected a UUIDv4.")
		}
	}
	export, err := readV1Board(source)
	if err != nil {
		return V1ImportResult{}, err
	}
	now := canonicalTime(time.Now())
	principals := workspacePrincipals(now)
	var root *os.Root
	if slices.ContainsFunc(export.Items, func(item v1Item) bool { return len(item.Attachments) != 0 }) {
		if attachmentDirectory == "" {
			return V1ImportResult{}, invalid("attachments", "Provide the V1 attachments directory to import active files.")
		}
		root, err = os.OpenRoot(attachmentDirectory)
		if err != nil {
			return V1ImportResult{}, fmt.Errorf("open V1 attachments directory: %w", err)
		}
		defer root.Close()
	}
	replay, err := planV1Replay(export, boardID, principals, now, root)
	if err != nil {
		return V1ImportResult{}, err
	}
	store, err := openStore(dataDirectory, principals...)
	if err != nil {
		return V1ImportResult{}, err
	}
	err = store.commitV1Replay(replay)
	closeErr := store.close()
	if errors.Is(err, errWriteUncertain) {
		return replay.result, fmt.Errorf("V1 import for board %s has an uncertain write outcome: %w", boardID, errors.Join(err, closeErr))
	}
	if err != nil {
		return V1ImportResult{}, errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return replay.result, fmt.Errorf("board %s was committed but closing the V2 workspace failed: %w", boardID, closeErr)
	}
	return replay.result, nil
}

type v1Replay struct {
	state       snapshot
	result      V1ImportResult
	attachments []v1AttachmentSource
	now         time.Time
	sourceFiles *os.Root
}

func planV1Replay(export v1BoardExport, id string, principals []user, now time.Time, root *os.Root) (v1Replay, error) {
	ordered, err := orderV1Cards(export)
	if err != nil {
		return v1Replay{}, err
	}
	wf, presentation, actors, err := makeV1Workflow(export, id, principals, now)
	if err != nil {
		return v1Replay{}, err
	}
	state := freshSnapshot(id, export.Project, wf, humanID, now)
	state.Board.ActorPresentation = presentation
	state.Board.DefaultOwnerID = actors[export.DefaultOwner]
	replay := v1Replay{state: state, now: now, sourceFiles: root,
		result: V1ImportResult{BoardID: id, Cards: make([]V1ImportedCard, 0, len(ordered)), PolicyAnnotationsOmitted: export.Policy.omittedAnnotations()}}
	marker := len(replay.state.Activity)
	replay.record(v1ImportOperation, "board", id, nil, nil, nil)
	lanes := make(map[string]lane, len(wf.Lanes))
	for _, item := range wf.Lanes {
		lanes[item.Key] = item
	}
	priorities := make(map[string]string, len(wf.Priorities))
	for _, item := range wf.Priorities {
		priorities[item.Key] = item.ID
	}
	for _, source := range ordered {
		if err := replay.createCard(source, lanes[source.State], priorities[source.Priority], actors[source.Owner]); err != nil {
			return v1Replay{}, err
		}
	}
	cards := make(map[string]*card, len(ordered))
	for index, source := range ordered {
		cards[source.ID] = &replay.state.Cards[index]
	}
	for _, source := range ordered {
		if err := replay.addCardContent(cards[source.ID], source, cards[source.Parent], actors); err != nil {
			return v1Replay{}, err
		}
	}
	for _, link := range export.Links {
		replay.addLink(cards[link.From], cards[link.To], link.Kind)
	}
	if err := replay.finishLanes(export.Policy); err != nil {
		return v1Replay{}, err
	}
	last := strconv.FormatUint(replay.state.Board.Revision, 10)
	replay.state.Activity[marker].After = &last
	replay.state.Board.Attention, replay.state.Board.LastCardEditAt = deriveAttention(replay.state)
	replay.result.Revision = replay.state.Board.Revision
	if err := validateSnapshot(replay.state, id); err != nil {
		return v1Replay{}, fmt.Errorf("V1 board cannot be represented in V2: %w", err)
	}
	if _, err := snapshotCommitBytes(replay.state); err != nil {
		return v1Replay{}, fmt.Errorf("V1 board cannot fit in the V2 store: %w", err)
	}
	return replay, nil
}

func (replay *v1Replay) record(operation, resource, id string, field, before, after *string) uint64 {
	s := &replay.state
	s.Board.Revision++
	s.Activity = append(s.Activity, activity{ID: newOperationID(), BoardID: s.Board.ID, Revision: s.Board.Revision,
		At: replay.now, ActorID: humanID, Operation: operation, ResourceType: resource, ResourceID: id,
		Field: field, Before: before, After: after})
	return s.Board.Revision
}

func (replay *v1Replay) createCard(source v1Item, lane lane, priority, owner string) error {
	if lane.ID == "" || priority == "" || owner == "" || source.Detail == nil {
		return fmt.Errorf("V1 ticket #%04d has an invalid lane, priority, owner or description", source.Ticket)
	}
	created, completed, err := v1CardDates(source, replay.now)
	if err != nil {
		return fmt.Errorf("V1 ticket #%04d: %w", source.Ticket, err)
	}
	if created.After(replay.now) || (completed != nil && completed.After(replay.now)) {
		return fmt.Errorf("V1 ticket #%04d has a creation or completion date after the import clock; correct the source or machine clock before importing", source.Ticket)
	}
	if lane.Role == "completed" && completed == nil {
		completed = &replay.now
		replay.result.CompletedDatesDefaulted++
	}
	id := newID()
	ticket := uint64(len(replay.state.Cards) + 1)
	dates, err := marshalCanonical(v1ImportedDates{CreatedAt: created, CompletedAt: completed})
	if err != nil {
		return err
	}
	revision := replay.record("create", "card", id, nil, nil, new(string(dates)))
	replay.state.Cards = append(replay.state.Cards, card{ID: id, Revision: revision, CreatedAt: created,
		UpdatedAt: maxCardTime(replay.now, created), BoardID: replay.state.Board.ID, Ticket: ticket, Title: source.Subject,
		Description: *source.Detail, LaneID: lane.ID, OwnerID: owner, PriorityID: priority, ChildIDs: []string{}, CompletedAt: completed})
	replay.result.Cards = append(replay.result.Cards, V1ImportedCard{source.Ticket, source.ID, ticket, id})
	return nil
}

func v1CardDates(source v1Item, now time.Time) (time.Time, *time.Time, error) {
	created := now
	var completed *time.Time
	for _, event := range source.History {
		if event.OwnerTo != nil || event.PriorityTo != nil {
			continue
		}
		if event.From != nil && (source.State != "completed" || event.To != "completed") {
			continue
		}
		at, err := v1Timestamp(event.At)
		if err != nil {
			return time.Time{}, nil, err
		}
		if event.From == nil {
			created = at
		}
		if source.State == "completed" && event.To == "completed" {
			completed = &at
		}
	}
	return created, completed, nil
}

func (replay *v1Replay) addCardContent(item *card, source v1Item, parent *card, actors map[string]string) error {
	if parent != nil {
		item.ParentID = &parent.ID
		parent.ChildIDs = append(parent.ChildIDs, item.ID)
		slices.Sort(parent.ChildIDs)
		item.Revision = replay.record("patch", "card", item.ID, new("parent_id"), nil, item.ParentID)
		parent.Revision = item.Revision
	}
	for _, note := range source.Comments {
		at, err := v1Timestamp(note.At)
		if err != nil || actors[note.By] == "" {
			return fmt.Errorf("V1 ticket #%04d has an invalid comment timestamp or author %q", source.Ticket, note.By)
		}
		value := comment{ID: newID(), BoardID: item.BoardID, CardID: item.ID, AuthorID: actors[note.By], CreatedAt: at, Text: note.Text}
		replay.state.Comments = append(replay.state.Comments, value)
		item.Revision = replay.record("comment", "comment", value.ID, nil, nil, nil)
		item.CommentCount++
	}
	if err := replay.addAttachments(item, source, actors); err != nil {
		return err
	}
	if source.Archived {
		item.Archived = true
		item.Revision = replay.record("patch", "card", item.ID, new("archived"), archiveAuditValue(false), archiveAuditValue(true))
	}
	return nil
}

func (replay *v1Replay) addLink(source, target *card, kind string) {
	link := relationshipLink{source.ID, target.ID, kind}
	id := newID()
	after := relationshipAuditValue(link)
	// V1's current priorities travel with the graph, without replaying promotions.
	revision := replay.record("create", "relationship", id, nil, nil, &after)
	replay.state.Relationships = append(replay.state.Relationships, relationship{
		ID: id, Revision: revision, CreatedAt: replay.now, UpdatedAt: replay.now, BoardID: replay.state.Board.ID, relationshipLink: link,
	})
	source.Revision, target.Revision = revision, revision
}

func (replay *v1Replay) finishLanes(policy v1Policy) error {
	for index, source := range policy.Lanes {
		item := &replay.state.Lanes[index]
		if source.ApprovalMessage != "" {
			item.BulkApprovalComment = v1TrimSpace(source.ApprovalMessage)
			if item.Role != "ready_for_review" || !snapshotText(item.BulkApprovalComment, 1, maxBulkApprovalComment) {
				return fmt.Errorf("V1 lane %q has an approval message that V2 cannot represent; Ready for Review messages allow 1 to %d characters", item.Key, maxBulkApprovalComment)
			}
			item.Revision = replay.record("patch", "lane", item.ID, new("bulk_approval_comment"), new(""), &item.BulkApprovalComment)
		}
		if slices.Contains(policy.RetiredLanes, source.ID) {
			item.Retired = true
			item.Revision = replay.record("patch", "lane", item.ID, new("retired"), archiveAuditValue(false), archiveAuditValue(true))
		}
	}
	return nil
}
