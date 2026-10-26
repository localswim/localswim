// Package transport owns the accepted D03 public boundaries. Structural
// validation does not establish authentication, authority, delivery or noncommit.
package transport

import (
	"encoding/json"
	A "github.com/localswim/localswim/internal/contract/archive"
)

type TerminalOutcome string
type RetentionClass string
type RefusedOutcome string
type UnknownOutcome string
type BoardScope string
type CardScope string
type HistoryScope string
type SessionScope string
type RevokedOutcome string
type CredentialScope string
type CredentialCommand string
type CredentialOutcome string
type ExportScope string
type ExportOutcome string
type UUIDv4 string
type SecretCode string
type SecretToken string
type Rights []string

type Error struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Field   *string `json:"field"`
}
type Conflict struct {
	Incarnation  A.UUID      `json:"incarnation"`
	Revision     A.Number    `json:"revision"`
	CardRevision *A.Positive `json:"cardRevision"`
	CardID       *A.UUID     `json:"cardId"`
}
type RefusalResult struct {
	Error    Error     `json:"error"`
	Conflict *Conflict `json:"conflict"`
}

// Result is a C01-owned command result, never an arbitrary domain object. A
// pointer distinguishes required null from a full retained result object.
type Terminal struct {
	APIVersion           A.Version        `json:"apiVersion"`
	Outcome              TerminalOutcome  `json:"outcome"`
	BoardID              A.UUID           `json:"boardId"`
	Incarnation          A.UUID           `json:"incarnation"`
	OpID                 A.UUID           `json:"opId"`
	ActorID              A.UUID           `json:"actorId"`
	FingerprintVersion   A.Version        `json:"fingerprintVersion"`
	Fingerprint          A.Digest         `json:"fingerprint"`
	Command              A.Command        `json:"command"`
	Target               *A.Target        `json:"target"`
	ExpectedRevision     A.Number         `json:"expectedRevision"`
	ExpectedCardRevision *A.Positive      `json:"expectedCardRevision"`
	TerminalAt           A.Timestamp      `json:"terminalAt"`
	Code                 string           `json:"code"`
	Revision             *A.Positive      `json:"revision"`
	CardRevision         *A.Positive      `json:"cardRevision"`
	Retention            RetentionClass   `json:"retention"`
	ResultRefs           []A.FactRef      `json:"resultRefs"`
	ByteFacts            []A.ByteFact     `json:"byteFacts"`
	Result               *json.RawMessage `json:"result"`
	Removal              *A.Removal       `json:"removal"`
}
type KnownIdentityUnknown struct {
	APIVersion         A.Version      `json:"apiVersion"`
	Outcome            UnknownOutcome `json:"outcome"`
	BoardID            A.UUID         `json:"boardId"`
	Incarnation        A.UUID         `json:"incarnation"`
	OpID               A.UUID         `json:"opId"`
	ActorID            A.UUID         `json:"actorId"`
	FingerprintVersion A.Version      `json:"fingerprintVersion"`
	Fingerprint        A.Digest       `json:"fingerprint"`
	Error              Error          `json:"error"`
}
type NonterminalRefusal struct {
	APIVersion A.Version      `json:"apiVersion"`
	Outcome    RefusedOutcome `json:"outcome"`
	Error      Error          `json:"error"`
	Conflict   *Conflict      `json:"conflict"`
	OpID       *A.UUID        `json:"opId,omitempty"`
}

type BoardRead struct {
	APIVersion A.Version  `json:"apiVersion"`
	Scope      BoardScope `json:"scope"`
	Result     BoardView  `json:"result"`
}
type BoardView struct {
	Identity         A.Identity     `json:"identity"`
	Revision         A.Number       `json:"revision"`
	PolicyVersion    A.Version      `json:"policyVersion"`
	Retention        A.Retention    `json:"retention"`
	RetentionVersion A.Positive     `json:"retentionVersion"`
	Coverage         A.Coverage     `json:"coverage"`
	Actors           []A.Actor      `json:"actors"`
	Cards            []A.Card       `json:"cards"`
	Comments         []A.Comment    `json:"comments"`
	Links            []A.Link       `json:"links"`
	Attachments      []A.Attachment `json:"attachments"`
	Approvals        []A.Approval   `json:"approvals"`
}
type CardRead struct {
	APIVersion A.Version `json:"apiVersion"`
	Scope      CardScope `json:"scope"`
	Result     CardView  `json:"result"`
}
type CardView struct {
	Identity          A.Identity         `json:"identity"`
	Revision          A.Number           `json:"revision"`
	Card              A.Card             `json:"card"`
	Actors            []A.Actor          `json:"actors"`
	Comments          []A.Comment        `json:"comments"`
	Links             []A.Link           `json:"links"`
	Attachments       []A.Attachment     `json:"attachments"`
	Approvals         []A.Approval       `json:"approvals"`
	HistoryReferences []HistoryReference `json:"historyReferences"`
	Coverage          A.Coverage         `json:"coverage"`
}
type HistoryReference struct {
	Origin       A.Identity  `json:"origin"`
	CardID       A.UUID      `json:"cardId"`
	CardRevision A.Positive  `json:"cardRevision"`
	CommittedAt  A.Timestamp `json:"committedAt"`
	Available    bool        `json:"available"`
}
type HistoryRead struct {
	APIVersion A.Version    `json:"apiVersion"`
	Scope      HistoryScope `json:"scope"`
	Result     HistoryView  `json:"result"`
}
type HistoryView struct {
	Identity     A.Identity          `json:"identity"`
	Revision     A.Number            `json:"revision"`
	CardID       A.UUID              `json:"cardId"`
	CardRevision A.Positive          `json:"cardRevision"`
	Coverage     A.Coverage          `json:"coverage"`
	Records      []A.Snapshot        `json:"records"`
	Actors       []A.Actor           `json:"actors"`
	Comments     []A.Comment         `json:"comments"`
	Links        []A.Link            `json:"links"`
	Attachments  []A.Attachment      `json:"attachments"`
	Approvals    []A.Approval        `json:"approvals"`
	Tombstones   []A.EntityTombstone `json:"tombstones"`
}

