package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const t06Operation = "019b1111-2222-7333-a444-555555555555"

func TestT06ParseAndHelp(t *testing.T) {
	for _, args := range [][]string{
		{"lane", "set-key", "--revision", "12", t03Board, t03Lane, "ready_now"},
		{"board", "get", "--view", "lanes", t03Board},
		{"ticket-order", "preview", t03Board, t03Lane, t03Card},
		{"card", "set-priorities", "--revision", "12", t03Board, t03Lane, t03Card},
		{"backup", "create", "--id", t06Operation, "--revision", "12", t03Board},
		{"backup", "info", t03Board, t06Operation},
		{"backup", "download", t03Board, t06Operation, "-"},
	} {
		if _, err := parseCLI(args); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"card", "set-priorities", t03Board},
		{"card", "set-priorities", "--revision", "0", t03Board, t03Lane, t03Card},
		{"backup", "create", "--id", "bad", t03Board},
		{"backup", "download", t06Operation},
		{"board", "get", "--view", "other", t03Board},
	} {
		if _, err := parseCLI(args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
	for _, command := range []string{"lane set-key", "card set-priorities", "ticket-order preview", "backup create", "backup info BOARD_ID BACKUP_ID", "backup download", "board get [--view lanes]"} {
		if !strings.Contains(cliHelp, command) {
			t.Errorf("help missing %q", command)
		}
	}
}

func TestT06BatchAndBackupValidators(t *testing.T) {
	for _, action := range []string{"batch", "backup"} {
		for _, revision := range []string{"12", "11"} {
			t.Run(action+revision, func(t *testing.T) {
				writes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						if r.URL.Path != t03Root+"/boards/"+t03Board {
							t.Error("wrong board preflight")
						}
						w.Header().Set("ETag", t03BoardTag)
						io.WriteString(w, `{"revision":12}`)
						return
					}
					writes++
					var body map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Fatal("invalid body")
					}
					if string(body["board_id"]) != fmt.Sprintf("%q", t03Board) || string(body["expected_board_etag"]) != fmt.Sprintf("%q", t03BoardTag) {
						t.Error("missing board conditions")
					}
					if action == "batch" {
						if r.Method != http.MethodPatch || r.URL.Path != t03Root+"/cards" || body["card_ids"] == nil || body["priority_id"] == nil {
							t.Error("wrong batch")
						}
						w.Header().Set("X-Board-ETag", t03BoardTag)
						fmt.Fprintf(w, `{"board_id":%q,"board_revision":13,"cards":[],"changes":[]}`, t03Board)
					} else {
						if r.Method != http.MethodPost || r.URL.Path != t03Root+"/backups" || string(body["id"]) != fmt.Sprintf("%q", t06Operation) {
							t.Error("wrong capture")
						}
						w.Header().Set("ETag", t03ResourceTag)
						w.WriteHeader(http.StatusCreated)
						io.WriteString(w, t06BackupMetadata([]byte("zip")))
					}
				}))
				defer server.Close()
				args := []string{"card", "set-priorities", "--revision", revision, t03Board, t03Lane, t03Card}
				if action == "backup" {
					args = []string{"backup", "create", "--revision", revision, "--id", t06Operation, t03Board}
				}
				var stdout, stderr bytes.Buffer
				code := runCLIWithInput(args, strings.NewReader(fmt.Sprintf(`{"card_ids":[%q],"priority_id":%q}`, t03Card, t03Lane)), &stdout, &stderr, t03Env(server.URL))
				if revision == "12" && (code != 0 || writes != 1) {
					t.Fatalf("code=%d writes=%d error=%s", code, writes, &stderr)
				}
				if revision == "11" && (code == 0 || writes != 0) {
					t.Fatalf("stale code=%d writes=%d", code, writes)
				}
			})
		}
	}
}

