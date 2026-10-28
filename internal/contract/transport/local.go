package transport

import A "github.com/localswim/localswim/internal/contract/archive"

type StandaloneMode string
type WorkspaceMode string
type WorkspaceHumanMode string

type StandaloneRuntimeMode struct {
	APIVersion A.Version      `json:"apiVersion"`
	Mode       StandaloneMode `json:"mode"`
}
type WorkspaceRuntimeMode struct {
	APIVersion  A.Version     `json:"apiVersion"`
	Mode        WorkspaceMode `json:"mode"`
	WorkspaceID UUIDv4        `json:"workspaceId"`
}

// BoardBootstrapRequest is mandatory for a selected workspace member. The
// BoardID is already in the route; the remaining durable tuple prevents a
// stale selection from silently attaching to a replacement.
type BoardBootstrapRequest struct {
	APIVersion     A.Version `json:"apiVersion"`
	InstallationID UUIDv4    `json:"installationId"`
	Incarnation    A.UUID    `json:"incarnation"`
}
type LocalBootstrap struct {
	APIVersion     A.Version   `json:"apiVersion"`
	InstallationID UUIDv4      `json:"installationId"`
	BoardID        A.UUID      `json:"boardId"`
	Incarnation    A.UUID      `json:"incarnation"`
	Epoch          UUIDv4      `json:"epoch"`
	Session        SessionRead `json:"session"`
}
type WorkspaceBootstrap struct {
	APIVersion  A.Version          `json:"apiVersion"`
	WorkspaceID UUIDv4             `json:"workspaceId"`
	Epoch       UUIDv4             `json:"epoch"`
	Mode        WorkspaceHumanMode `json:"mode"`
}
