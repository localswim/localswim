package v2

import (
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
)

type v1BoardExport struct {
	Schema       int      `json:"schema"`
	Revision     uint64   `json:"revision"`
	Project      string   `json:"project"`
	Port         int      `json:"port"`
	Policy       v1Policy `json:"policy"`
	Users        []v1User `json:"users"`
	BrowserUser  string   `json:"browserUser"`
	CLIUser      string   `json:"cliUser"`
	DefaultOwner string   `json:"defaultOwner"`
	NextTicket   uint64   `json:"nextTicket"`
	Items        []v1Item `json:"items"`
	Links        []v1Link `json:"links"`

	RelationshipHistory json.RawMessage `json:"relationshipHistory"`
	ParentHistory       json.RawMessage `json:"parentHistory"`
	SubjectHistory      json.RawMessage `json:"subjectHistory"`
	DetailHistory       json.RawMessage `json:"detailHistory"`
	ArchiveHistory      json.RawMessage `json:"archiveHistory"`
	TicketHistory       json.RawMessage `json:"ticketHistory"`
}

type v1User struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Class string `json:"class"`
	Color string `json:"color"`
}

type v1Policy struct {
	Schema          int                 `json:"schema"`
	Note            string              `json:"note"`
	DefaultPriority *string             `json:"defaultPriority,omitempty"`
	Priorities      []v1Priority        `json:"priorities"`
	Lanes           []v1Lane            `json:"lanes"`
	RetiredLanes    []string            `json:"retiredLanes"`
	Edges           map[string][]v1Edge `json:"edges"`
}

type v1Priority struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type v1Lane struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Note            string   `json:"note"`
	Create          []string `json:"create"`
	Reorder         bool     `json:"reorderByDependencies"`
	ApprovalEnabled bool     `json:"approveAll"`
	ApprovalMessage string   `json:"approvalMessage"`
}

type v1Edge struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Description string `json:"description"`
}

type v1Item struct {
	ID                string              `json:"id"`
	Ticket            uint64              `json:"ticket"`
	OriginTicket      uint64              `json:"originTicket"`
	State             string              `json:"state"`
	Priority          string              `json:"priority"`
	Subject           string              `json:"subject"`
	Detail            *string             `json:"detail"`
	Owner             string              `json:"owner"`
	Parent            string              `json:"parent"`
	Archived          bool                `json:"archived"`
	CoverAttachment   string              `json:"coverAttachment"`
	History           []v1CardHistory     `json:"history"`
	Comments          []v1Comment         `json:"comments"`
	Attachments       []v1Attachment      `json:"attachments"`
	AttachmentHistory []v1AttachmentEvent `json:"attachmentHistory"`
}

type v1CardHistory struct {
	At           string  `json:"at"`
	By           string  `json:"by"`
	From         *string `json:"from"`
	To           string  `json:"to"`
	OwnerFrom    *string `json:"ownerFrom"`
	OwnerTo      *string `json:"ownerTo"`
	PriorityFrom *string `json:"priorityFrom"`
	PriorityTo   *string `json:"priorityTo"`
}

type v1Link struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
}

type v1Comment struct {
	At   string `json:"at"`
	By   string `json:"by"`
	Text string `json:"text"`
}

type v1Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	MediaType string `json:"mediaType"`
	SHA256    string `json:"sha256"`
	At        string `json:"at"`
	By        string `json:"by"`
}

type v1AttachmentEvent struct {
	At           string `json:"at"`
	By           string `json:"by"`
	Action       string `json:"action"`
	AttachmentID string `json:"attachmentId"`
	Extension    string `json:"extension"`
}

func (policy v1Policy) omittedAnnotations() int {
	count := 0
	if policy.Note != "" {
		count++
	}
	for _, edges := range policy.Edges {
		for _, edge := range edges {
			if edge.Description != "" {
				count++
			}
		}
	}
	return count
}

func readV1Board(source io.Reader) (v1BoardExport, error) {
	var value v1BoardExport
	data, err := io.ReadAll(io.LimitReader(source, maxSnapshotBytes+1))
	if err != nil {
		return value, fmt.Errorf("read V1 board export: %w", err)
	}
	if len(data) > maxSnapshotBytes {
		return value, invalid("source", "V1 board export exceeds the import size limit.")
	}
	if err := jsonv2.Unmarshal(data, &value, jsonv2.RejectUnknownMembers(true)); err != nil {
		return value, fmt.Errorf("read V1 board export: %w", err)
	}
	if (value.Schema != 4 && value.Schema != 6) || (value.Policy.Schema != 6 && value.Policy.Schema != 7) {
		return value, invalid("source", "Expected V1 board schema 4 or 6 with embedded policy schema 6 or 7.")
	}
	if value.Items == nil || len(value.Users) == 0 || len(value.Policy.Lanes) == 0 || len(value.Policy.Priorities) == 0 || value.NextTicket == 0 {
		return value, invalid("source", "Expected the complete V1 board show --json export.")
	}
	if (value.Policy.Schema == 6 && value.Policy.RetiredLanes != nil) || (value.Policy.Schema == 7 && len(value.Policy.RetiredLanes) == 0) {
		return value, invalid("policy.retiredLanes", "Retirement must match the V1 policy schema.")
	}
	if len(value.Policy.Edges) != 2 || value.Policy.Edges[value.BrowserUser] == nil || value.Policy.Edges[value.CLIUser] == nil {
		return value, invalid("policy.edges", "Expected V1 move grants for the browser and CLI users.")
	}
	return value, nil
}

