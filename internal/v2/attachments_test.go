package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type attachmentFormField struct{ name, value string }

func attachmentHTTPServer(t *testing.T) (*Service, turnOneClient) {
	t.Helper()
	human := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))
	bot := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	app, err := Open(t.TempDir(), human, bot)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = app.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(func() {
		server.Close()
		if err := app.Close(); err != nil {
			t.Error(err)
		}
	})
	return app, turnOneClient{t, server.URL, human, &http.Client{Timeout: 15 * time.Second}}
}

func attachmentHTTPCard(t *testing.T, editor turnOneClient, b board) card {
	t.Helper()
	create := fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q,"lane_id":%q,"title":"Synthetic"}`, b.ID, b.entityTag(), b.LaneIDs[0])
	raw, _ := editor.call("POST", "/cards", create, "", 201)
	return turnOneDecode[card](t, raw)
}

func attachmentHTTPUpload(t *testing.T, c turnOneClient, fields []attachmentFormField, name string, data []byte, status int) ([]byte, http.Header) {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := form.WriteField(field.name, field.value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := form.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest("POST", c.url+Prefix+"/attachments", &body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("POST /attachments status %d, want %d: %s", response.StatusCode, status, raw)
	}
	return raw, response.Header
}

func TestM23UploadAndListAttachments(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Attachments"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	id := newID()
	fields := []attachmentFormField{{"card_id", strings.ToUpper(c.ID)}, {"expected_card_etag", c.entityTag()}, {"id", strings.ToUpper(id)}}
	name := `../original\case 🧪.png`
	data := make([]byte, maxAttachmentBytes+1)
	copy(data, "\x89PNG\r\n\x1a\n")
	attachmentHTTPUpload(t, editor, fields, name, data, 413)
	attachmentHTTPUpload(t, editor, fields[:1], name, data[:8], 428)
	attachmentHTTPUpload(t, editor, append(fields, attachmentFormField{"Actor_ID", botID}), name, data[:8], 400)
	attachmentHTTPUpload(t, editor, append(fields, attachmentFormField{"card_id", c.ID}), name, data[:8], 400)
	stale := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", etag(c.ID, 99)}}
	attachmentHTTPUpload(t, editor, stale, name, data[:8], 412)
	weak := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", "W/" + c.entityTag()}}
	attachmentHTTPUpload(t, editor, weak, name, data[:8], 400)
	editor.call("POST", "/attachments", `{}`, "", 415)
	query := "/attachments?card_id=" + c.ID
	raw, _ = editor.call("GET", query, "", "", 200)
	if page := turnOnePage[attachment](t, raw, "attachments"); len(page.Values) != 0 || page.NextCursor != nil {
		t.Fatal("rejected uploads created metadata")
	}

	fields = append(fields, attachmentFormField{"ignored_field", "ignored"})
	data = data[:maxAttachmentBytes]
	raw, headers := attachmentHTTPUpload(t, editor, fields, name, data, 201)
	item := turnOneDecode[attachment](t, raw)
	if item.ID != id || item.BoardID != b.ID || item.CardID != c.ID || item.OriginalName != name ||
		item.AddedBy != humanID || !item.Active || !item.Previewable || item.MediaType != "image/png" ||
		item.Size != maxAttachmentBytes || item.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("upload metadata differs from supplied bytes, identity, or actor: %+v", item)
	}
	if headers.Get("Location") != Prefix+"/attachments/"+id || headers.Get("ETag") != item.entityTag() || headers.Get("X-Board-ETag") != etag(b.ID, 3) || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("incorrect upload headers: %v", headers)
	}
	stored, err := os.ReadFile(filepath.Join(app.store.root, attachmentPath(item)))
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("stored attachment bytes differ from upload", err)
	}
	raw, _ = editor.call("GET", "/cards/"+c.ID, "", "", 200)
	updated := turnOneDecode[card](t, raw)
	if updated.AttachmentCount != 1 || updated.Revision != 3 || updated.entityTag() == c.entityTag() || updated.UpdatedAt != item.CreatedAt {
		t.Fatal("attachment transaction did not update the owning card")
	}
	saved, err := app.store.durable(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	audit := saved.Activity[len(saved.Activity)-1]
	if saved.Board.Revision != 3 || len(saved.Attachments) != 1 || len(saved.Activity) != 3 ||
		audit.Operation != "attach" || audit.ResourceType != "attachment" || audit.ResourceID != item.ID ||
		audit.ActorID != item.AddedBy || audit.Revision != 3 || !audit.At.Equal(item.CreatedAt) ||
		saved.Board.LastCardEditAt == nil || !saved.Board.LastCardEditAt.Equal(item.CreatedAt) {
		t.Fatal("attachment and its attributed audit were not saved in the same revision")
	}

	raw, _ = editor.call("GET", "/attachments/"+strings.ToUpper(id), "", "", 200)
	if read := turnOneDecode[attachment](t, raw); read != item {
		t.Fatal("focused read differs from the upload response")
	}
	raw, _ = editor.call("GET", query+"&active=false", "", "", 200)
	if len(turnOnePage[attachment](t, raw, "attachments").Values) != 0 {
		t.Fatal("active item appeared in an inactive-only list")
	}
	editor.call("GET", query+"&active=1", "", "", 400)
	newFields := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", updated.entityTag()}}
	attachmentHTTPUpload(t, editor, append(newFields, attachmentFormField{"id", id}), "duplicate.bin", nil, 409)
	attachmentHTTPUpload(t, editor, append(newFields, attachmentFormField{"id", c.ID}), "duplicate.bin", nil, 409)
	raw, _ = attachmentHTTPUpload(t, editor, newFields, "empty.bin", nil, 201)
	empty := turnOneDecode[attachment](t, raw)
	if empty.Previewable || empty.Size != 0 || empty.ID == id {
		t.Fatal("empty generic attachment did not get its own metadata")
	}
	raw, _ = editor.call("GET", query+"&limit=1", "", "", 200)
	page := turnOnePage[attachment](t, raw, "attachments")
	if len(page.Values) != 1 || page.Values[0] != item || page.NextCursor == nil {
		t.Fatal("first attachment page did not return the original file and a continuation")
	}
	raw, _ = editor.call("GET", query+"&limit=1&cursor="+url.QueryEscape(*page.NextCursor), "", "", 200)
	if page := turnOnePage[attachment](t, raw, "attachments"); len(page.Values) != 1 || page.Values[0] != empty || page.NextCursor != nil {
		t.Fatal("attachment continuation did not return the second file")
	}
	request, err := http.NewRequest("HEAD", editor.url+Prefix+"/attachments/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+editor.token)
	request.Header.Set("If-None-Match", item.entityTag())
	response, err := editor.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 304 || response.Header.Get("ETag") != item.entityTag() {
		t.Fatal("attachment HEAD did not return the conditional representation tag")
	}
}

func TestAttachmentUploadPreservesExtendedMultipartFilename(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Browser multipart"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	card := attachmentHTTPCard(t, editor, b)
	current, ok := app.store.accepted(b.ID)
	if !ok {
		t.Fatal("board snapshot missing")
	}

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	id := newID()
	for _, field := range []attachmentFormField{
		{"id", id},
		{"card_id", card.ID},
		{"expected_card_etag", card.entityTag()},
	} {
		if err := form.WriteField(field.name, field.value); err != nil {
			t.Fatal(err)
		}
	}
	const filename = "clip e\u0301\"\\\r\n\x00.png"
	header := textproto.MIMEHeader{
		"Content-Disposition": {"form-data; name=\"file\"; filename*=UTF-8''clip%20e%CC%81%22%5C%0D%0A%00.png"},
		"Content-Type":        {"application/octet-stream"},
	}
	part, err := form.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, editor.url+Prefix+"/attachments", &body)
	if err != nil {
		t.Fatal(err)
	}
	requestID := newID()
	req.Header.Set("X-Request-ID", requestID)
	req.Header.Set("X-Expected-Board-ETag", current.Board.entityTag())
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+editor.token)
	resp, err := editor.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Request-ID") != requestID {
		t.Fatalf("upload status %d: %s", resp.StatusCode, raw)
	}
	item := turnOneDecode[attachment](t, raw)
	if item.ID != id || item.OriginalName != filename || item.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
		t.Fatalf("upload identity differs: %+v", item)
	}
	saved, _ := editor.call("GET", "/attachments/"+id, "", "", 200)
	if metadata := turnOneDecode[attachment](t, saved); metadata.OriginalName != filename {
		t.Fatalf("stored filename differs: %q", metadata.OriginalName)
	}
	content, _ := attachmentHTTPContent(t, editor, http.MethodGet, "/attachments/"+id+"/content", "", "", 200)
	if !bytes.Equal(content, data) {
		t.Fatal("download differs from original bytes")
	}
}

func TestM23AttachmentReadbackAfterUncertainOutcome(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	unrelatedRaw, _ := editor.call("POST", "/boards", `{"id":"00000000-0000-4000-8000-000000000001","name":"Unrelated"}`, "", 201)
	unrelated := turnOneDecode[board](t, unrelatedRaw)
	raw, _ := editor.call("POST", "/boards", `{"id":"00000000-0000-4000-8000-000000000002","name":"Recovery"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	previous, _ := app.store.accepted(b.ID)
	id := newID()
	fields := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", c.entityTag()}, {"id", id}}
	attachmentHTTPUpload(t, editor, fields, "recover.bin", []byte("recover"), 201)
	// Model a lost success response and a commit that reached disk before the
	// store could acknowledge it. Retain the pre-write accepted state.
	app.store.mu.Lock()
	app.store.uncertainBoards = map[string]snapshot{b.ID: app.store.boards[b.ID]}
	app.store.boards[b.ID] = previous
	app.store.mu.Unlock()
	if err := os.WriteFile(app.store.snapshotPath(unrelated.ID), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	raw, _ = editor.call("GET", "/attachments/"+id, "", "", 200)
	item := turnOneDecode[attachment](t, raw)
	raw, _ = editor.call("GET", "/attachments?card_id="+c.ID, "", "", 200)
	if page := turnOnePage[attachment](t, raw, "attachments"); len(page.Values) != 1 || page.Values[0] != item || item.ID != id {
		t.Fatal("durable readback did not expose the committed upload")
	}
	if current, _ := app.store.accepted(b.ID); len(current.Attachments) != 0 || current.Board.Revision != previous.Board.Revision {
		t.Fatal("readback changed accepted memory before restart")
	}
	path := filepath.Join(app.store.root, attachmentPath(item))
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "recover" {
		t.Fatal("readback did not preserve committed file bytes", err)
	}
	if err := os.WriteFile(app.store.snapshotPath(b.ID), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	editor.call("GET", "/attachments/"+id, "", "", 503)
	editor.call("GET", "/attachments?card_id="+c.ID, "", "", 503)
}

func TestM23AttachmentCommitRefusalAndAuthorization(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	id, now := newID(), canonicalTime(time.Now())
	wf := defaultWorkflow(id, now, app.principals)
	for index := range wf.Policy.ResourceGrants {
		grant := &wf.Policy.ResourceGrants[index]
		if grant.UserID == botID {
			grant.Capabilities = []string{"read"}
		}
	}
	value := freshSnapshot(id, "Attachment permissions", wf, humanID, now)
	if err := app.store.create(value); err != nil {
		t.Fatal(err)
	}
	c := attachmentHTTPCard(t, editor, value.Board)
	id = newID()
	fields := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", c.entityTag()}, {"id", id}}
	bot := editor
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	attachmentHTTPUpload(t, bot, fields, "test.bin", []byte("content"), 403)
	path := app.store.snapshotPath(value.Board.ID)
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	attachmentHTTPUpload(t, editor, fields, "test.bin", []byte("content"), 503)
	file := filepath.Join(app.store.root, attachmentPath(attachment{ID: id, BoardID: c.BoardID, CardID: c.ID}))
	if _, err := os.Stat(filepath.Dir(file)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refused snapshot admission staged attachment data", err)
	}
	if err := os.Rename(path+".saved", path); err != nil {
		t.Fatal(err)
	}
	attachmentHTTPUpload(t, editor, fields, "test.bin", []byte("content"), 201)
	bot.call("GET", "/attachments/"+id, "", "", 200)
}
