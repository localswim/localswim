package v2

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestM074InspectAndExportAfterWriterStops(t *testing.T) {
	for _, availability := range []string{"open", "closed"} {
		t.Run(availability, func(t *testing.T) {
			root := t.TempDir()
			human := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("h"), 32))
			bot := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte("b"), 32))
			app, err := Open(root, human, bot)
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
			editor := turnOneClient{t, server.URL, human, &http.Client{Timeout: 5 * time.Second}}
			raw, _ := editor.call("POST", "/boards", `{"name":"Saved board"}`, "", 201)
			created := turnOneDecode[board](t, raw)
			item := attachmentHTTPCard(t, editor, created)
			boardRoute := "/boards/" + created.ID
			if availability == "closed" {
				raw, _ = editor.call("GET", boardRoute, "", "", 200)
				current := turnOneDecode[board](t, raw)
				editor.call("PATCH", boardRoute, `{"availability":"closed"}`, current.entityTag(), 200)
				editor.call("PATCH", "/cards/"+item.ID, `{"title":"Refused"}`, item.entityTag(), 503)
			}

			snapshotRoute := "/snapshots/" + created.ID
			ticketRoute := fmt.Sprintf("%s?view=ticket&ticket=%d&include_prose=true", boardRoute, item.Ticket)
			metadata, _ := editor.call("GET", boardRoute, "", "", 200)
			export, _ := editor.call("GET", snapshotRoute, "", "", 200)
			ticket, _ := editor.call("GET", ticketRoute, "", "", 200)
			if got := turnOneDecode[board](t, metadata); got.Availability != availability {
				t.Fatalf("board availability = %q, want %q", got.Availability, availability)
			}
			anonymous := editor
			anonymous.token = ""
			for _, route := range []string{boardRoute, snapshotRoute, ticketRoute} {
				anonymous.call("GET", route, "", "", 401)
			}

			server.Close()
			if err := app.Close(); err != nil {
				t.Fatal(err)
			}
			savedPath := filepath.Join(root, "boards", created.ID+".json")
			before, err := os.Stat(savedPath)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := os.ReadFile(savedPath)
			if err != nil {
				t.Fatal(err)
			}

			native, err := OpenNativeBoard(root, strings.ToUpper(created.ID))
			if err != nil {
				t.Fatal(err)
			}
			assertNativeProjection(t, "metadata", native.MetadataJSON, metadata)
			assertNativeProjection(t, "export", native.ExportJSON, export)
			assertNativeProjection(t, "ticket", func() (json.RawMessage, error) {
				return native.InspectTicketJSON(item.Ticket, true)
			}, ticket)

			after, err := os.Stat(savedPath)
			if err != nil {
				t.Fatal(err)
			}
			afterBytes, err := os.ReadFile(savedPath)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || !bytes.Equal(saved, afterBytes) {
				t.Fatal("saved snapshot changed after read-only inspection")
			}
		})
	}
}

func assertNativeProjection(t *testing.T, label string, native func() (json.RawMessage, error), live []byte) {
	t.Helper()
	got, err := native()
	if err != nil {
		t.Fatalf("%s projection: %v", label, err)
	}
	if !bytes.Equal(bytes.TrimSpace(live), got) {
		t.Fatalf("native and live %s projections differ", label)
	}
}

func TestM074NativeReadUsesValidatorWithoutCreatingWriterFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "boards")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	state := nativeVerificationFixture()
	path := filepath.Join(dir, state.Board.ID+".json")
	data := marshalVerificationFixture(t, state)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenNativeBoard(root, state.Board.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenNativeBoard(root, newID()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing board error = %v", err)
	}
	if _, err := OpenNativeBoard(root, "../boards"); err == nil {
		t.Fatal("native source accepted an invalid board ID")
	}

	for _, edit := range []struct{ before, after string }{
		{`"format_version":2`, `"format_version":1`},
		{state.Board.ID, newID()},
		{state.Board.DefaultOwnerID, newID()},
	} {
		damaged := bytes.Replace(data, []byte(edit.before), []byte(edit.after), 1)
		if bytes.Equal(damaged, data) {
			t.Fatal("corruption fixture was unchanged")
		}
		if err := os.WriteFile(path, damaged, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenNativeBoard(root, state.Board.ID); !errors.Is(err, errInvalidSnapshot) {
			t.Fatalf("corrupt board error = %v", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "boards" {
		t.Fatal("native read created workspace state")
	}
}
