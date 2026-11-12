package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestM86RestoreStreamsLargeFileAndStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("PK\x03\x04")); err != nil {
		t.Fatal(err)
	}
	const size = maxCLIInput + 1
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, fromStdin := range []bool{false, true} {
		t.Run(fmt.Sprint(fromStdin), func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					w.Header().Set("ETag", t03BoardTag)
					_, _ = io.WriteString(w, `{"board":{"revision":12}}`)
					return
				}
				writes++
				n, err := io.Copy(io.Discard, r.Body)
				if err != nil || n != size || r.ContentLength != size ||
					r.Header.Get("Content-Type") != "application/zip" || r.Header.Get("If-Match") != t03BoardTag {
					t.Errorf("streamed restore size=%d, length=%d, error=%v", n, r.ContentLength, err)
				}
				w.Header().Set("X-Board-ETag", t03ResourceTag)
				_, _ = io.WriteString(w, `{"revision":13}`)
			}))
			defer server.Close()
			input, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			argument := path
			if fromStdin {
				argument = "-"
			}
			var stdout, stderr bytes.Buffer
			code := runCLIWithInput([]string{"board", "restore", "--revision", "12", t03Board, argument}, input, &stdout, &stderr, t03Env(server.URL))
			if code != 0 || writes != 1 {
				t.Fatalf("restore code=%d, writes=%d, stderr=%s", code, writes, &stderr)
			}
		})
	}
}

func TestM86RestoreInputBoundsAndPrivateCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.zip")
	original := []byte("PK\x03\x04private input")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	staged, media, err := prepareRestoreInput(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRestoreInput(staged)
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(staged)
	if err != nil || !bytes.Equal(got, original) || media != "application/zip" {
		t.Fatalf("private copy changed or wrong media type: %v", err)
	}
	for _, input := range []string{"{", "plain text"} {
		if file, _, err := prepareRestoreInput("-", strings.NewReader(input)); err == nil {
			closeRestoreInput(file)
			t.Fatal("invalid restore content accepted")
		}
	}
	// JSON retains its existing bound independently of the ZIP transfer limit.
	oversized := io.MultiReader(strings.NewReader("{"), io.LimitReader(zeroRestoreReader{}, maxCLIInput))
	if file, _, err := prepareRestoreInput("-", oversized); err == nil {
		closeRestoreInput(file)
		t.Fatal("oversized JSON accepted")
	}
}

type zeroRestoreReader struct{}

func (zeroRestoreReader) Read(data []byte) (int, error) {
	clear(data)
	return len(data), nil
}
