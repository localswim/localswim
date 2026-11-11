package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const maxAttachmentBytes = 10 << 20
const maxAttachmentBody = maxAttachmentBytes + (256 << 10)

var errAttachmentTagRequired = errors.New("attachment upload requires expected_card_etag")

type attachment struct {
	ID           string    `json:"id"`
	Revision     uint64    `json:"revision"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	BoardID      string    `json:"board_id"`
	CardID       string    `json:"card_id"`
	OriginalName string    `json:"original_name"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha2_256"`
	MediaType    string    `json:"media_type"`
	Previewable  bool      `json:"previewable"`
	Active       bool      `json:"active"`
	AddedBy      string    `json:"added_by"`
}

func validAttachmentName(name string) bool {
	return name != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 1024
}

func previewableAttachment(mediaType string) bool {
	switch mediaType {
	case "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func validateAttachmentMetadata(item attachment, index int) error {
	bad := func(field, detail string) error {
		return invalidSnapshot(fmt.Sprintf("attachments[%d].%s", index, field), detail)
	}
	if !validAttachmentName(item.OriginalName) {
		return bad("original_name", "Expected a UTF-8 name of 1 to 1024 characters.")
	}
	if item.Size < 0 || item.Size > maxAttachmentBytes {
		return bad("size", "Attachment size is outside the allowed range.")
	}
	if _, err := hex.DecodeString(item.SHA256); err != nil || len(item.SHA256) != 64 || item.SHA256 != strings.ToLower(item.SHA256) {
		return bad("sha2_256", "Expected a lowercase SHA-2-256 digest.")
	}
	mediaType, _, err := mime.ParseMediaType(item.MediaType)
	if err != nil || !strings.Contains(mediaType, "/") || len(item.MediaType) > 128 {
		return bad("media_type", "Expected a valid media type.")
	}
	if item.Previewable != previewableAttachment(mediaType) {
		return bad("previewable", "Preview eligibility does not match the media type.")
	}
	return nil
}

type attachmentUpload struct {
	id, cardID, expected, name string
	boardTag                   string
	data                       []byte
}

func (x *request) attachmentsCollection() {
	if x.r.Method == http.MethodPost {
		x.uploadAttachment()
		return
	}
	rawID, err := singleQuery(x.r, "card_id")
	if err != nil {
		x.fail(err)
		return
	}
	id, ok := normalizeID(rawID)
	if !ok {
		x.fail(invalid("card_id", "Expected a UUIDv4."))
		return
	}
	active, err := singleQuery(x.r, "active")
	if err != nil {
		x.fail(err)
		return
	}
	if active != "true" && active != "false" && (active != "" || x.r.URL.Query().Has("active")) {
		x.fail(invalid("active", "Expected true or false."))
		return
	}
	state, _, found := x.findCard(id)
	if !found || !state.can(x.actor, "read") {
		x.fail(errMissing)
		return
	}
	if !x.useBoard(state.Board.ID) {
		return
	}
	state, err = x.service.store.readback(state.Board.ID)
	if err != nil {
		x.fail(err)
		return
	}
	if !state.can(x.actor, "read") || !slices.ContainsFunc(state.Cards, func(item card) bool { return item.ID == id }) {
		x.fail(errMissing)
		return
	}
	var rows []attachment
	for _, item := range state.Attachments {
		if item.CardID == id && item.Active == (active != "false") {
			rows = append(rows, item)
		}
	}
	page(x, "attachments", rows)
}

func (x *request) attachmentResource(id string) {
	if x.r.Method == http.MethodDelete {
		for _, state := range x.service.store.list() {
			if slices.Contains(state.Board.UserIDs, x.actor) &&
				slices.ContainsFunc(state.Attachments, func(item attachment) bool { return item.ID == id }) {
				x.detachAttachment(state, id)
				return
			}
		}
		x.fail(errMissing)
		return
	}
	item, err := x.findAttachment(id)
	if err != nil {
		x.fail(err)
		return
	}
	x.respond(http.StatusOK, item, item.entityTag())
}

func (x *request) detachAttachment(state snapshot, id string) {
	if !state.can(x.actor, "manage_attachments") {
		x.fail(errForbidden)
		return
	}
	expected, err := x.ifMatch()
	if err != nil {
		x.fail(err)
		return
	}
	const header = "X-Expected-Board-ETag"
	boardTags := x.r.Header.Values(header)
	if len(boardTags) == 0 {
		x.problem(http.StatusPreconditionRequired, "precondition_required", header+" is required.", "", nil)
		return
	}
	if len(boardTags) != 1 || !snapshotETagPattern.MatchString(boardTags[0]) {
		x.fail(invalid(header, "Expected one canonical entity tag."))
		return
	}
	updated, err := x.updateBoard(state.Board.ID, boardTags[0], func(next *snapshot) error {
		return detachAttachment(next, id, expected, x.actor)
	})
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("Cache-Control", "no-store")
	x.w.Header().Set("X-Board-ETag", updated.Board.entityTag())
	x.w.WriteHeader(http.StatusNoContent)
}

func detachAttachment(state *snapshot, id, expected, actor string) error {
	if !slices.Contains(state.Board.UserIDs, actor) {
		return errMissing
	}
	if !state.can(actor, "manage_attachments") {
		return errForbidden
	}
	i := slices.IndexFunc(state.Attachments, func(item attachment) bool { return item.ID == id && item.Active })
	if i < 0 {
		return errMissing
	}
	item := &state.Attachments[i]
	if !matchesStrongTag(expected, item.entityTag()) {
		return errStale
	}
	i = slices.IndexFunc(state.Cards, func(c card) bool { return c.ID == item.CardID })
	if i < 0 {
		return errMissing
	}
	c := &state.Cards[i]
	now := maxCardTime(canonicalTime(time.Now()), state.Board.UpdatedAt)
	revision := state.Board.Revision + 1
	if c.CoverAttachmentID != nil && *c.CoverAttachmentID == id {
		recordCardAssignment(state, c.ID, actor, "cover_attachment_id", c.CoverAttachmentID, nil, now)
		c.CoverAttachmentID = nil
	}
	c.AttachmentCount--
	c.UpdatedAt = now
	c.Revision = revision
	item.Active = false
	item.UpdatedAt = now
	item.Revision = revision
	state.Activity = append(state.Activity, activity{ID: newOperationID(), BoardID: state.Board.ID,
		Revision: revision, At: now, ActorID: actor, Operation: "detach", ResourceType: "attachment", ResourceID: id})
	return nil
}

func (x *request) findAttachment(id string) (attachment, error) {
	for _, known := range x.service.store.list() {
		if known.can(x.actor, "read") && slices.ContainsFunc(known.Attachments, func(item attachment) bool {
			return item.ID == id
		}) {
			return x.attachmentReadback(known.Board.ID, id)
		}
	}
	// A just-committed ID may exist only in the fenced board's durable snapshot.
	if boardID := x.service.store.fencedBoard(); boardID != "" {
		if known, ok := x.service.store.accepted(boardID); ok && known.can(x.actor, "read") {
			return x.attachmentReadback(boardID, id)
		}
	}
	return attachment{}, errMissing
}

func (x *request) attachmentReadback(boardID, id string) (attachment, error) {
	state, err := x.service.store.readback(boardID)
	if err != nil {
		return attachment{}, err
	}
	if state.can(x.actor, "read") {
		for _, item := range state.Attachments {
			if item.ID == id {
				if err := x.admitBoard(boardID); err != nil {
					return attachment{}, err
				}
				return item, nil
			}
		}
	}
	return attachment{}, errMissing
}

func (x *request) readAttachmentUpload() (attachmentUpload, error) {
	var input attachmentUpload
	mediaType, params, err := mime.ParseMediaType(x.r.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/form-data" {
		return input, errMediaType
	}
	boundary := params["boundary"]
	if boundary == "" {
		return input, invalid("body", "The multipart boundary is required.")
	}
	if x.r.ContentLength > maxAttachmentBody {
		return input, errTooBig
	}
	data, err := io.ReadAll(http.MaxBytesReader(x.w, x.r.Body, maxAttachmentBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return input, errTooBig
		}
		return input, invalid("body", "Unreadable multipart request body.")
	}
	fields := map[string]string{}
	reader := multipart.NewReader(bytes.NewReader(data), boundary)
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return input, invalid("body", "Malformed multipart request body.")
		}
		disposition, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		field := params["name"]
		if err != nil || len(part.Header.Values("Content-Disposition")) != 1 || disposition != "form-data" || field == "" || part.Header.Get("Content-Transfer-Encoding") != "" {
			return input, invalid("body", "Malformed multipart form part.")
		}
		if err := readAttachmentPart(&input, fields, field, params, part); err != nil {
			return input, err
		}
	}
	var ok bool
	if input.cardID, ok = normalizeID(fields["card_id"]); !ok {
		return input, invalid("card_id", "Expected a UUIDv4.")
	}
	if rawID, supplied := fields["id"]; supplied {
		if input.id, ok = normalizeID(rawID); !ok {
			return input, invalid("id", "Expected a UUIDv4.")
		}
	} else {
		input.id = newID()
	}
	if input.name == "" {
		return input, invalid("file", "A file is required.")
	}
	input.expected, ok = fields["expected_card_etag"]
	if !ok {
		return input, errAttachmentTagRequired
	}
	if tags, valid := parseTagList(input.expected); !valid || len(tags) != 1 || tags[0].weak || tags[0].opaque != input.expected || input.expected == "*" {
		return input, invalid("expected_card_etag", "Expected one strong entity tag.")
	}
	return input, nil
}