type SessionGrant struct {
	GrantID     A.UUID `json:"grantId"`
	BoardID     A.UUID `json:"boardId"`
	Incarnation A.UUID `json:"incarnation"`
	Rights      Rights `json:"rights"`
}
type SessionRead struct {
	APIVersion A.Version    `json:"apiVersion"`
	ActorID    A.UUID       `json:"actorId"`
	Kind       A.Kind       `json:"kind"`
	Grant      SessionGrant `json:"grant"`
}
type CodeRequest struct {
	InstallationID UUIDv4 `json:"installationId"`
	BoardID        A.UUID `json:"boardId"`
	Incarnation    A.UUID `json:"incarnation"`
	GrantID        A.UUID `json:"grantId"`
}
type CredentialIssueRequest = CodeRequest
type CodeSecret struct {
	APIVersion     A.Version   `json:"apiVersion"`
	InstallationID UUIDv4      `json:"installationId"`
	BoardID        A.UUID      `json:"boardId"`
	Incarnation    A.UUID      `json:"incarnation"`
	GrantID        A.UUID      `json:"grantId"`
	ActorID        A.UUID      `json:"actorId"`
	Code           SecretCode  `json:"code"`
	IssuedAt       A.Timestamp `json:"issuedAt"`
	ExpiresAt      A.Timestamp `json:"expiresAt"`
}
type ExchangeRequest struct {
	Code SecretCode `json:"code"`
}
type ExchangeSecret struct {
	APIVersion    A.Version   `json:"apiVersion"`
	Token         SecretToken `json:"token"`
	Session       SessionRead `json:"session"`
	IssuedAt      A.Timestamp `json:"issuedAt"`
	IdleExpiresAt A.Timestamp `json:"idleExpiresAt"`
	ExpiresAt     A.Timestamp `json:"expiresAt"`
}
type CredentialSecret struct {
	APIVersion     A.Version   `json:"apiVersion"`
	InstallationID UUIDv4      `json:"installationId"`
	BoardID        A.UUID      `json:"boardId"`
	Incarnation    A.UUID      `json:"incarnation"`
	GrantID        A.UUID      `json:"grantId"`
	ActorID        A.UUID      `json:"actorId"`
	Kind           A.Kind      `json:"kind"`
	Rights         Rights      `json:"rights"`
	CredentialID   UUIDv4      `json:"credentialId"`
	IssuedAt       A.Timestamp `json:"issuedAt"`
	ExpiresAt      A.Timestamp `json:"expiresAt"`
	Token          SecretToken `json:"token"`
}
type SessionRevoked struct {
	APIVersion A.Version      `json:"apiVersion"`
	Scope      SessionScope   `json:"scope"`
	Outcome    RevokedOutcome `json:"outcome"`
}
type CredentialError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type CredentialResult struct {
	InstallationID   *UUIDv4      `json:"installationId"`
	BoardID          *A.UUID      `json:"boardId"`
	Incarnation      *A.UUID      `json:"incarnation"`
	GrantID          *A.UUID      `json:"grantId"`
	ActorID          *A.UUID      `json:"actorId"`
	CredentialID     *UUIDv4      `json:"credentialId"`
	IssuedAt         *A.Timestamp `json:"issuedAt"`
	ExpiresAt        *A.Timestamp `json:"expiresAt"`
	LocallyAvailable bool         `json:"locallyAvailable"`
}
type CredentialCLI struct {
	APIVersion A.Version         `json:"apiVersion"`
	Scope      CredentialScope   `json:"scope"`
	Command    CredentialCommand `json:"command"`
	Outcome    CredentialOutcome `json:"outcome"`
	Result     *CredentialResult `json:"result"`
	Error      *CredentialError  `json:"error"`
}
type EmptyInput struct{}
type ExportRequest struct {
	Incarnation      A.UUID     `json:"incarnation"`
	ExpectedRevision A.Number   `json:"expectedRevision"`
	Input            EmptyInput `json:"input"`
}
type ExportResult struct {
	SourceIdentity *A.Identity `json:"sourceIdentity"`
	SourceRevision *A.Number   `json:"sourceRevision"`
	Destination    *EmptyInput `json:"destination"`
}
type ExportStatus struct {
	APIVersion A.Version     `json:"apiVersion"`
	Scope      ExportScope   `json:"scope"`
	Outcome    ExportOutcome `json:"outcome"`
	Result     ExportResult  `json:"result"`
}

// These remain standalone C03 objects: transport does not introduce wrappers.
type PreviewRequest = A.PreviewRequest
type Preview = A.Preview
type Confirmation = A.Confirmation
type RestoreStatus = A.Status
