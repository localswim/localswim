package v2

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestM25DetachAttachment(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	raw, _ := editor.call("POST", "/boards", `{"name":"Detach attachment"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	data := []byte("\x89PNG\r\n\x1a\n")
	raw, _ = attachmentHTTPUpload(t, editor, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
	}, "cover.png", data, 201)
	cover := turnOneDecode[attachment](t, raw)
	cardPath := "/cards/" + c.ID
	raw, _ = editor.call("GET", cardPath, "", "", 200)
	c = turnOneDecode[card](t, raw)
	raw, headers := editor.call("PATCH", cardPath, `{"cover_attachment_id":"`+cover.ID+`"}`, c.entityTag(), 200)
	c = turnOneDecode[card](t, raw)
	boardTag := headers.Get("X-Board-ETag")
	path := "/attachments/" + strings.ToUpper(cover.ID)

	attachmentHTTPDetach(t, editor, path, "", boardTag, 428)
	attachmentHTTPDetach(t, editor, path, cover.entityTag(), "", 428)
	attachmentHTTPDetach(t, editor, path, cover.entityTag(), "*", 400)
	attachmentHTTPDetach(t, editor, path, cover.entityTag(), b.ETag, 412)
	attachmentHTTPDetach(t, editor, path, "W/"+cover.entityTag(), boardTag, 412)

	headers = attachmentHTTPDetach(t, editor, path, cover.entityTag(), boardTag, 204)
	newBoardTag := etag(b.ID, c.Revision+1)
	if headers.Get("Cache-Control") != "no-store" || headers.Get("X-Board-ETag") != newBoardTag {
		t.Fatalf("unexpected detach headers: %v", headers)
	}
	raw, headers = editor.call("GET", path, "", "", 200)
	detached := turnOneDecode[attachment](t, raw)
	if detached.Active || detached.ID != cover.ID || detached.CardID != c.ID ||
		detached.SHA256 != cover.SHA256 || detached.AddedBy != cover.AddedBy ||
		detached.OriginalName != cover.OriginalName || detached.Revision != c.Revision+1 || headers.Get("ETag") != detached.entityTag() {
		t.Fatalf("unexpected detached attachment: %+v", detached)
	}
	attachmentHTTPContent(t, editor, "GET", path+"/content", "", cover.entityTag(), 404)
	attachmentHTTPContent(t, editor, "HEAD", path+"/content", "", "", 404)
	attachmentHTTPDetach(t, editor, path, detached.entityTag(), newBoardTag, 404)
	raw, _ = editor.call("GET", cardPath, "", "", 200)
	updated := turnOneDecode[card](t, raw)
	if updated.CoverAttachmentID != nil || updated.AttachmentCount != 0 || updated.Revision != c.Revision+1 {
		t.Fatalf("unexpected card state after detach: %+v", updated)
	}
	for _, active := range []string{"true", "false"} {
		raw, _ = editor.call("GET", "/attachments?card_id="+c.ID+"&active="+active, "", "", 200)
		if got, want := strings.Contains(string(raw), cover.ID), active == "false"; got != want {
			t.Fatalf("unexpected active=%s attachment list: %s", active, raw)
		}
	}
	state, err := app.store.readback(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Board.ETag != newBoardTag || len(state.Activity) < 2 {
		t.Fatal("durable readback did not include the detach revision")
	}
	clear, detach := state.Activity[len(state.Activity)-2], state.Activity[len(state.Activity)-1]
	if clear.ResourceType != "card" || clear.ResourceID != c.ID || clear.Revision != updated.Revision ||
		detach.Operation != "detach" || detach.ResourceType != "attachment" || detach.ResourceID != cover.ID ||
		detach.Revision != updated.Revision || detach.ActorID != cover.AddedBy {
		t.Fatal("detach and cover clear were not audited together")
	}
	if kept, err := readVerifiedAttachment(app.store.root, detached); err != nil || !bytes.Equal(kept, data) {
		t.Fatal("detachment did not retain the verified bytes")
	}
	altered := cloneSnapshot(state)
	altered.Attachments[0].SHA256 = strings.Repeat("0", 64)
	if err := validateSnapshot(altered, b.ID); err == nil {
		t.Fatal("inactive digest substitution was admitted")
	}
	altered = cloneSnapshot(state)
	field, before, after := "name", state.Board.Name, "Injected same-revision edit"
	altered.Board.Name = after
	altered.Activity = append(altered.Activity, activity{
		ID: newOperationID(), BoardID: b.ID, Revision: detach.Revision, At: detach.At,
		ActorID: detach.ActorID, Operation: "patch", ResourceType: "board", ResourceID: b.ID,
		Field: &field, Before: &before, After: &after,
	})
	if err := validateSnapshot(altered, b.ID); err == nil {
		t.Fatal("detach audit admitted an unrelated change in the same revision")
	}
}

func attachmentHTTPDetach(t *testing.T, client turnOneClient, path, tag, boardTag string, status int) http.Header {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodDelete, client.url+Prefix+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+client.token)
	if tag != "" {
		req.Header.Set("If-Match", tag)
	}
	if boardTag != "" {
		req.Header.Set("X-Expected-Board-ETag", boardTag)
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
	if response.StatusCode != status || status == 204 && len(body) != 0 {
		t.Fatalf("DELETE returned %d, want %d: %s", response.StatusCode, status, body)
	}
	return response.Header
}

func TestM25DetachUsesMutationGrantWithoutRead(t *testing.T) {
	app, human := attachmentHTTPServer(t)
	id, now := newID(), canonicalTime(time.Now())
	wf := defaultWorkflow(id, now, app.store.principals)
	for i := range wf.Policy.ResourceGrants {
		grant := &wf.Policy.ResourceGrants[i]
		if grant.UserID == botID {
			grant.Capabilities = slices.DeleteFunc(grant.Capabilities, func(capability string) bool {
				return capability == "read"
			})
		}
	}
	state := freshSnapshot(id, "Independent detach grant", wf, humanID, now)
	if err := app.store.create(state); err != nil {
		t.Fatal(err)
	}
	c := attachmentHTTPCard(t, human, state.Board)
	raw, headers := attachmentHTTPUpload(t, human, []attachmentFormField{
		{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
	}, "data.bin", []byte("synthetic"), 201)
	item := turnOneDecode[attachment](t, raw)
	bot := human
	bot.token = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	path := "/attachments/" + item.ID
	bot.call("GET", path, "", "", 404)
	attachmentHTTPDetach(t, bot, path, item.entityTag(), headers.Get("X-Board-ETag"), 204)
	raw, _ = human.call("GET", path, "", "", 200)
	if turnOneDecode[attachment](t, raw).Active {
		t.Fatal("member with the attachment mutation grant could not detach")
	}
}