func readAttachmentPart(input *attachmentUpload, fields map[string]string, field string, params map[string]string, part io.Reader) error {
	switch field {
	case "file":
		if input.name != "" {
			return invalid(field, "Supply exactly one file.")
		}
		name := params["filename"]
		if !validAttachmentName(name) {
			return invalid(field, "Expected a UTF-8 filename of 1 to 1024 characters.")
		}
		data, err := io.ReadAll(io.LimitReader(part, maxAttachmentBytes+1))
		if err != nil {
			return invalid(field, "Unreadable attachment bytes.")
		}
		if len(data) > maxAttachmentBytes {
			return errTooBig
		}
		input.name, input.data = name, data
	case "id", "card_id", "expected_card_etag":
		if _, exists := fields[field]; exists || params["filename"] != "" {
			return invalid(field, "Expected a single text field.")
		}
		data, err := io.ReadAll(io.LimitReader(part, 1025))
		if err != nil || len(data) > 1024 {
			return invalid(field, "Unreadable or oversized multipart field.")
		}
		fields[field] = string(data)
	default:
		for _, protected := range protectedFields {
			if strings.EqualFold(field, protected) {
				return invalid(protected, "Protected member.")
			}
		}
	}
	return nil
}

func (x *request) uploadAttachment() {
	input, err := x.readAttachmentUpload()
	if err != nil {
		switch {
		case errors.Is(err, errMediaType):
			x.problem(415, "unsupported_media_type", "Use multipart/form-data.", "", nil)
		case errors.Is(err, errAttachmentTagRequired):
			x.problem(428, "precondition_required", "expected_card_etag is required.", "", nil)
		default:
			x.fail(err)
		}
		return
	}
	const boardHeader = "X-Expected-Board-ETag"
	if tags := x.r.Header.Values(boardHeader); len(tags) != 0 {
		if len(tags) != 1 || !snapshotETagPattern.MatchString(tags[0]) {
			x.fail(invalid(boardHeader, "Expected one canonical entity tag."))
			return
		}
		input.boardTag = tags[0]
	}
	state, _, found := x.findCard(input.cardID)
	if !found {
		x.fail(errMissing)
		return
	}
	if !state.can(x.actor, "manage_attachments") {
		x.fail(errForbidden)
		return
	}
	if !x.useBoard(state.Board.ID) {
		return
	}
	value, boardTag, err := x.service.store.attach(state.Board.ID, x.actor, input)
	if err != nil {
		x.fail(err)
		return
	}
	x.w.Header().Set("Cache-Control", "no-store")
	x.w.Header().Set("X-Board-ETag", boardTag)
	x.w.Header().Set("Location", Prefix+"/attachments/"+value.ID)
	x.respond(http.StatusCreated, value, value.entityTag())
}

