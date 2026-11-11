package v2

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertAttachmentRevision(t *testing.T, raw []byte, want uint64, tag string) attachment {
	t.Helper()
	fields := turnOneDecode[map[string]json.RawMessage](t, raw)
	for _, obsolete := range []string{"etag", "sha256"} {
		if _, present := fields[obsolete]; present {
			t.Fatalf("attachment JSON contains obsolete %s", obsolete)
		}
	}
	if string(fields["revision"]) != fmt.Sprint(want) {
		t.Fatalf("attachment revision = %s, want numeric %d", fields["revision"], want)
	}
	item := turnOneDecode[attachment](t, raw)
	if string(fields["sha2_256"]) != fmt.Sprintf("%q", item.SHA256) {
		t.Fatal("attachment SHA-2-256 member differs from digest")
	}
	if tag != "" && tag != item.entityTag() {
		t.Fatal("attachment HTTP tag does not match its revision")
	}
	return item
}

func TestAttachmentRevisionRepresentationAndRestart(t *testing.T) {
	app, client := attachmentHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Attachment revisions"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, client, b)
	// The attachment's initial revision must follow unrelated board commits too.
	client.call("PATCH", "/boards/"+b.ID, `{"name":"Before upload"}`, etag(b.ID, 2), 200)
	fields := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", c.entityTag()}}
	raw, headers := attachmentHTTPUpload(t, client, fields, "revision.bin", []byte("revision"), 201)
	item := assertAttachmentRevision(t, raw, 4, headers.Get("ETag"))
	path := "/attachments/" + item.ID
	client.call("PATCH", "/boards/"+b.ID, `{"name":"After upload"}`, headers.Get("X-Board-ETag"), 200)
	client.call("PATCH", "/cards/"+c.ID, `{"title":"Unrelated card edit"}`, etag(c.ID, 4), 200)
	raw, headers = client.call("GET", path, "", "", 200)
	assertAttachmentRevision(t, raw, 4, headers.Get("ETag"))
	raw, _ = client.call("GET", "/attachments?card_id="+c.ID, "", "", 200)
	page := turnOnePage[json.RawMessage](t, raw, "attachments")
	if len(page.Values) != 1 {
		t.Fatal("attachment list membership differs")
	}
	assertAttachmentRevision(t, page.Values[0], 4, "")
	state, _ := app.store.accepted(b.ID)
	data := marshalVerificationFixture(t, state)
	native := turnOneDecode[struct {
		Attachments []json.RawMessage `json:"attachments"`
	}](t, data)
	original := native.Attachments[0]
	assertAttachmentRevision(t, original, 4, "")
	for _, replacement := range []string{`"revision":0`, `"revision":1`, `"revision":3`, `"revision":5`, `"revision":7`, `"revision":9007199254740992`, `"revision":-1`, `"revision":1.5`, `"revision":"4"`, `"revision":null`, `"revision":true`, `"revision":4e0`, `"etag":"obsolete"`, `"revision":4,"etag":"obsolete"`} {
		t.Run(replacement, func(t *testing.T) {
			changed := strings.Replace(string(original), `"revision":4`, replacement, 1)
			damaged := strings.Replace(string(data), string(original), changed, 1)
			if damaged == string(data) {
				t.Fatal("native attachment was not replaced")
			}
			if _, err := parseSnapshot([]byte(damaged), b.ID); err == nil {
				t.Fatal("accepted invalid or obsolete attachment revision")
			}
		})
	}
	for _, replacement := range []string{`"sha256":` + fmt.Sprintf("%q", item.SHA256), `"sha2_256":"bad"`, `"sha2_256":` + fmt.Sprintf("%q", strings.Repeat("0", 64))} {
		t.Run(replacement, func(t *testing.T) {
			changed := strings.Replace(string(original), `"sha2_256":`+fmt.Sprintf("%q", item.SHA256), replacement, 1)
			damaged := strings.Replace(string(data), string(original), changed, 1)
			if damaged == string(data) {
				t.Fatal("native digest was not replaced")
			}
			if _, err := parseSnapshot([]byte(damaged), b.ID); err == nil {
				t.Fatal("accepted obsolete or tampered attachment digest")
			}
		})
	}
	audit := state.Activity[3]
	if audit.After == nil || *audit.After != attachmentAuditValue(item) {
		t.Fatal("upload audit does not preserve exact attachment metadata")
	}
	wrongRevision := strings.Replace(*audit.After, `"revision":4`, `"revision":5`, 1)
	obsoleteDigest := strings.Replace(*audit.After, `"sha2_256"`, `"sha256"`, 1)
	for _, replacement := range []*string{nil, &wrongRevision, &obsoleteDigest} {
		changed := state
		changed.Activity = append([]activity(nil), state.Activity...)
		changed.Activity[3].After = replacement
		if _, err := parseSnapshot(marshalVerificationFixture(t, changed), b.ID); err == nil {
			t.Fatal("accepted obsolete or tampered attachment audit")
		}
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	botToken := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	reopened, err := Open(app.store.root, client.token, botToken)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewUnstartedServer(nil)
	server.Config.Handler = reopened.Handler(server.Listener.Addr().(*net.TCPAddr))
	server.Start()
	t.Cleanup(server.Close)
	client = turnOneClient{t, server.URL, client.token, server.Client()}
	raw, headers = client.call("GET", path, "", "", 200)
	assertAttachmentRevision(t, raw, 4, headers.Get("ETag"))
	state, _ = reopened.store.accepted(b.ID)
	state.Attachments[0].Revision++
	accepted, _ := reopened.store.accepted(b.ID)
	if accepted.Attachments[0].Revision != 4 {
		t.Fatal("caller changed accepted attachment revision")
	}
}

func TestAttachmentRevisionIsServiceManaged(t *testing.T) {
	_, client := attachmentHTTPServer(t)
	raw, _ := client.call("POST", "/boards", `{"name":"Protected attachment revision"}`, "", 201)
	c := attachmentHTTPCard(t, client, turnOneDecode[board](t, raw))
	fields := []attachmentFormField{{"card_id", c.ID}, {"expected_card_etag", c.entityTag()}}
	for _, name := range []string{"revision", "etag"} {
		attachmentHTTPUpload(t, client, append(fields, attachmentFormField{name, "9"}), "file.bin", nil, 400)
	}
}
