// Package cover identifies the protected archive members used by explicit covers.
package cover

import A "github.com/localswim/localswim/internal/contract/archive"

// BlobPaths is called after document validation has established exact membership
// and full attachment metadata. Choices reuse the existing protected byte union.
func BlobPaths(board A.Board, history A.History) map[string]bool {
	selected := map[A.UUID]bool{}
	for _, card := range board.Cards {
		if card.CoverAttachmentID != nil {
			selected[*card.CoverAttachmentID] = true
		}
	}
	for _, record := range history.Records {
		if record.State != nil && record.State.CoverAttachmentID != nil {
			selected[*record.State.CoverAttachmentID] = true
		}
	}
	paths := map[string]bool{}
	if len(selected) == 0 {
		return paths
	}
	for _, attachment := range board.Attachments {
		if selected[attachment.AttachmentID] {
			paths["blobs/sha256-"+string(attachment.SHA256)] = true
		}
	}
	return paths
}
