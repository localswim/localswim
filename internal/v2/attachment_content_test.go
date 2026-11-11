package v2

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestM24VerifiedAttachmentContent(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", "{\"name\":\"Verified bytes\"}", "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	data := []byte("\x89PNG\r\n\x1a\n")
	raw, _ = attachmentHTTPUpload(t, editor, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
	}, "../unsafe\\café.png", data, 201)
	raster := turnOneDecode[attachment](t, raw)
	path := "/attachments/" + strings.ToUpper(raster.ID) + "/content"

	unauthorized := editor
	unauthorized.token = ""
	attachmentHTTPContent(t, unauthorized, "GET", path, "", "", 401)
	attachmentHTTPContent(t, editor, "GET", "/attachments/"+newID()+"/content", "", "", 404)
	attachmentHTTPContent(t, editor, "GET", path+"?disposition=", "", "", 400)
	attachmentHTTPContent(t, editor, "GET", path+"?disposition=inline&disposition=attachment", "", "", 400)
	attachmentHTTPContent(t, editor, "GET", path+"?disposition=bogus", "", "", 400)
	attachmentHTTPContent(t, editor, "GET", path, "image/*;q=0", "", 406)
	attachmentHTTPContent(t, editor, "GET", path, "", "bad tag", 400)

	body, headers := attachmentHTTPContent(t, editor, "GET", path+"?disposition=InLiNe", "image/png", "", 200)
	disposition := headers.Get("Content-Disposition")
	kind, params, err := mime.ParseMediaType(disposition)
	if !bytes.Equal(body, data) || headers.Get("Content-Type") != "image/png" ||
		headers.Get("Content-Length") != strconv.Itoa(len(data)) || headers.Get("ETag") != raster.entityTag() ||
		headers.Get("Cache-Control") != "private, no-cache" || headers.Get("X-Content-Type-Options") != "nosniff" ||
		err != nil || kind != "inline" || params["filename"] != "_unsafe_café.png" ||
		!strings.Contains(disposition, "filename=_unsafe_caf_.png; filename*=UTF-8''_unsafe_caf%C3%A9.png") {
		t.Fatalf("unexpected raster response: headers %v, bytes %x, filename %q", headers, body, params["filename"])
	}

	body, headers = attachmentHTTPContent(t, editor, "HEAD", path, "", "", 200)
	if len(body) != 0 || headers.Get("Content-Length") != strconv.Itoa(len(data)) ||
		!strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") {
		t.Fatal("HEAD did not preserve the headers for the default download representation")
	}
	body, headers = attachmentHTTPContent(t, editor, "GET", path, "", "W/"+raster.entityTag(), 304)
	if len(body) != 0 || headers.Get("ETag") != raster.entityTag() {
		t.Fatal("conditional read did not return the verified attachment tag")
	}

	raw, _ = editor.call("GET", "/cards/"+c.ID, "", "", 200)
	current := turnOneDecode[card](t, raw)
	data = []byte("<!doctype html><script>alert('synthetic')</script>")
	raw, _ = attachmentHTTPUpload(t, editor, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", current.entityTag()},
	}, "document.html", data, 201)
	generic := turnOneDecode[attachment](t, raw)
	body, headers = attachmentHTTPContent(t, editor, "GET", "/attachments/"+generic.ID+"/content?disposition=inline", "application/octet-stream", "", 200)
	if !bytes.Equal(body, data) || headers.Get("Content-Type") != "application/octet-stream" ||
		!strings.HasPrefix(headers.Get("Content-Disposition"), "attachment;") ||
		!strings.Contains(headers.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal("generic bytes escaped attachment-only handling")
	}

	// The content reader must check even an otherwise matching conditional request.
	file := filepath.Join(app.store.root, attachmentPath(raster))
	if err := os.WriteFile(file, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	attachmentHTTPContent(t, editor, "GET", path, "", raster.entityTag(), 503)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	attachmentHTTPContent(t, editor, "HEAD", path, "", "", 503)

	// Seed retained metadata in the disposable service without exposing a detach route.
	app.store.mu.Lock()
	value := app.store.boards[b.ID]
	value.Attachments = append([]attachment(nil), value.Attachments...)
	value.Attachments[0].Active = false
	app.store.boards[b.ID] = value
	app.store.mu.Unlock()
	attachmentHTTPContent(t, editor, "GET", path, "", "", 404)
}