func TestT06ReadViews(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("read performed a mutation")
		}
		switch r.URL.Path {
		case t03Root + "/boards/" + t03Board:
			if r.URL.Query().Get("view") == "ticket" {
				writeScopeView(w, r)
				return
			}
			if r.URL.Query().Get("view") != "lanes" {
				t.Error("missing lane view")
			}
			io.WriteString(w, `{"board":{},"lanes":[],"cards":[]}`)
		case t03Root + "/lanes/" + t03Lane:
			fmt.Fprintf(w, `{"id":%q,"board_id":%q}`, t03Lane, t03Board)
		case t03Root + "/cards/" + t03Card:
			writeScopeCard(w, t03Card, t03Board, 12)
		case t03Root + "/ticket-orders/" + t03Lane:
			w.Header().Set("X-Board-ETag", fmt.Sprintf("\"%s-12\"", t03Board))
			if r.URL.Query().Get("preview") != "true" || r.URL.Query().Get("card_id") != t03Card {
				t.Error("missing selection")
			}
			fmt.Fprintf(w, `{"board_id":%q,"lane_id":%q,"board_revision":12,"selected_card_ids":[],"current":[],"proposed":[],"changed":false}`, t03Board, t03Lane)
		default:
			t.Error("unexpected resource")
		}
	}))
	defer server.Close()
	for _, args := range [][]string{{"board", "get", "--view", "lanes", t03Board}, {"ticket-order", "preview", t03Board, t03Lane, t03Card}} {
		var out, errOut bytes.Buffer
		if code := runCLIWithInput(args, strings.NewReader(""), &out, &errOut, t03Env(server.URL)); code != 0 {
			t.Fatalf("%v: %s", args, &errOut)
		}
	}
}

func t06BackupMetadata(data []byte) string {
	digest := sha256.Sum256(data)
	return fmt.Sprintf(`{"id":%q,"board_id":%q,"board_revision":12,"sha2_256":%q,"size":%d,"attachment_ids":[]}`, t06Operation, t03Board, hex.EncodeToString(digest[:]), len(data))
}

func TestT06BackupInfoOnlyReadsMetadata(t *testing.T) {
	metadata := t06BackupMetadata([]byte("zip"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != t03Root+"/backups/"+t06Operation {
			t.Errorf("unexpected backup info request: %s %s", r.Method, r.URL.RequestURI())
			w.WriteHeader(http.StatusNotFound)
			return
		}
		io.WriteString(w, metadata)
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	code := runCLIWithInput([]string{"backup", "info", t03Board, t06Operation}, strings.NewReader(""), &out, &stderr, t03Env(server.URL))
	var compact bytes.Buffer
	if code != 0 || stderr.Len() != 0 || json.Compact(&compact, out.Bytes()) != nil || compact.String() != metadata {
		t.Fatalf("backup info returned code=%d, output=%q, error=%s", code, &out, &stderr)
	}
}

func TestT06BackupGetIsUnknown(t *testing.T) {
	var out, stderr bytes.Buffer
	code := runCLIWithInput([]string{"backup", "get", t06Operation}, strings.NewReader(""), &out, &stderr, func(name string) string {
		t.Errorf("obsolete command read environment variable %s", name)
		return ""
	})
	if code != 2 || out.Len() != 0 || !strings.Contains(stderr.String(), "unknown command") ||
		strings.Contains(stderr.String(), "backup get") {
		t.Fatalf("backup get returned code=%d, output=%q, error=%s", code, &out, &stderr)
	}
}

func TestT06BackupDownloadVerifiedBeforeOutput(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(corrupt), func(t *testing.T) {
			data := []byte("PK\x03\x04archive\x00bytes")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/content") {
					w.Header().Set("Content-Type", "application/zip")
					if corrupt {
						data = bytes.Repeat([]byte{'X'}, len(data))
					}
					w.Write(data)
					return
				}
				io.WriteString(w, t06BackupMetadata(data))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "backup.zip")
			var out, errOut bytes.Buffer
			code := runCLIWithInput([]string{"backup", "download", t03Board, t06Operation, path}, strings.NewReader(""), &out, &errOut, t03Env(server.URL))
			if corrupt {
				if code == 0 {
					t.Fatal("corrupt download accepted")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("corrupt bytes published")
				}
			} else {
				got, err := os.ReadFile(path)
				if code != 0 || err != nil || !bytes.Equal(got, data) {
					t.Fatalf("download failed: %s", &errOut)
				}
			}
		})
	}
}

