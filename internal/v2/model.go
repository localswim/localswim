package v2

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

const Version = "2026-11-10"
const Prefix = "/api/" + Version
const maxJSONBytes = 96 << 20

type entity struct {
	ID        string    `json:"id"`
	ETag      string    `json:"etag"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newEntity(id string, now time.Time) entity {
	now = canonicalTime(now)
	return entity{id, etag(id, 1), now, now}
}
func newID() string {
	var data [16]byte
	rand.Read(data[:])
	data[6] = data[6]&0x0f | 0x40
	data[8] = data[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[:4], data[4:6], data[6:8], data[8:10], data[10:])
}
func normalizeID(input string) (string, bool) {
	id := strings.ToLower(input)
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) || strings.Count(id, "-") != 4 {
		return "", false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil {
		return "", false
	}
	return id, true
}
func etag(id string, revision uint64) string { return fmt.Sprintf("\"%s-%d\"", id, revision) }

type attention struct {
	Blocked        int `json:"blocked"`
	NeedsInput     int `json:"needs_input"`
	ReadyForReview int `json:"ready_for_review"`
}
type publishing struct {
	Enabled       bool       `json:"enabled"`
	Repository    *string    `json:"repository"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	Pending       bool       `json:"pending"`
	ErrorCode     *string    `json:"error_code"`
}
type board struct {
	entity
	Name           string        `json:"name"`
	PolicyID       string        `json:"policy_id"`
	UserIDs        []string      `json:"user_ids"`
	LaneIDs        []string      `json:"lane_ids"`
	PriorityIDs    []string      `json:"priority_ids"`
	Availability   string        `json:"availability"`
	Revision       uint64        `json:"revision"`
	Attention      attention     `json:"attention"`
	LastCardEditAt *time.Time    `json:"last_card_edit_at"`
	Notifications  notifications `json:"notifications"`
	Publishing     publishing    `json:"publishing"`
	UserCSS        string        `json:"user_css"`
}
type user struct {
	entity
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Color string `json:"color"`
}
type lane struct {
	entity
	BoardID         string `json:"board_id"`
	Key             string `json:"key"`
	Label           string `json:"label"`
	Position        int    `json:"position"`
	Role            string `json:"role"`
	Retired         bool   `json:"retired"`
	ApprovalEnabled bool   `json:"approval_enabled"`
	ReorderEnabled  bool   `json:"reorder_enabled"`
}
type priority struct {
	entity
	BoardID string `json:"board_id"`
	Key     string `json:"key"`
	Label   string `json:"label"`
	Rank    int    `json:"rank"`
	Color   string `json:"color"`
}
type createGrant struct {
	UserID string `json:"user_id"`
	LaneID string `json:"lane_id"`
}
type moveGrant struct {
	UserID     string `json:"user_id"`
	FromLaneID string `json:"from_lane_id"`
	ToLaneID   string `json:"to_lane_id"`
}
type resourceGrant struct {
	UserID       string   `json:"user_id"`
	Capabilities []string `json:"capabilities"`
}
type policy struct {
	entity
	BoardID           string          `json:"board_id"`
	DefaultPriorityID string          `json:"default_priority_id"`
	CreateGrants      []createGrant   `json:"create_grants"`
	MoveGrants        []moveGrant     `json:"move_grants"`
	ResourceGrants    []resourceGrant `json:"resource_grants"`
}
type workflow struct {
	Users      []user
	Lanes      []lane
	Priorities []priority
	Policy     policy
}
type card struct {
	entity
	BoardID           string     `json:"board_id"`
	Ticket            uint64     `json:"ticket"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	LaneID            string     `json:"lane_id"`
	OwnerID           *string    `json:"owner_id"`
	PriorityID        string     `json:"priority_id"`
	ParentID          *string    `json:"parent_id"`
	ChildIDs          []string   `json:"child_ids"`
	Archived          bool       `json:"archived"`
	CoverAttachmentID *string    `json:"cover_attachment_id"`
	CommentCount      int        `json:"comment_count"`
	AttachmentCount   int        `json:"attachment_count"`
	BlockingDepth     int        `json:"blocking_depth"`
	BlockedCardCount  int        `json:"blocked_card_count"`
	TriageScore       int        `json:"triage_score"`
	CompletedAt       *time.Time `json:"completed_at"`
}
type activity struct {
	ID           string    `json:"id"`
	BoardID      string    `json:"board_id"`
	Revision     uint64    `json:"revision"`
	At           time.Time `json:"at"`
	ActorID      string    `json:"actor_id"`
	Operation    string    `json:"operation"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Field        *string   `json:"field"`
	Before       *string   `json:"before"`
	After        *string   `json:"after"`
}
type snapshot struct {
	Format        string            `json:"format"`
	FormatVersion int               `json:"format_version"`
	Board         board             `json:"board"`
	Users         []user            `json:"users"`
	Lanes         []lane            `json:"lanes"`
	Priorities    []priority        `json:"priorities"`
	Policy        policy            `json:"policy"`
	Cards         []card            `json:"cards"`
	Comments      []json.RawMessage `json:"comments"`
	Relationships []json.RawMessage `json:"relationships"`
	Attachments   []json.RawMessage `json:"attachments"`
	Activity      []activity        `json:"activity"`
}

func freshSnapshot(id, name string, wf workflow, actorID string, now time.Time) snapshot {
	now = canonicalTime(now)
	s := snapshot{Format: "localswim-snapshot", FormatVersion: 2,
		Board: board{entity: newEntity(id, now), Name: name, PolicyID: wf.Policy.ID, Availability: "open", Revision: 1,
			UserIDs: []string{}, LaneIDs: []string{}, PriorityIDs: []string{}},
		Users: wf.Users, Lanes: wf.Lanes, Priorities: wf.Priorities, Policy: wf.Policy,
		Cards: []card{}, Comments: []json.RawMessage{}, Relationships: []json.RawMessage{}, Attachments: []json.RawMessage{}, Activity: []activity{}}
	for _, u := range wf.Users {
		s.Board.UserIDs = append(s.Board.UserIDs, u.ID)
	}
	for _, l := range wf.Lanes {
		s.Board.LaneIDs = append(s.Board.LaneIDs, l.ID)
	}
	for _, p := range wf.Priorities {
		s.Board.PriorityIDs = append(s.Board.PriorityIDs, p.ID)
	}
	s.Activity = append(s.Activity, activity{ID: newID(), BoardID: id, Revision: 1, At: now, ActorID: actorID, Operation: "create", ResourceType: "board", ResourceID: id})
	return s
}
func (s snapshot) can(userID, capability string) bool {
	if !slices.Contains(s.Board.UserIDs, userID) {
		return false
	}
	for _, g := range s.Policy.ResourceGrants {
		if g.UserID == userID && slices.Contains(g.Capabilities, capability) {
			return true
		}
	}
	return false
}