func attachmentHTTPContent(t *testing.T, client turnOneClient, method, path, accept, conditional string, status int) ([]byte, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, client.url+Prefix+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.token != "" {
		req.Header.Set("Authorization", "Bearer "+client.token)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if conditional != "" {
		req.Header.Set("If-None-Match", conditional)
	}
	response, err := client.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s returned %d, want %d: %s", method, path, response.StatusCode, status, body)
	}
	return body, response.Header
}

func TestAttachmentDispositionBounds(t *testing.T) {
	name := "\r\n../unsafe\\" + strings.Repeat("🧪", 1000)
	header := attachmentDisposition("attachment", name)
	kind, params, err := mime.ParseMediaType(header)
	filename := params["filename"]
	if err != nil || kind != "attachment" || len(header) > 2048 ||
		strings.ContainsAny(header, "\r\n") || strings.ContainsAny(filename, "/\\\r\n") ||
		!strings.Contains(filename, "🧪") {
		t.Fatal("long UTF-8 filename produced an unsafe or invalid disposition")
	}
}

func TestAttachmentCoverEligibility(t *testing.T) {
	id := newID()
	cardID := newID()
	value := snapshot{Attachments: []attachment{{
		ID: id, CardID: cardID, Active: true, Previewable: true,
	}}}
	if err := validateAttachmentCover(&value, cardID, nil); err != nil {
		t.Fatal(err)
	}
	if err := validateAttachmentCover(&value, cardID, &id); err != nil {
		t.Fatal(err)
	}
	var issue invalidField
	if err := validateAttachmentCover(&value, newID(), &id); !errors.As(err, &issue) || issue.Field != "cover_attachment_id" {
		t.Fatal("cover from a different card was accepted")
	}
	value.Attachments[0].Active = false
	if err := validateAttachmentCover(&value, cardID, &id); err == nil {
		t.Fatal("inactive cover was accepted")
	}
	value.Attachments[0].Active = true
	value.Attachments[0].Previewable = false
	if err := validateAttachmentCover(&value, cardID, &id); err == nil {
		t.Fatal("generic cover was accepted")
	}
}

func TestAttachmentDispositionSanitizesDeviceNamesAndFormatControls(t *testing.T) {
	for _, tc := range []struct{ original, want string }{
		{"CON", "_CON"},
		{"PRN .txt", "_PRN .txt"},
		{"lpt\u00b2.txt", "_lpt\u00b2.txt"},
		{"CONOUT$.log", "_CONOUT$.log"},
		{"report\u202egnp.\u200dpng", "report_gnp._png"},
		{"COM10.log", "COM10.log"},
	} {
		header := attachmentDisposition("attachment", tc.original)
		_, params, err := mime.ParseMediaType(header)
		if err != nil || params["filename"] != tc.want {
			t.Errorf("filename %q was sanitized to %q, want %q (%v)", tc.original, params["filename"], tc.want, err)
		}
	}
	// Either rendered name can be shortened before the decisive part of its stem.
	header := attachmentDisposition("attachment", "CON"+strings.Repeat(" ", 1000)+"suffix.txt")
	fallback, _, _ := strings.Cut(header, "; filename*=")
	_, ascii, asciiErr := mime.ParseMediaType(fallback)
	_, utf8, utf8Err := mime.ParseMediaType(header)
	if asciiErr != nil || utf8Err != nil || !strings.HasPrefix(ascii["filename"], "_CON") ||
		!strings.HasPrefix(utf8["filename"], "_CON") {
		t.Fatal("filename truncation exposed a Windows device name")
	}
}
