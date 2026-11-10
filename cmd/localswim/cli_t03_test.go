package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	t03Card        = "b671226b-6d19-4db1-91b8-a68bc6d29155"
	t03Board       = "2c525683-4c2f-4d98-9945-cead23c534ad"
	t03Lane        = "ecf04727-5c5e-4ef7-a365-490c88750fe4"
	t03ResourceTag = `"resource-etag-123"`
	t03BoardTag    = `"board-etag-123456"`
	t03Root        = "/api/2026-11-10"
)

func TestT03CLIWriteValidatorsAndUTF8(t *testing.T) {
	const text = " café 🚩\r\n\n"
	descriptionFile := filepath.Join(t.TempDir(), "description.txt")
	if err := os.WriteFile(descriptionFile, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input, expected string
		args                  []string
	}{
		{"description stdin", text, "description", []string{"card", "description", "--revision", "12", t03Card, "-"}},
		{"description file", "ignored", "description", []string{"card", "description", "--revision", "12", t03Card, descriptionFile}},
		{"stale", text, "", []string{"card", "description", "--revision", "11", t03Card, "-"}},
		{"lane rename", "", "label", []string{"lane", "rename", "--revision", "12", t03Lane, text}},
		{"lane rename stale", "", "", []string{"lane", "rename", "--revision", "11", t03Lane, text}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mutations atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", t03ResourceTag)
				switch {
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board:
					w.Header().Set("ETag", t03BoardTag)
					if tc.name != "lane rename" {
						t.Error("card edit queried the unrelated board revision")
					}
					_, _ = io.WriteString(w, `{"revision":99}`)
				case r.Method == http.MethodGet:
					_, _ = io.WriteString(w, `{"board_id":"`+t03Board+`","revision":12}`)
				case r.Method == http.MethodPatch:
					mutations.Add(1)
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body[tc.expected] != text {
						t.Error("mutation lost exact text")
					}
					if r.Header.Get("If-Match") != t03ResourceTag {
						t.Error("mutation did not bind the resource validator")
					}
					if tc.name == "lane rename" && body["board_etag"] != t03BoardTag {
						t.Error("rename did not bind the board validator")
					}
					w.Header().Set("X-Board-ETag", t03BoardTag)
					_, _ = io.WriteString(w, "{}")
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer server.Close()
			var out, stderr bytes.Buffer
			code := runCLIWithInput(tc.args, strings.NewReader(tc.input), &out, &stderr, t03Env(server.URL))
			if tc.expected == "" {
				if code != 1 || mutations.Load() != 0 || !strings.Contains(stderr.String(), "revision mismatch") {
					t.Fatalf("stale invocation dispatched a mutation or failed unexpectedly: %d %s", code, &stderr)
				}
			} else if code != 0 || mutations.Load() != 1 {
				t.Fatalf("mutation failed: %d %s", code, &stderr)
			}
		})
	}
}

func TestT03CLIRequiresExplicitInputSource(t *testing.T) {
	for _, tc := range []struct{ group, action, id string }{
		{"card", "patch", t03Card},
		{"card", "description", t03Card},
		{"comment", "post", t03Card},
		{"lane", "add", t03Board},
	} {
		t.Run(tc.group+" "+tc.action, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runCLIWithInput([]string{tc.group, tc.action, tc.id}, nil, io.Discard, &stderr, func(string) string {
				t.Fatal("CLI started executing a command with no input source")
				return ""
			})
			if code != 2 || !strings.Contains(stderr.String(), "FILE or -") {
				t.Fatalf("missing input source did not produce a usage error: %d %s", code, &stderr)
			}
		})
	}
}

func TestT03DescriptionInputCountsCodePoints(t *testing.T) {
	const maxCodePoints = 4_194_304
	text := strings.Repeat("🚩", maxCodePoints)
	body, err := readT03Input(strings.NewReader(text), true)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil || decoded["description"] != text {
		t.Fatal("CLI did not preserve a maximum-length multibyte description")
	}
	if _, err := readT03Input(strings.NewReader(strings.Repeat("x", maxCodePoints+1)), true); err == nil {
		t.Fatal("CLI accepted more than 4,194,304 description code points")
	}
}

