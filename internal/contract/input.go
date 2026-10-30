package contract

import (
	"encoding/json"
	"fmt"
)

// CommandInput is validated semantic input, without transport fields or secrets.
// Optional fields stay nil to preserve omission; admission applies the accepted
// create priority default without changing the original immutable intent.
type CommandInput interface{ commandInput() }

// OwnerInput preserves omission versus explicit null (unassignment).
type OwnerInput struct {
	Present bool
	Value   *string
}

func (o *OwnerInput) UnmarshalJSON(b []byte) error {
	o.Present = true
	return json.Unmarshal(b, &o.Value)
}
func (o OwnerInput) IsZero() bool                 { return !o.Present }
func (o OwnerInput) MarshalJSON() ([]byte, error) { return json.Marshal(o.Value) }

type CreateInput struct {
	OwnerID     OwnerInput `json:"ownerId,omitzero"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Priority    *string    `json:"priority"`
}
type EditInput struct {
	OwnerID     OwnerInput `json:"ownerId,omitzero"`
	Archived    *bool      `json:"archived,omitempty"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Priority    *string    `json:"priority"`
	Lane        *string    `json:"lane"`
}
type CommentInput struct {
	Text string `json:"text"`
}
type AttachmentInput struct {
	Name       string `json:"name"`
	MediaLabel string `json:"mediaLabel"`
	ByteLength uint64 `json:"byteLength"`
	SHA256     string `json:"sha256"`
}
type LinkAddInput struct {
	Kind         string `json:"kind"`
	TargetCardID string `json:"targetCardId"`
}
type LinkRemoveInput struct {
	LinkID string `json:"linkId"`
}
type EmptyInput struct{}
type ApproveInput struct {
	PolicyVersion uint64 `json:"policyVersion"`
}
type GrantCreateInput struct {
	GranteeActorID string   `json:"granteeActorId"`
	Kind           string   `json:"kind"`
	Rights         []string `json:"rights"`
}
type HistoryClearInput struct {
	ThroughTime string `json:"throughTime"`
}
type RetentionSetInput struct {
	HistoryDays  uint64 `json:"historyDays"`
	ActivityDays uint64 `json:"activityDays"`
	OutcomeDays  uint64 `json:"outcomeDays"`
}

func (CreateInput) commandInput()       {}
func (EditInput) commandInput()         {}
func (CommentInput) commandInput()      {}
func (AttachmentInput) commandInput()   {}
func (LinkAddInput) commandInput()      {}
func (LinkRemoveInput) commandInput()   {}
func (EmptyInput) commandInput()        {}
func (ApproveInput) commandInput()      {}
func (GrantCreateInput) commandInput()  {}
func (HistoryClearInput) commandInput() {}
func (RetentionSetInput) commandInput() {}

type Identity struct {
	BoardID     string `json:"boardId"`
	Incarnation string `json:"incarnation"`
}
type Target struct {
	CardID  string `json:"cardId,omitempty"`
	GrantID string `json:"grantId,omitempty"`
}
type Preconditions struct {
	ExpectedRevision     uint64
	ExpectedCardRevision *uint64
}
type semanticEnvelope struct {
	BoardID              string          `json:"boardId"`
	Incarnation          string          `json:"incarnation"`
	OpID                 string          `json:"opId"`
	ActorID              string          `json:"actorId"`
	Target               *Target         `json:"target"`
	ExpectedRevision     uint64          `json:"expectedRevision"`
	ExpectedCardRevision *uint64         `json:"expectedCardRevision"`
	Payload              json.RawMessage `json:"payload"`
}

func (i Intent) envelope() (semanticEnvelope, error) {
	var e semanticEnvelope
	err := json.Unmarshal(i.canonical, &e)
	return e, err
}

// Context returns copies of the frozen identities and exact preconditions.
func (i Intent) Context() (Identity, string, string, *Target, Preconditions, error) {
	e, err := i.envelope()
	return Identity{e.BoardID, e.Incarnation}, e.OpID, e.ActorID, e.Target, Preconditions{e.ExpectedRevision, e.ExpectedCardRevision}, err
}

// Input returns a fresh typed input value. Recovery previewHash remains separate
// from its inner input and is available in CanonicalBytes, never discarded.
func (i Intent) Input() (CommandInput, error) {
	e, err := i.envelope()
	if err != nil {
		return nil, err
	}
	raw := e.Payload
	if i.command == "history.clear" || i.command == "retention.set" {
		var p struct {
			Input json.RawMessage `json:"input"`
		}
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		raw = p.Input
	}
	var out CommandInput
	switch i.command {
	case "card.create":
		out = &CreateInput{}
	case "card.edit":
		out = &EditInput{}
	case "comment.add":
		out = &CommentInput{}
	case "attachment.add":
		out = &AttachmentInput{}
	case "link.add":
		out = &LinkAddInput{}
	case "link.remove":
		out = &LinkRemoveInput{}
	case "review-handoff", "grant.revoke":
		out = &EmptyInput{}
	case "approve":
		out = &ApproveInput{}
	case "grant.create":
		out = &GrantCreateInput{}
	case "history.clear":
		out = &HistoryClearInput{}
	case "retention.set":
		out = &RetentionSetInput{}
	default:
		return nil, fmt.Errorf("uninitialized intent")
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return nil, err
	}
	return out, nil
}
