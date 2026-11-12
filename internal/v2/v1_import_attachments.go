package v2

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var v1AttachmentID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var v1AttachmentExtension = regexp.MustCompile(`^\.[a-z0-9]{1,16}$`)

type v1AttachmentSource struct {
	path string
	file attachment
}

func (replay *v1Replay) addAttachments(item *card, source v1Item, actors map[string]string) error {
	uploads := make(map[string]v1AttachmentEvent, len(source.Attachments))
	for _, event := range source.AttachmentHistory {
		switch event.Action {
		case "attached":
			uploads[event.AttachmentID] = event
		case "attachment_removed":
			delete(uploads, event.AttachmentID)
		}
	}
	ids := make(map[string]string, len(source.Attachments))
	for _, original := range source.Attachments {
		at, err := v1Timestamp(original.At)
		if err != nil || actors[original.By] == "" {
			return fmt.Errorf("V1 ticket #%04d attachment has an invalid timestamp or uploader %q", source.Ticket, original.By)
		}
		if at.After(replay.now) {
			return fmt.Errorf("V1 ticket #%04d attachment %s has an upload time after the import clock; correct the source or machine clock before importing", source.Ticket, original.ID)
		}
		if !v1AttachmentID.MatchString(original.ID) || ids[original.ID] != "" ||
			(original.MediaType != "application/octet-stream" && !previewableAttachment(original.MediaType)) ||
			!validV1AttachmentName(original.Name) {
			return fmt.Errorf("V1 ticket #%04d has invalid or repeated attachment metadata", source.Ticket)
		}
		ticket := source.OriginTicket
		if ticket == 0 {
			ticket = source.Ticket
		}
		if ticket == 0 {
			return fmt.Errorf("V1 card %q has an attachment without a storage ticket", source.ID)
		}
		// V1 verification requires each active attachment to have an upload
		// event. Its stored suffix reflects the source platform's filename rules.
		upload := uploads[original.ID]
		if !v1AttachmentExtension.MatchString(upload.Extension) || upload.At != original.At || upload.By != original.By {
			return fmt.Errorf("V1 ticket #%04d attachment %s lacks a matching active upload event with a valid extension", source.Ticket, original.ID)
		}
		path := filepath.Join(fmt.Sprintf("%04d", ticket), original.ID+upload.Extension)
		file := attachment{ID: newID(), BoardID: item.BoardID, CardID: item.ID, CreatedAt: at, UpdatedAt: at,
			OriginalName: original.Name, Size: original.Size, SHA256: original.SHA256, AddedBy: actors[original.By], Active: true}
		ref := v1AttachmentSource{path: path, file: file}
		data, err := ref.read(replay.sourceFiles)
		if err != nil {
			return err
		}
		// V1 labels all non-raster files as octet-stream. V2 stores the sniffed
		// media type and still serves non-previewable content as a download.
		file.MediaType = http.DetectContentType(data)
		file.Previewable = previewableAttachment(file.MediaType)
		if original.ID == source.CoverAttachment && !file.Previewable {
			return fmt.Errorf("V1 ticket #%04d cover %s is not previewable from its bytes in V2", source.Ticket, path)
		}
		file.Revision = replay.state.Board.Revision + 1
		after := attachmentAuditValue(file)
		item.Revision = replay.record("attach", "attachment", file.ID, nil, nil, &after)
		item.AttachmentCount++
		ids[original.ID] = file.ID
		replay.state.Attachments = append(replay.state.Attachments, file)
		replay.attachments = append(replay.attachments, v1AttachmentSource{path: path, file: file})
	}
	if source.CoverAttachment != "" {
		cover := ids[source.CoverAttachment]
		if cover == "" {
			return fmt.Errorf("V1 ticket #%04d has a cover without an active attachment", source.Ticket)
		}
		item.CoverAttachmentID = &cover
		item.Revision = replay.record("patch", "card", item.ID, new("cover_attachment_id"), nil, &cover)
	}
	// V1 removed file records contain the ID and extension, but no name,
	// digest or size. Their original files remain with the V1 source.
	detached := make(map[string]bool)
	for _, event := range source.AttachmentHistory {
		if event.Action == "attachment_removed" && ids[event.AttachmentID] == "" {
			detached[event.AttachmentID] = true
		}
	}
	replay.result.DetachedAttachmentsOmitted += len(detached)
	return nil
}

func validV1AttachmentName(name string) bool {
	return strings.TrimSpace(name) != "" && name != "." && name != ".." &&
		utf8.RuneCountInString(name) <= 255 && strings.IndexFunc(name, func(char rune) bool {
		return !unicode.IsPrint(char) || char == '/' || char == '\\'
	}) < 0
}

func (item v1AttachmentSource) read(root *os.Root) ([]byte, error) {
	bad := fmt.Errorf("V1 attachment %s is missing, changed, or does not match its size and SHA-256", item.path)
	if root == nil || item.file.Size <= 0 || item.file.Size > maxAttachmentBytes {
		return nil, bad
	}
	named, err := root.Lstat(item.path)
	if err != nil || !named.Mode().IsRegular() || named.Size() != item.file.Size {
		return nil, bad
	}
	file, err := openBuildFile(root, item.path)
	if err != nil {
		return nil, bad
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(named, opened) {
		return nil, bad
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil || int64(len(data)) != item.file.Size || fmt.Sprintf("%x", sha256.Sum256(data)) != item.file.SHA256 {
		return nil, bad
	}
	current, err := root.Lstat(item.path)
	if err != nil || !os.SameFile(opened, current) {
		return nil, bad
	}
	return data, nil
}

func (s *store) commitV1Replay(replay v1Replay) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := replay.state.Board.ID
	if err := s.checkCreateLocked(id); err != nil {
		return err
	}
	data, err := s.prepareCommit(replay.state, true)
	if err != nil {
		return err
	}
	published := make([]*publishedAttachment, 0, len(replay.attachments))
	defer func() {
		for _, file := range published {
			if err != nil && !errors.Is(err, errWriteUncertain) {
				file.discard()
			}
			_ = file.dir.Close()
		}
	}()
	for _, source := range replay.attachments {
		data, err := source.read(replay.sourceFiles)
		if err != nil {
			return err
		}
		file, err := publishAttachment(s.root, source.file, data)
		if file != nil {
			published = append(published, file)
		}
		if err != nil {
			return err
		}
	}
	return s.finishCommit(replay.state, data, s.writeNewSnapshotWithRecoveryReference(replay.state, data, func() error {
		if err := s.checkCreateLocked(id); err != nil {
			return err
		}
		for index, file := range published {
			if _, err := readVerifiedAttachmentIdentity(s.root, replay.attachments[index].file, file.info); err != nil {
				return fmt.Errorf("V1 attachment %s changed before snapshot admission: %w", replay.attachments[index].path, err)
			}
		}
		return nil
	}))
}
