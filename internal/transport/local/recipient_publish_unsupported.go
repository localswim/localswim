//go:build !windows || !amd64

package local

func recipientPublish(string, []byte) error { return ErrWorkspaceFiles }
