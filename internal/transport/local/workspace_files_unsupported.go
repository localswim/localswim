//go:build !windows || !amd64

package local

import "os"

func workspacePath(string, bool, bool, bool) (*WorkspacePath, error) { return nil, ErrWorkspaceFiles }
func workspacePathWithAncestor(string, bool, bool, bool, *WorkspacePath) (*WorkspacePath, error) {
	return nil, ErrWorkspaceFiles
}
func workspaceLock(string) (*os.File, error)             { return nil, ErrWorkspaceFiles }
func workspaceExistingLock(string) (*os.File, error)     { return nil, ErrWorkspaceFiles }
func workspaceRead(string, int64) ([]byte, error)        { return nil, ErrWorkspaceFiles }
func workspaceIdentity(string) (*os.File, []byte, error) { return nil, nil, ErrWorkspaceFiles }
func workspacePublish(string, []byte, bool) error        { return ErrWorkspaceFiles }
func workspaceRemove(string) error                       { return ErrWorkspaceFiles }
