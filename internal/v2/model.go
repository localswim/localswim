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
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func newEntity(id string, now time.Time) entity { return entity{id, etag(id, 1), now, now} }
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
	NeedsInput     int `json:"needsInput"`
	ReadyForReview int `json:"readyForReview"`
}
type publishing struct {
	Enabled       bool       `json:"enabled"`
	Repository    *string    `json:"repository"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
	Pending       bool       `json:"pending"`
	ErrorCode     *string    `json:"errorCode"`
}
type board struct {
	entity
	Name           string        `json:"name"`
	PolicyID       string        `json:"policyId"`
	UserIDs        []string      `json:"userIds"`
	LaneIDs        []string      `json:"laneIds"`
	PriorityIDs    []string      `json:"priorityIds"`
	Availability   string        `json:"availability"`
	Revision       uint64        `json:"revision"`
	Attention      attention     `json:"attention"`
	LastCardEditAt *time.Time    `json:"lastCardEditAt"`
	Notifications  notifications `json:"notifications"`
	Publishing     publishing    `json:"publishing"`
	UserCSS        string        `json:"userCss"`
}
type user struct {
	entity
	Label string `json:"label"`
	Kind  string `json:"kind"`
	Color string `json:"color"`
}
type lane struct {
	entity
	BoardID         string `json:"boardId"`
	Key             string `json:"key"`
	Label           string `json:"label"`
	Position        int    `json:"position"`
	Role            string `json:"role"`
	Retired         bool   `json:"retired"`
	ApprovalEnabled bool   `json:"approvalEnabled"`
	ReorderEnabled  bool   `json:"reorderEnabled"`
}
type priority struct {
	entity
	BoardID string `json:"boardId"`
	Key     string `json:"key"`
	Label   string `json:"label"`
	Rank    int    `json:"rank"`
	Color   string `json:"color"`
}
type createGrant struct {
	UserID string `json:"userId"`
	LaneID string `json:"laneId"`
}
type moveGrant struct {
	UserID     string `json:"userId"`
	FromLaneID string `json:"fromLaneId"`
	ToLaneID   string `json:"toLaneId"`
}
type resourceGrant struct {
	UserID       string   `json:"userId"`
	Capabilities []string `json:"capabilities"`
}
type policy struct {
	entity
	BoardID           string          `json:"boardId"`
	DefaultPriorityID string          `json:"defaultPriorityId"`
	CreateGrants      []createGrant   `json:"createGrants"`
	MoveGrants        []moveGrant     `json:"moveGrants"`
	ResourceGrants    []resourceGrant `json:"resourceGrants"`
}
type workflow struct {
	Users      []user
	Lanes      []lane
	Priorities []priority
	Policy     policy
}
type card struct {
	entity
	BoardID           string     `json:"boardId"`
	Ticket            uint64     `json:"ticket"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	LaneID            string     `json:"laneId"`
	OwnerID           *string    `json:"ownerId"`
	PriorityID        string     `json:"priorityId"`
	ParentID          *string    `json:"parentId"`
	ChildIDs          []string   `json:"childIds"`
	Archived          bool       `json:"archived"`
	CoverAttachmentID *string    `json:"coverAttachmentId"`
	CommentCount      int        `json:"commentCount"`
	AttachmentCount   int        `json:"attachmentCount"`
	BlockingDepth     int        `json:"blockingDepth"`
	BlockedCardCount  int        `json:"blockedCardCount"`
	TriageScore       int        `json:"triageScore"`
	CompletedAt       *time.Time `json:"completedAt"`
}
type activity struct {
	ID           string    `json:"id"`
	BoardID      string    `json:"boardId"`
	Revision     uint64    `json:"revision"`
	At           time.Time `json:"at"`
	ActorID      string    `json:"actorId"`
	Operation    string    `json:"operation"`
	ResourceType string    `json:"resourceType"`
	ResourceID   string    `json:"resourceId"`
	Field        *string   `json:"field"`
	Before       *string   `json:"before"`
	After        *string   `json:"after"`
}
type snapshot struct {
	Format        string            `json:"format"`
	FormatVersion int               `json:"formatVersion"`
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
	s := snapshot{Format: "localswim-snapshot", FormatVersion: 1,
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
