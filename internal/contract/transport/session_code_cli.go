package transport

import (
	"fmt"

	A "github.com/localswim/localswim/internal/contract/archive"
)

type SessionCodeScope string
type SessionCodeCommand string
type SessionCodeOutcome string

// SessionCodeResult contains only observed public metadata. Every nullable key
// is required; unknown facts remain nil. Presented means a complete private
// console write, never login, delivery to a browser or a retained receipt.
type SessionCodeResult struct {
	InstallationID *UUIDv4      `json:"installationId"`
	BoardID        *A.UUID      `json:"boardId"`
	Incarnation    *A.UUID      `json:"incarnation"`
	GrantID        *A.UUID      `json:"grantId"`
	ActorID        *A.UUID      `json:"actorId"`
	IssuedAt       *A.Timestamp `json:"issuedAt"`
	ExpiresAt      *A.Timestamp `json:"expiresAt"`
	Presented      bool         `json:"presented"`
}

// SessionCodeCLI is the CLI-only, secret-free issue status. It adds no server
// route or domain receipt. Callers supply inert diagnostics and must never put
// secret values or private paths in diagnostic strings.
type SessionCodeCLI struct {
	APIVersion A.Version          `json:"apiVersion"`
	Scope      SessionCodeScope   `json:"scope"`
	Command    SessionCodeCommand `json:"command"`
	Outcome    SessionCodeOutcome `json:"outcome"`
	Result     *SessionCodeResult `json:"result"`
	Error      *CredentialError   `json:"error"`
}

func sessionCodeCLI(x SessionCodeCLI) error {
	if x.Outcome == "presented" {
		r := x.Result
		if x.Error != nil || r == nil || !r.Presented || r.InstallationID == nil || r.BoardID == nil || r.Incarnation == nil || r.GrantID == nil || r.ActorID == nil || r.IssuedAt == nil || r.ExpiresAt == nil {
			return fmt.Errorf("presented code public facts")
		}
	} else if x.Error == nil || (x.Result != nil && x.Result.Presented) {
		return fmt.Errorf("unpresented code diagnostic required")
	}
	return nil
}

func sessionCodeResult(x SessionCodeResult) error {
	if x.IssuedAt != nil && x.ExpiresAt != nil && !duration(*x.IssuedAt, *x.ExpiresAt, 60) {
		return fmt.Errorf("code status expiry")
	}
	return nil
}