func TestT06BackupUnknownIncludesIdentityWithoutRetry(t *testing.T) {
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", t03BoardTag)
			io.WriteString(w, `{"revision":12}`)
			return
		}
		writes++
		w.Header().Set("ETag", t03ResourceTag)
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{`)
	}))
	defer server.Close()
	var out, errOut bytes.Buffer
	code := runCLIWithInput([]string{"backup", "create", "--id", t06Operation, t03Board}, strings.NewReader(""), &out, &errOut, t03Env(server.URL))
	if code == 0 || writes != 1 || !strings.Contains(errOut.String(), "Mutation outcome unknown") || !strings.Contains(errOut.String(), "backup info "+t03Board+" "+t06Operation) {
		t.Fatalf("unknown response %d %d %s", code, writes, &errOut)
	}
}

func TestT06LaneKeyUsesLaneAndBoardValidators(t *testing.T) {
	for _, revision := range []string{"12", "11"} {
		t.Run(revision, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/lanes/"+t03Lane:
					w.Header().Set("ETag", t03ResourceTag)
					fmt.Fprintf(w, `{"id":%q,"board_id":%q,"revision":12}`, t03Lane, t03Board)
				case r.Method == http.MethodGet && r.URL.Path == t03Root+"/boards/"+t03Board:
					w.Header().Set("ETag", t03BoardTag)
					fmt.Fprintf(w, `{"id":%q,"revision":99}`, t03Board)
				case r.Method == http.MethodPatch && r.URL.Path == t03Root+"/lanes/"+t03Lane:
					writes++
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || body["key"] != "ready_now" || body["board_etag"] != t03BoardTag || r.Header.Get("If-Match") != t03ResourceTag {
						t.Error("missing lane key validators")
					}
					w.Header().Set("ETag", t03ResourceTag)
					w.Header().Set("X-Board-ETag", t03BoardTag)
					io.WriteString(w, `{}`)
				default:
					t.Error("unexpected request")
				}
			}))
			defer server.Close()
			var out, errOut bytes.Buffer
			code := runCLIWithInput([]string{"lane", "set-key", "--revision", revision, t03Board, t03Lane, "ready_now"}, strings.NewReader(""), &out, &errOut, t03Env(server.URL))
			if revision == "12" && (code != 0 || writes != 1) {
				t.Fatalf("key failed: %s", &errOut)
			}
			if revision == "11" && (code == 0 || writes != 0) {
				t.Fatal("stale lane key wrote")
			}
		})
	}
}

func TestT06BackupAcceptedOutputFailureRetainsGeneratedIdentity(t *testing.T) {
	writes := 0
	var backupID string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("ETag", t03BoardTag)
			io.WriteString(w, `{"revision":12}`)
			return
		}
		writes++
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		backupID = body.ID
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, strings.ReplaceAll(t06BackupMetadata([]byte("zip")), t06Operation, backupID))
	}))
	defer server.Close()
	reader, writer := io.Pipe()
	reader.Close()
	defer writer.Close()
	var stderr bytes.Buffer
	code := runCLIWithInput([]string{"backup", "create", t03Board}, strings.NewReader(""), writer, &stderr, t03Env(server.URL))
	if code == 0 || writes != 1 || !operationIDPattern.MatchString(backupID) ||
		!strings.Contains(stderr.String(), "service accepted backup "+backupID) ||
		!strings.Contains(stderr.String(), "response could not be written") ||
		strings.Contains(stderr.String(), "Mutation outcome unknown") {
		t.Fatalf("output failure lost known capture result: code=%d writes=%d id=%q error=%s", code, writes, backupID, &stderr)
	}
}
