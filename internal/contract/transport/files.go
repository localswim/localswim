package transport

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func cardFilesContinuation(v CardFilesView) error {
	if v.NextCursor == nil {
		return nil
	}
	if len(v.Items) == 0 || len(*v.NextCursor) > 1024 {
		return fmt.Errorf("invalid file continuation")
	}
	raw, e := base64.RawURLEncoding.Strict().DecodeString(*v.NextCursor)
	if e != nil || base64.RawURLEncoding.EncodeToString(raw) != *v.NextCursor {
		return fmt.Errorf("invalid file continuation")
	}
	cursor, e := A.Decode[struct {
		Version           A.Version          `json:"version"`
		BoardID           A.UUID             `json:"boardId"`
		Incarnation       A.UUID             `json:"incarnation"`
		CardID            A.UUID             `json:"cardId"`
		BoardRevision     A.Number           `json:"boardRevision"`
		CardRevision      A.Positive         `json:"cardRevision"`
		View              FileReferenceState `json:"view"`
		AfterCreatedAt    A.Timestamp        `json:"afterCreatedAt"`
		AfterAttachmentID A.UUID             `json:"afterAttachmentId"`
	}](raw)
	if e != nil {
		return fmt.Errorf("invalid file continuation")
	}
	object, e := contract.DecodeJSON(raw)
	if e != nil {
		return fmt.Errorf("invalid file continuation")
	}
	canonical, e := contract.Canonical(object)
	last := v.Items[len(v.Items)-1].Attachment
	if e != nil || !bytes.Equal(canonical, raw) || cursor.Version != 1 || cursor.BoardID != v.Identity.BoardID || cursor.Incarnation != v.Identity.Incarnation || cursor.CardID != v.CardID || cursor.BoardRevision != v.Revision || cursor.CardRevision != v.CardRevision || cursor.View != v.View || cursor.AfterCreatedAt != last.CreatedAt || cursor.AfterAttachmentID != last.AttachmentID {
		return fmt.Errorf("file continuation binding")
	}
	return nil
}
