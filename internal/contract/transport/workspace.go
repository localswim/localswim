package transport

import A "github.com/localswim/localswim/internal/contract/archive"

type WorkspaceKey string
type WorkspaceLabel string
type WorkspacePath string
type WorkspaceAction string
type WorkspaceAvailability string
type WorkspaceRegistrationStatus string

type WorkspaceIdentity struct {
	InstallationID UUIDv4 `json:"installationId"`
	BoardID        A.UUID `json:"boardId"`
	Incarnation    A.UUID `json:"incarnation"`
}
type WorkspaceProject struct {
	Key   WorkspaceKey   `json:"key"`
	Label WorkspaceLabel `json:"label"`
}
type WorkspaceProjectRequest struct {
	APIVersion             A.Version      `json:"apiVersion"`
	ExpectedConfigRevision A.Positive     `json:"expectedConfigRevision"`
	Key                    WorkspaceKey   `json:"key"`
	Label                  WorkspaceLabel `json:"label"`
	Root                   WorkspacePath  `json:"root"`
}
type WorkspaceBoardRequest struct {
	APIVersion             A.Version          `json:"apiVersion"`
	ExpectedConfigRevision A.Positive         `json:"expectedConfigRevision"`
	ProjectKey             WorkspaceKey       `json:"projectKey"`
	BoardKey               WorkspaceKey       `json:"boardKey"`
	Label                  WorkspaceLabel     `json:"label"`
	DataDir                WorkspacePath      `json:"dataDir"`
	Action                 WorkspaceAction    `json:"action"`
	ExpectedIdentity       *WorkspaceIdentity `json:"expectedIdentity"`
}
type WorkspaceStatusRequest struct {
	APIVersion       A.Version          `json:"apiVersion"`
	ProjectKey       WorkspaceKey       `json:"projectKey"`
	BoardKey         WorkspaceKey       `json:"boardKey"`
	DataDir          *WorkspacePath     `json:"dataDir"`
	ExpectedIdentity *WorkspaceIdentity `json:"expectedIdentity"`
}
type WorkspaceProjects struct {
	APIVersion     A.Version          `json:"apiVersion"`
	WorkspaceID    UUIDv4             `json:"workspaceId"`
	ConfigRevision A.Positive         `json:"configRevision"`
	Projects       []WorkspaceProject `json:"projects"`
}
type WorkspaceMember struct {
	ProjectKey   WorkspaceKey          `json:"projectKey"`
	BoardKey     WorkspaceKey          `json:"boardKey"`
	Label        WorkspaceLabel        `json:"label"`
	Availability WorkspaceAvailability `json:"availability"`
	Identity     *WorkspaceIdentity    `json:"identity"`
	MemberEpoch  *UUIDv4               `json:"memberEpoch"`
}
type WorkspaceBoards struct {
	APIVersion     A.Version         `json:"apiVersion"`
	WorkspaceID    UUIDv4            `json:"workspaceId"`
	ConfigRevision A.Positive        `json:"configRevision"`
	Boards         []WorkspaceMember `json:"boards"`
}
type WorkspaceResolve struct {
	APIVersion     A.Version       `json:"apiVersion"`
	WorkspaceID    UUIDv4          `json:"workspaceId"`
	ConfigRevision A.Positive      `json:"configRevision"`
	Member         WorkspaceMember `json:"member"`
}
type WorkspaceControlResult struct {
	APIVersion     A.Version                   `json:"apiVersion"`
	WorkspaceID    UUIDv4                      `json:"workspaceId"`
	ConfigRevision A.Positive                  `json:"configRevision"`
	Status         WorkspaceRegistrationStatus `json:"status"`
	ProjectKey     WorkspaceKey                `json:"projectKey"`
	BoardKey       *WorkspaceKey               `json:"boardKey"`
	Identity       *WorkspaceIdentity          `json:"identity"`
}
type WorkspaceControlError struct {
	APIVersion     A.Version      `json:"apiVersion"`
	Outcome        RefusedOutcome `json:"outcome"`
	Error          Error          `json:"error"`
	ConfigRevision A.Positive     `json:"configRevision"`
}
