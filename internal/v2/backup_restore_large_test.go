package v2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/localswim/localswim/internal/client"
)

func TestM86RestoreDownloadedLargeBackup(t *testing.T) {
	app, editor := attachmentHTTPServer(t)
	app.store.ownerNotices.close()
	app.store.ownerNotices = newOwnerNoticeWorker(ownerNoticeQuietPeriod, app.store.ownerNoticeSettings,
		func(context.Context, string, string) bool { return true })
	app.store.ownerNotices.writerID = app.store.activityWriterID
	raw, _ := editor.call("POST", "/boards", `{"name":"Large portable restore"}`, "", 201)
	b := turnOneDecode[board](t, raw)
	c := attachmentHTTPCard(t, editor, b)
	data := bytes.Repeat([]byte{'a'}, maxAttachmentBytes)
	var detached attachment
	for i := range 10 {
		raw, _ = editor.call("GET", "/cards/"+c.ID, "", "", 200)
		c = turnOneDecode[card](t, raw)
		raw, headers := attachmentHTTPUpload(t, editor, []attachmentFormField{
			{"card_id", c.ID}, {"expected_card_etag", c.entityTag()},
		}, fmt.Sprintf("fixture-%d.bin", i), data, 201)
		if i == 9 {
			detached = turnOneDecode[attachment](t, raw)
			attachmentHTTPDetach(t, editor, "/attachments/"+detached.ID,
				detached.entityTag(), headers.Get("X-Board-ETag"), 204)
		}
	}
	source, err := app.store.readback(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	routePath := "/notifications/" + b.ID
	_, headers := editor.call("GET", routePath, "", "", 200)
	_, headers = editor.call("PATCH", routePath,
		fmt.Sprintf(`{"enabled":true,"target_thread_id":%q}`, ownerNoticeTestTarget), headers.Get("ETag"), 200)
	raw, _ = editor.call("POST", "/backups", fmt.Sprintf(`{"board_id":%q,"expected_board_etag":%q}`,
		b.ID, source.Board.entityTag()), "", 201)
	item := turnOneDecode[backup](t, raw)
	api, err := client.New(editor.url, editor.token)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "download.zip")
	download, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(api.BackupContent("/backups/"+item.ID+"/content", uint64(item.Size), download), download.Close()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() <= maxSnapshotBytes || m96FileDigest(t, path) != item.SHA256 {
		t.Fatalf("expected an intact download above 96 MiB: %v", err)
	}
	// Change the independent routing after capture so restoration must preserve
	// today's enabled destination and revision.
	raw, headers = editor.call("PATCH", routePath,
		fmt.Sprintf(`{"target_thread_id":%q}`, ownerNoticeNextTarget), headers.Get("ETag"), 200)
	route := turnOneDecode[notificationRoute](t, raw)
	routeTag := headers.Get("ETag")
	raw, _ = editor.call("PATCH", "/cards/"+c.ID, `{"title":"After capture"}`, source.Cards[0].entityTag(), 200)
	current, ok := app.store.accepted(b.ID)
	if !ok {
		t.Fatal("missing current board")
	}
	snapshotDigest := m96FileDigest(t, app.store.snapshotPath(b.ID))
	// Remove good live content so both refusal and later repair are observable.
	for _, attachment := range source.Attachments {
		if err := os.WriteFile(filepath.Join(app.store.root, attachmentPath(attachment)), []byte("damaged live file"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	corruptPath := filepath.Join(t.TempDir(), "corrupt.zip")
	m96WriteArchive(t, corruptPath, original)
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	m96CorruptAttachment(t, corruptPath, detached)
	corruptDigest := m96FileDigest(t, corruptPath)
	send := func(path string) (client.Response, error) {
		t.Helper()
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		stat, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		return api.DoWithReader(http.MethodPut, "/snapshots/"+b.ID, file, stat.Size(), snapshotRepresentationTag(current), "application/zip")
	}
	if _, err := send(corruptPath); err == nil || errors.Is(err, client.ErrMutationOutcomeUnknown) {
		t.Fatalf("corrupt detached entry must have a known refusal: %v", err)
	}
	after, ok := app.store.accepted(b.ID)
	if !ok || !reflect.DeepEqual(after, current) || m96FileDigest(t, app.store.snapshotPath(b.ID)) != snapshotDigest {
		t.Fatal("corrupt archive changed accepted or durable state")
	}
	for _, attachment := range source.Attachments {
		got, err := os.ReadFile(filepath.Join(app.store.root, attachmentPath(attachment)))
		if err != nil || string(got) != "damaged live file" {
			t.Fatal("corrupt archive published an attachment before all entries were verified")
		}
	}
	reply, err := send(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := app.store.readback(b.ID)
	if err != nil || restored.Board.Revision <= current.Board.Revision || reply.BoardETag != restored.Board.entityTag() ||
		restored.Cards[0].Title != source.Cards[0].Title || len(restored.Attachments) != 10 {
		t.Fatalf("large archive did not restore the captured state: %v", err)
	}
	for i, attachment := range restored.Attachments {
		if attachment.Active != source.Attachments[i].Active || attachment.SHA256 != source.Attachments[i].SHA256 {
			t.Fatal("restore changed active/detached attachment metadata")
		}
		if _, err := readVerifiedAttachment(app.store.root, attachment); err != nil {
			t.Fatalf("restored active/detached bytes failed digest verification: %v", err)
		}
	}
	raw, headers = editor.call("GET", routePath, "", "", 200)
	if got := turnOneDecode[notificationRoute](t, raw); !reflect.DeepEqual(got, route) || headers.Get("ETag") != routeTag {
		t.Fatal("restore changed current notification routing")
	}
	if m96FileDigest(t, path) != item.SHA256 || m96FileDigest(t, corruptPath) != corruptDigest {
		t.Fatal("restore modified source files")
	}
}

func TestM86UploadedBackupTransportBound(t *testing.T) {
	for _, size := range []int64{MaxBackupArchiveBytes, MaxBackupArchiveBytes + 1} {
		r := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader([]byte("bad")))
		r.ContentLength = size
		x := request{r: r, w: httptest.NewRecorder()}
		archive, err := x.readUploadedBackup("")
		if archive != nil {
			archive.close()
			t.Fatal("invalid archive accepted")
		}
		if (size > MaxBackupArchiveBytes) != errors.Is(err, errTooBig) {
			t.Fatalf("inclusive ZIP transfer bound: %v", err)
		}
	}
	// Unknown-length uploads remain bounded by the reader as well.
	r := httptest.NewRequest(http.MethodPut, "/", io.NopCloser(bytes.NewReader([]byte("bad"))))
	r.ContentLength = -1
	x := request{r: r, w: httptest.NewRecorder()}
	if _, err := x.readUploadedBackup(""); err == nil {
		t.Fatal("truncated unknown-length archive accepted")
	}
}
