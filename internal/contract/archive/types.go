// Package archive defines the accepted D02 in-memory boundary contracts.
// It performs no IO, authentication, publication, retention execution or ZIP parsing.
package archive

import "encoding/json"

type UUID string
type Digest string
type Timestamp string
type Number uint64
type Positive uint64
type Version uint64
type Days uint64
type Priority string
type Lane string
type Kind string
type Reason string
type GapClass string
type FactKind string
type Command string
type RecoveryCommand string
type ClearInput struct {
	ThroughTime Timestamp `json:"throughTime"`
}
type TerminalClass string
type Phase string
type ReceiptResult string
type RestoreOutcome string
type HashAlgorithm string
type RestoreScope string
type LinkKind string
type EntityKind string

type Identity struct {
	BoardID     UUID `json:"boardId"`
	Incarnation UUID `json:"incarnation"`
}
type CardRef struct {
	BoardID     UUID `json:"boardId"`
	Incarnation UUID `json:"incarnation"`
	CardID      UUID `json:"cardId"`
}
type Retention struct {
	HistoryDays  Days `json:"historyDays"`
	ActivityDays Days `json:"activityDays"`
	OutcomeDays  Days `json:"outcomeDays"`
}
type Floors struct {
	HistoryFloor        *Timestamp `json:"historyFloor"`
	ActivityFloor       *Timestamp `json:"activityFloor"`
	OutcomePayloadFloor *Timestamp `json:"outcomePayloadFloor"`
}
type Coverage struct {
	HistoryFloor        *Timestamp `json:"historyFloor"`
	ActivityFloor       *Timestamp `json:"activityFloor"`
	OutcomePayloadFloor *Timestamp `json:"outcomePayloadFloor"`
	Gaps                []Gap      `json:"gaps"`
}
type Gap struct {
	Origin           Identity   `json:"origin"`
	Class            GapClass   `json:"class"`
	Reason           Reason     `json:"reason"`
	FromExclusive    *Timestamp `json:"fromExclusive"`
	ThroughInclusive Timestamp  `json:"throughInclusive"`
	RecordedAt       Timestamp  `json:"recordedAt"`
	BoardRevision    Number     `json:"boardRevision"`
}
type GapSelection struct {
	Origin           Identity   `json:"origin"`
	Class            GapClass   `json:"class"`
	Reason           Reason     `json:"reason"`
	FromExclusive    *Timestamp `json:"fromExclusive"`
	ThroughInclusive Timestamp  `json:"throughInclusive"`
}
type Entry struct {
	Path       string `json:"path"`
	ByteLength Number `json:"byteLength"`
	SHA256     Digest `json:"sha256"`
}
type ByteFact struct {
	SHA256     Digest `json:"sha256"`
	ByteLength Number `json:"byteLength" validate:"blob-length"`
}
type Lineage struct {
	Identity       Identity `json:"identity"`
	ManifestSHA256 Digest   `json:"manifestSha256"`
	Revision       Number   `json:"revision"`
}
type Manifest struct {
	FormatVersion    Version       `json:"formatVersion"`
	SchemaVersion    Version       `json:"schemaVersion"`
	HashAlgorithm    HashAlgorithm `json:"hashAlgorithm"`
	Source           Identity      `json:"source"`
	Revision         Number        `json:"revision"`
	ExportedAt       Timestamp     `json:"exportedAt"`
	Lineage          []Lineage     `json:"lineage"`
	Retention        Retention     `json:"retention"`
	RetentionVersion Positive      `json:"retentionVersion"`
	Coverage         Coverage      `json:"coverage"`
	Inventory        []Entry       `json:"inventory"`
}
type Actor struct {
	ActorID   UUID     `json:"actorId"`
	Kind      Kind     `json:"kind"`
	Label     string   `json:"label"`
	CreatedIn Identity `json:"createdIn"`
}
type Card struct {
	CardID            UUID     `json:"cardId"`
	Ticket            Positive `json:"ticket"`
	Revision          Positive `json:"revision"`
	Title             string   `json:"title" validate:"title"`
	Description       string   `json:"description" validate:"prose"`
	Priority          Priority `json:"priority"`
	Lane              Lane     `json:"lane"`
	Archived          bool     `json:"archived"`
	Parent            *CardRef `json:"parent,omitempty"`
	OwnerID           *UUID    `json:"ownerId,omitempty"`
	CurrentApprovalID *UUID    `json:"currentApprovalId"`
	CommentIDs        []UUID   `json:"commentIds"`
	LinkIDs           []UUID   `json:"linkIds"`
	AttachmentIDs     []UUID   `json:"attachmentIds"`
	CreatedIn         Identity `json:"createdIn"`
}
type Comment struct {
	CommentID UUID      `json:"commentId"`
	CardID    UUID      `json:"cardId"`
	ActorID   UUID      `json:"actorId"`
	Text      string    `json:"text" validate:"comment"`
	CreatedAt Timestamp `json:"createdAt"`
	CreatedIn Identity  `json:"createdIn"`
}
type Link struct {
	LinkID    UUID      `json:"linkId"`
	CardID    UUID      `json:"cardId"`
	Kind      LinkKind  `json:"kind"`
	Target    CardRef   `json:"target"`
	Active    bool      `json:"active"`
	CreatedAt Timestamp `json:"createdAt"`
	CreatedIn Identity  `json:"createdIn"`
}
type Attachment struct {
	AttachmentID UUID      `json:"attachmentId"`
	CardID       UUID      `json:"cardId"`
	Name         string    `json:"name" validate:"attachment-name"`
	MediaLabel   string    `json:"mediaLabel" validate:"media-label"`
	SHA256       Digest    `json:"sha256"`
	ByteLength   Number    `json:"byteLength" validate:"blob-length"`
	CreatedAt    Timestamp `json:"createdAt"`
	CreatedIn    Identity  `json:"createdIn"`
}
type Approval struct {
	ApprovalID            UUID      `json:"approvalId"`
	CardID                UUID      `json:"cardId"`
	ActorID               UUID      `json:"actorId"`
	ReviewedCardRevision  Positive  `json:"reviewedCardRevision"`
	ReviewedBoardRevision Number    `json:"reviewedBoardRevision"`
	PolicyVersion         Version   `json:"policyVersion"`
	CreatedAt             Timestamp `json:"createdAt"`
	CreatedIn             Identity  `json:"createdIn"`
}
type EntityTombstone struct {
	Kind      EntityKind `json:"kind"`
	ID        UUID       `json:"id"`
	CardID    UUID       `json:"cardId"`
	CreatedIn Identity   `json:"createdIn"`
	ActorID   *UUID      `json:"actorId"`
	CreatedAt *Timestamp `json:"createdAt"`
	RemovedAt Timestamp  `json:"removedAt"`
	Reason    Reason     `json:"reason"`
}
type Board struct {
	SchemaVersion    Version           `json:"schemaVersion"`
	Identity         Identity          `json:"identity"`
	Revision         Number            `json:"revision"`
	NextTicket       Positive          `json:"nextTicket"`
	PolicyVersion    Version           `json:"policyVersion"`
	Retention        Retention         `json:"retention"`
	RetentionVersion Positive          `json:"retentionVersion"`
	Coverage         Coverage          `json:"coverage"`
	Actors           []Actor           `json:"actors"`
	Cards            []Card            `json:"cards"`
	Comments         []Comment         `json:"comments"`
	Links            []Link            `json:"links"`
	Attachments      []Attachment      `json:"attachments"`
	Approvals        []Approval        `json:"approvals"`
	Tombstones       []EntityTombstone `json:"tombstones"`
}
type SnapshotState struct {
	Ticket            Positive `json:"ticket"`
	Title             string   `json:"title" validate:"title"`
	Description       string   `json:"description" validate:"prose"`
	Priority          Priority `json:"priority"`
	Lane              Lane     `json:"lane"`
	Archived          bool     `json:"archived"`
	Parent            *CardRef `json:"parent,omitempty"`
	OwnerID           *UUID    `json:"ownerId,omitempty"`
	CurrentApprovalID *UUID    `json:"currentApprovalId"`
}
type SnapshotReferences struct {
	CommentIDs    []UUID `json:"commentIds"`
	LinkIDs       []UUID `json:"linkIds"`
	AttachmentIDs []UUID `json:"attachmentIds"`
	ApprovalIDs   []UUID `json:"approvalIds"`
}
type Removal struct {
	Reason        Reason    `json:"reason"`
	RemovedAt     Timestamp `json:"removedAt"`
	BoardRevision Number    `json:"boardRevision"`
}
type Snapshot struct {
	Origin       Identity           `json:"origin"`
	CardID       UUID               `json:"cardId"`
	CardRevision Positive           `json:"cardRevision"`
	CommittedAt  Timestamp          `json:"committedAt"`
	ActorID      UUID               `json:"actorId"`
	State        *SnapshotState     `json:"state"`
	References   SnapshotReferences `json:"references"`
	Removal      *Removal           `json:"removal"`
}
type History struct {
	SchemaVersion Version    `json:"schemaVersion"`
	Identity      Identity   `json:"identity"`
	Revision      Number     `json:"revision"`
	Records       []Snapshot `json:"records"`
}
type FactRef struct {
	Kind     FactKind `json:"kind"`
	ID       UUID     `json:"id"`
	Revision *Number  `json:"revision"`
}
type Event struct {
	Origin        Identity  `json:"origin"`
	EventID       Positive  `json:"eventId"`
	ActorID       UUID      `json:"actorId"`
	OpID          *UUID     `json:"opId"`
	Type          string    `json:"type"`
	BoardRevision Number    `json:"boardRevision"`
	Affected      []FactRef `json:"affected"`
	At            Timestamp `json:"at"`
}
type Activity struct {
	SchemaVersion Version  `json:"schemaVersion"`
	Identity      Identity `json:"identity"`
	Revision      Number   `json:"revision"`
	Events        []Event  `json:"events"`
}