// Only blocks contributes precedence. All current cards, including archived and
// completed cards, participate, and each ready layer is ordered by source ticket.
func orderV1Cards(value v1BoardExport) ([]v1Item, error) {
	cards := make(map[string]v1Item, len(value.Items))
	tickets := make(map[uint64]bool, len(value.Items))
	for _, item := range value.Items {
		if item.ID == "" || cards[item.ID].ID != "" || (item.Ticket > 0 && tickets[item.Ticket]) || item.Ticket >= value.NextTicket {
			return nil, fmt.Errorf("V1 card %q has a missing or repeated identity or invalid ticket #%04d", item.ID, item.Ticket)
		}
		cards[item.ID], tickets[item.Ticket] = item, true
	}
	for _, item := range value.Items {
		if item.Parent != "" && cards[item.Parent].ID == "" {
			return nil, fmt.Errorf("V1 ticket #%04d refers to missing parent %q", item.Ticket, item.Parent)
		}
	}
	indegree := make(map[string]int, len(cards))
	blocked := make(map[string][]string)
	keys := make(map[relationshipLink]bool, len(value.Links))
	for _, link := range value.Links {
		key := (relationshipLink{link.From, link.To, link.Kind}).key()
		if cards[link.From].ID == "" || cards[link.To].ID == "" || link.From == link.To || !validRelationshipKind(link.Kind) || keys[key] {
			return nil, fmt.Errorf("invalid V1 %q relationship from %q to %q", link.Kind, link.From, link.To)
		}
		keys[key] = true
		if link.Kind == "blocks" {
			indegree[link.To]++
			blocked[link.From] = append(blocked[link.From], link.To)
		}
	}
	ready := make([]v1Item, 0, len(cards))
	for _, item := range value.Items {
		if indegree[item.ID] == 0 {
			ready = append(ready, item)
		}
	}
	ordered := make([]v1Item, 0, len(cards))
	for len(ready) != 0 {
		sortV1Cards(ready)
		ordered = append(ordered, ready...)
		var next []v1Item
		for _, item := range ready {
			for _, successor := range blocked[item.ID] {
				indegree[successor]--
				if indegree[successor] == 0 {
					next = append(next, cards[successor])
				}
			}
		}
		ready = next
	}
	if len(ordered) != len(cards) {
		return nil, v1BlockingCycle(value.Items, indegree)
	}
	return ordered, nil
}

func sortV1Cards(cards []v1Item) {
	slices.SortFunc(cards, func(a, b v1Item) int {
		if a.Ticket != b.Ticket {
			if a.Ticket < b.Ticket {
				return -1
			}
			return 1
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func v1BlockingCycle(cards []v1Item, indegree map[string]int) error {
	var remaining []v1Item
	for _, item := range cards {
		if indegree[item.ID] > 0 {
			remaining = append(remaining, item)
		}
	}
	sortV1Cards(remaining)
	labels := make([]string, 0, min(len(remaining), 12))
	for _, item := range remaining[:min(len(remaining), 12)] {
		labels = append(labels, fmt.Sprintf("#%04d", item.Ticket))
	}
	return fmt.Errorf("V1 blocking cycle prevents ordering %d tickets on or downstream of a cycle (%s)", len(remaining), strings.Join(labels, ", "))
}

func v1Timestamp(value string) (time.Time, error) {
	value = v1TrimSpace(value)
	if parsed, ok := parseV1Timestamp(value); ok {
		return parsed, nil
	}
	// V1's legacy display parser treats a trailing zone word as a label
	// and reads the preceding timestamp using the source machine's local zone.
	if index := strings.LastIndexByte(value, ' '); index > 0 && strings.IndexFunc(value[index+1:], func(char rune) bool {
		return !unicode.IsLetter(char)
	}) < 0 {
		if parsed, ok := parseV1Timestamp(value[:index]); ok {
			return parsed, nil
		}
	}
	return time.Time{}, invalid("timestamp", "Expected an ISO V1 timestamp or a legacy local date and time.")
}

func parseV1Timestamp(value string) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00", "2006-01-02T15:04:05Z0700", "2006-01-02 15:04:05Z0700",
		"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if result, err := time.ParseInLocation(layout, value, time.Local); err == nil && validSnapshotTime(canonicalTime(result)) {
			return canonicalTime(result), true
		}
	}
	return time.Time{}, false
}

func v1TrimSpace(value string) string {
	return strings.TrimFunc(value, func(char rune) bool { return unicode.IsSpace(char) || char >= 0x1c && char <= 0x1f })
}