func (s *store) attach(boardID, actor string, input attachmentUpload) (attachment, string, error) {
	mediaType := http.DetectContentType(input.data)
	value := attachment{BoardID: boardID, CardID: input.cardID, OriginalName: input.name,
		Size: int64(len(input.data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(input.data)),
		MediaType: mediaType, Previewable: previewableAttachment(mediaType), Active: true, AddedBy: actor}
	var published *publishedAttachment
	definitelyUncommitted := false
	defer func() {
		if published != nil {
			if definitelyUncommitted {
				published.discard()
			}
			_ = published.dir.Close()
		}
	}()
	updated, err := s.updateChecked(boardID, nil, func(next *snapshot) error {
		if !slices.Contains(next.Board.UserIDs, actor) {
			return errMissing
		}
		if !next.can(actor, "manage_attachments") {
			return errForbidden
		}
		if input.boardTag != "" && !matchesStrongTag(input.boardTag, next.Board.entityTag()) {
			return errStale
		}
		index := slices.IndexFunc(next.Cards, func(item card) bool { return item.ID == input.cardID })
		if index < 0 {
			return errMissing
		}
		item := &next.Cards[index]
		if snapshotIdentityExists(next, input.id) {
			return errIdentityConflict
		}
		if !matchesStrongTag(input.expected, item.entityTag()) {
			return errStale
		}
		for _, current := range s.boards {
			if slices.ContainsFunc(current.Attachments, func(existing attachment) bool { return existing.ID == input.id }) {
				return errIdentityConflict
			}
		}
		now := maxCardTime(canonicalTime(time.Now()), next.Board.UpdatedAt)
		revision := next.Board.Revision + 1
		value.ID, value.Revision = input.id, revision
		value.CreatedAt, value.UpdatedAt = now, now
		var err error
		published, err = publishAttachment(s.root, value, input.data)
		if err != nil {
			return err
		}
		item.AttachmentCount++
		item.UpdatedAt = now
		item.Revision = revision
		next.Attachments = append(next.Attachments, value)
		after := attachmentAuditValue(value)
		next.Activity = append(next.Activity, activity{ID: newOperationID(), BoardID: boardID, Revision: revision,
			At: now, ActorID: actor, Operation: "attach", ResourceType: "attachment", ResourceID: value.ID, After: &after})
		return nil
	})
	if err != nil {
		definitelyUncommitted = !errors.Is(err, errWriteUncertain)
		return attachment{}, "", err
	}
	return value, updated.Board.entityTag(), nil
}

func attachmentAuditValue(item attachment) string {
	// Validated metadata contains only scalar values and canonical timestamps.
	data, _ := marshalCanonical(item)
	return string(data)
}

// Attachment filenames derive only from canonical UUIDs. The card UUID is a
// stable storage anchor even when its visible ticket number changes.
func attachmentPath(item attachment) string {
	return filepath.Join("attachments", item.BoardID, item.CardID, item.ID)
}

type publishedAttachment struct {
	dir  *os.Root
	name string
	info os.FileInfo
}

func (file *publishedAttachment) discard() {
	if named, err := file.dir.Lstat(file.name); err == nil && file.info != nil && os.SameFile(file.info, named) {
		_ = file.dir.Remove(file.name)
	}
}

func publishAttachment(root string, item attachment, data []byte) (*publishedAttachment, error) {
	parent, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	for _, name := range []string{"attachments", item.BoardID, item.CardID} {
		child, err := openAttachmentDirectory(parent, name)
		if err != nil {
			return nil, err
		}
		defer child.Close()
		if err := syncAttachmentDirectory(parent); err != nil {
			return nil, err
		}
		parent = child
	}
	// Keep a separate root open through snapshot publication so cleanup can
	// remove only this file even if the directory name is replaced.
	dir, err := parent.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	file, err := dir.OpenFile(item.ID, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, errors.Join(errIdentityConflict, dir.Close())
	}
	if err != nil {
		return nil, errors.Join(err, dir.Close())
	}
	info, err := file.Stat()
	published := &publishedAttachment{dir: dir, name: item.ID, info: info}
	if err != nil {
		return published, errors.Join(err, file.Close())
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return published, err
	}
	if err := syncAttachmentDirectory(parent); err != nil {
		return published, err
	}
	named, err := os.Lstat(filepath.Join(root, attachmentPath(item)))
	if err != nil || !named.Mode().IsRegular() || !os.SameFile(info, named) {
		return published, errors.New("attachment path changed during publication")
	}
	return published, nil
}

func openAttachmentDirectory(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return inspectAttachmentDirectory(parent, name)
}

func inspectAttachmentDirectory(parent *os.Root, name string) (*os.Root, error) {
	named, err := parent.Lstat(name)
	if err != nil || !named.IsDir() {
		return nil, errors.New("attachment storage requires a directory")
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(named, opened) {
		return nil, errors.Join(errors.New("attachment directory changed during open"), child.Close())
	}
	named, err = parent.Lstat(name)
	if err != nil || !named.IsDir() || !os.SameFile(named, opened) {
		return nil, errors.Join(errors.New("attachment directory changed during open"), child.Close())
	}
	return child, nil
}

func syncAttachmentDirectory(dir *os.Root) error {
	if runtime.GOOS == "windows" {
		// Windows cannot flush a directory handle with os.File.Sync.
		return nil
	}
	file, err := dir.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