// Target is a required nullable discriminated object. Neither member is optional
// on wire: the command selects exactly one member or a null target.
type Target struct {
	CardID  *UUID `json:"cardId,omitempty"`
	GrantID *UUID `json:"grantId,omitempty"`
}

// Intent/result remain exact C01-owned command objects, validated by Validate.
// RawMessage preserves their typed command union without defining a competing model.
type FullOutcome struct {
	Intent json.RawMessage `json:"intent"`
	Result json.RawMessage `json:"result"`
}
type Operation struct {
	Origin                Identity      `json:"origin"`
	OpID                  UUID          `json:"opId"`
	ActorID               UUID          `json:"actorId"`
	FingerprintVersion    Version       `json:"fingerprintVersion"`
	Fingerprint           Digest        `json:"fingerprint"`
	Command               Command       `json:"command"`
	Target                *Target       `json:"target"`
	ExpectedRevision      Number        `json:"expectedRevision"`
	ExpectedCardRevision  *Positive     `json:"expectedCardRevision"`
	TerminalClass         TerminalClass `json:"terminalClass"`
	Code                  string        `json:"code"`
	TerminalAt            Timestamp     `json:"terminalAt"`
	CommittedRevision     *Positive     `json:"committedRevision"`
	CommittedCardRevision *Positive     `json:"committedCardRevision"`
	ResultRefs            []FactRef     `json:"resultRefs"`
	ByteFacts             []ByteFact    `json:"byteFacts"`
	Full                  *FullOutcome  `json:"full"`
	Removal               *Removal      `json:"removal"`
}
type SourceArchive struct {
	Identity   Identity    `json:"identity"`
	Operations []Operation `json:"operations"`
}
type Outcomes struct {
	SchemaVersion  Version         `json:"schemaVersion"`
	Identity       Identity        `json:"identity"`
	Revision       Number          `json:"revision"`
	Operations     []Operation     `json:"operations"`
	SourceArchives []SourceArchive `json:"sourceArchives"`
}
type HistoryKey struct {
	Origin       Identity `json:"origin"`
	CardID       UUID     `json:"cardId"`
	CardRevision Positive `json:"cardRevision"`
}
type ActivityKey struct {
	Origin  Identity `json:"origin"`
	EventID Positive `json:"eventId"`
}
type OutcomeKey struct {
	Origin Identity `json:"origin"`
	OpID   UUID     `json:"opId"`
}
type PreviewCounts struct {
	HistorySnapshots Number `json:"historySnapshots"`
	ActivityEvents   Number `json:"activityEvents"`
	OutcomePayloads  Number `json:"outcomePayloads"`
}
type PreviewBytes struct {
	DomainBefore      []ByteFact `json:"domainBefore"`
	DomainAfter       []ByteFact `json:"domainAfter"`
	ExportPinned      []ByteFact `json:"exportPinned"`
	Reclaimable       []ByteFact `json:"reclaimable"`
	DomainBeforeBytes Number     `json:"domainBeforeBytes"`
	DomainAfterBytes  Number     `json:"domainAfterBytes"`
	ExportPinnedBytes Number     `json:"exportPinnedBytes"`
	ReclaimableBytes  Number     `json:"reclaimableBytes"`
}
type PreviewInventory struct {
	History                  []HistoryKey   `json:"history"`
	Activity                 []ActivityKey  `json:"activity"`
	Outcomes                 []OutcomeKey   `json:"outcomes"`
	Counts                   PreviewCounts  `json:"counts"`
	Bytes                    PreviewBytes   `json:"bytes"`
	CoverageGaps             []GapSelection `json:"coverageGaps"`
	ProposedFloors           Floors         `json:"proposedFloors"`
	ProposedRetention        Retention      `json:"proposedRetention"`
	ProposedRetentionVersion Positive       `json:"proposedRetentionVersion"`
	ProposedBoardRevision    Positive       `json:"proposedBoardRevision"`
}
type PreviewRequest struct {
	Incarnation      UUID            `json:"incarnation"`
	ExpectedRevision Number          `json:"expectedRevision"`
	Command          RecoveryCommand `json:"command"`
	Input            json.RawMessage `json:"input"`
}
type Preview struct {
	APIVersion       Version          `json:"apiVersion"`
	BoardID          UUID             `json:"boardId"`
	Incarnation      UUID             `json:"incarnation"`
	ActorID          UUID             `json:"actorId"`
	ExpectedRevision Number           `json:"expectedRevision"`
	RetentionVersion Positive         `json:"retentionVersion"`
	Command          RecoveryCommand  `json:"command"`
	Input            json.RawMessage  `json:"input"`
	PreviewedAt      Timestamp        `json:"previewedAt"`
	ExpiresAt        Timestamp        `json:"expiresAt"`
	Inventory        PreviewInventory `json:"inventory"`
	PreviewHash      Digest           `json:"previewHash"`
}
type Confirmation struct {
	OpID                 UUID            `json:"opId"`
	Incarnation          UUID            `json:"incarnation"`
	Target               *Target         `json:"target"`
	ExpectedRevision     Number          `json:"expectedRevision"`
	ExpectedCardRevision *Positive       `json:"expectedCardRevision"`
	Command              RecoveryCommand `json:"command"`
	Input                json.RawMessage `json:"input"`
	PreviewHash          Digest          `json:"previewHash"`
}
type TargetInventory struct {
	SchemaVersion Version `json:"schemaVersion"`
	Entries       []Entry `json:"entries"`
	EntryCount    Number  `json:"entryCount"`
	SHA256        Digest  `json:"sha256"`
}
type Receipt struct {
	ReceiptVersion       Version          `json:"receiptVersion"`
	RequestID            UUID             `json:"requestId"`
	SourceManifest       Manifest         `json:"sourceManifest"`
	SourceManifestSHA256 Digest           `json:"sourceManifestSha256"`
	Destination          Identity         `json:"destination"`
	TargetInventory      *TargetInventory `json:"targetInventory"`
	Phase                Phase            `json:"phase"`
	Result               ReceiptResult    `json:"result"`
	FailureCode          *string          `json:"failureCode"`
	CreatedAt            Timestamp        `json:"createdAt"`
	UpdatedAt            Timestamp        `json:"updatedAt"`
}
type RestoreResult struct {
	SourceIdentity *Identity `json:"sourceIdentity"`
	SourceRevision *Number   `json:"sourceRevision"`
	Destination    *Identity `json:"destination"`
}
type Status struct {
	APIVersion Version        `json:"apiVersion"`
	Scope      RestoreScope   `json:"scope"`
	Outcome    RestoreOutcome `json:"outcome"`
	Result     RestoreResult  `json:"result"`
}
