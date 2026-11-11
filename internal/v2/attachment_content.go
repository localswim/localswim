package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errAttachmentIntegrity = errors.New("attachment bytes failed integrity verification")

func validateAttachmentCover(state *snapshot, cardID string, coverID *string) error {
	if coverID == nil || slices.ContainsFunc(state.Attachments, func(item attachment) bool {
		return item.ID == *coverID && item.CardID == cardID && item.Active && item.Previewable
	}) {
		return nil
	}
	return invalid("cover_attachment_id", "Select an active raster attachment on this card.")
}

func (x *request) attachmentContent(id string) {
	disposition, err := singleQuery(x.r, "disposition")
	if err != nil {
		x.fail(err)
		return
	}
	switch {
	case strings.EqualFold(disposition, "inline"):
		disposition = "inline"
	case strings.EqualFold(disposition, "attachment"), disposition == "" && !x.r.URL.Query().Has("disposition"):
		disposition = "attachment"
	default:
		x.fail(invalid("disposition", "Expected inline or attachment."))
		return
	}
	var conditions []conditionTag
	if len(x.r.Header.Values("If-None-Match")) != 0 {
		var ok bool
		if conditions, ok = parseTagList(x.r.Header.Get("If-None-Match")); !ok {
			x.fail(invalid("If-None-Match", "Expected an entity tag list."))
			return
		}
	}
	item, err := x.findAttachment(id)
	if err != nil {
		x.fail(err)
		return
	}
	if !item.Active {
		x.fail(errMissing)
		return
	}

	mediaType := "application/octet-stream"
	if item.Previewable {
		mediaType = item.MediaType
	} else {
		disposition = "attachment"
	}
	major, subtype, _ := strings.Cut(mediaType, "/")
	if !acceptsMedia(x.r.Header.Values("Accept"), major, subtype) {
		x.problem(406, "not_acceptable", "The attachment content type is not accepted.", "", nil)
		return
	}
	data, err := readVerifiedAttachment(x.service.store.root, item)
	if err != nil {
		x.problem(503, "unavailable", "Durable attachment content is unavailable.", "", nil)
		return
	}

	h := x.w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", item.entityTag())
	for _, candidate := range conditions {
		if candidate.opaque == "*" || candidate.opaque == item.entityTag() {
			x.w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h.Set("Content-Type", mediaType)
	h.Set("Content-Disposition", attachmentDisposition(disposition, item.OriginalName))
	h.Set("Content-Length", strconv.Itoa(len(data)))
	x.w.WriteHeader(http.StatusOK)
	if x.r.Method != http.MethodHead {
		_, _ = x.w.Write(data)
	}
}

// Read through checked directory handles and verify the exact bytes buffered for
// delivery. A matching filename or file size alone cannot prove integrity.
func readVerifiedAttachment(root string, item attachment) ([]byte, error) {
	parent, err := os.OpenRoot(root)
	if err != nil {
		return nil, errAttachmentIntegrity
	}
	defer parent.Close()
	for _, name := range []string{"attachments", item.BoardID, item.CardID} {
		child, err := inspectAttachmentDirectory(parent, name)
		if err != nil {
			return nil, errAttachmentIntegrity
		}
		defer child.Close()
		parent = child
	}
	named, err := parent.Lstat(item.ID)
	if err != nil || !named.Mode().IsRegular() || named.Size() != item.Size || item.Size > maxAttachmentBytes {
		return nil, errAttachmentIntegrity
	}
	// The platform helper uses a nonblocking open on Unix in case a FIFO
	// replaces the regular file between Lstat and Open.
	file, err := openBuildFile(parent, item.ID)
	if err != nil {
		return nil, errAttachmentIntegrity
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(named, opened) {
		return nil, errAttachmentIntegrity
	}
	data, err := io.ReadAll(io.LimitReader(file, maxAttachmentBytes+1))
	if err != nil || int64(len(data)) != item.Size {
		return nil, errAttachmentIntegrity
	}
	current, err := parent.Lstat(item.ID)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return nil, errAttachmentIntegrity
	}
	digest := sha256.Sum256(data)
	mediaType := http.DetectContentType(data)
	if hex.EncodeToString(digest[:]) != item.SHA256 || mediaType != item.MediaType ||
		previewableAttachment(mediaType) != item.Previewable {
		return nil, errAttachmentIntegrity
	}
	return data, nil
}

// Keep storage metadata exact. The suggested download name excludes path
// separators and header controls and fits the 2048-byte response header limit.
func attachmentDisposition(disposition, original string) string {
	name := strings.Trim(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || strings.ContainsRune("/\\<>:\"|?*", r) {
			return '_'
		}
		return r
	}, original), " .")
	if name == "" {
		name = "attachment"
	}
	var ascii, encoded strings.Builder
	nameEnd := 0
	for index, r := range name {
		attr := encodeFilenameRune(r)
		if encoded.Len()+len(attr) > 1720 {
			break
		}
		encoded.WriteString(attr)
		nameEnd = index + utf8.RuneLen(r)
		if ascii.Len() < 256 {
			if r < unicode.MaxASCII && r != '%' {
				ascii.WriteRune(r)
			} else {
				ascii.WriteByte('_')
			}
		}
	}
	asciiName := ascii.String()
	if reservedAttachmentName(asciiName) {
		asciiName = "_" + asciiName
	}
	encodedName := encoded.String()
	if reservedAttachmentName(name[:nameEnd]) {
		encodedName = "_" + encodedName
	}
	return mime.FormatMediaType(disposition, map[string]string{"filename": asciiName}) +
		"; filename*=UTF-8''" + encodedName
}

func reservedAttachmentName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.ToUpper(strings.TrimRightFunc(stem, unicode.IsSpace))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(stem) > 3 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		digit, size := utf8.DecodeRuneInString(stem[3:])
		return len(stem) == 3+size && strings.ContainsRune("123456789¹²³", digit)
	}
	return false
}

func encodeFilenameRune(r rune) string {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
		strings.ContainsRune("!#$&+-.^_|~", r) {
		return string(r)
	}
	const hexDigits = "0123456789ABCDEF"
	var encoded strings.Builder
	for _, b := range []byte(string(r)) {
		encoded.WriteByte('%')
		encoded.WriteByte(hexDigits[b>>4])
		encoded.WriteByte(hexDigits[b&15])
	}
	return encoded.String()
}