func TestT03CLIAttachmentUploadUnknownNoReplay(t *testing.T) {
	t.Run("generated ID", func(t *testing.T) { testAttachmentReadback(t, "") })
	t.Run("caller ID", func(t *testing.T) { testAttachmentReadback(t, t03Lane) })
}

func testAttachmentReadback(t *testing.T, requestedID string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "café.bin")
	want := []byte{0xff, 0, 0x7f}
	if err := os.WriteFile(file, want, 0600); err != nil {
		t.Fatal(err)
	}
	var mutations atomic.Int32
	uploadIDs := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", t03ResourceTag)
		if r.Method == http.MethodGet && r.URL.Path == t03Root+"/cards/"+t03Card {
			_, _ = io.WriteString(w, "{}")
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, t03Root+"/attachments/") {
			_, _ = io.WriteString(w, `{"id":"`+strings.TrimPrefix(r.URL.Path, t03Root+"/attachments/")+`"}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != t03Root+"/attachments" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mutations.Add(1)
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Errorf("multipart upload failed: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.MultipartForm.RemoveAll()
		id := r.FormValue("id")
		select {
		case uploadIDs <- id:
		default:
		}
		if !boardIDPattern.MatchString(id) || id != strings.ToLower(id) || (requestedID != "" && id != requestedID) {
			t.Error("missing or noncanonical upload ID")
		}
		if r.FormValue("card_id") != t03Card || r.FormValue("expected_card_etag") != t03ResourceTag {
			t.Error("missing upload identity or validator")
		}
		part, meta, err := r.FormFile("file")
		if err != nil {
			t.Error("missing file")
			return
		}
		defer part.Close()
		data, err := io.ReadAll(part)
		if err != nil || !bytes.Equal(data, want) || meta.Filename != filepath.Base(file) {
			t.Error("upload lost file bytes or original name")
		}
		w.Header().Set("X-Board-ETag", t03BoardTag)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "{")
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	args := []string{"attachment", "upload"}
	if requestedID != "" {
		args = append(args, "--id", strings.ToUpper(requestedID))
	}
	args = append(args, t03Card, file)
	code := runCLIWithInput(args, strings.NewReader(""), &out, &stderr, t03Env(server.URL))
	if code != 1 || mutations.Load() != 1 || !strings.Contains(stderr.String(), "Mutation outcome unknown") {
		t.Fatalf("upload was replayed or the outcome was misreported: %d %s", code, &stderr)
	}
	id := <-uploadIDs
	if !strings.Contains(stderr.String(), "attachment ID "+id) {
		t.Fatalf("missing attachment ID in outcome: %s", &stderr)
	}
	out.Reset()
	stderr.Reset()
	code = runCLIWithInput([]string{"attachment", "get", id}, strings.NewReader(""), &out, &stderr, t03Env(server.URL))
	if code != 0 || out.String() != `{"id":"`+id+`"}`+"\n" {
		t.Fatalf("focused attachment readback failed: %d %s", code, &stderr)
	}
}

func t03Env(origin string) func(string) string {
	return func(name string) string {
		switch name {
		case "LOCALSWIM_URL":
			return origin
		case "LOCALSWIM_TOKEN":
			return "t03-test-token"
		default:
			return ""
		}
	}
}

func TestCLICardAssignRequiresOwner(t *testing.T) {
	for _, args := range [][]string{
		{"card", "assign", t03Card},
		{"card", "assign", t03Card, "none"},
		{"card", "assign", t03Card, ""},
	} {
		var out, stderr bytes.Buffer
		code := runCLIWithInput(args, nil, &out, &stderr, func(string) string {
			t.Fatal("invalid owner reached execution")
			return ""
		})
		if code != 2 || !strings.HasPrefix(stderr.String(), "ERROR ") {
			t.Fatalf("expected command error for %v: %d %s", args, code, stderr.String())
		}
	}
	command, err := parseCLI([]string{"card", "assign", t03Card, t03Board})
	var body map[string]string
	if err != nil || json.Unmarshal(command.body, &body) != nil || body["owner_id"] != t03Board {
		t.Fatalf("valid owner assignment: %+v, %v", command, err)
	}
}
