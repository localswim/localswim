package attachments

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	B "github.com/localswim/localswim/internal/board/attachments"
)

func multipartFixture(t *testing.T, names []string, metadata, file []byte, encoding string) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	for _, name := range names {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="../../ignored.exe"`, name))
		media := "application/octet-stream"
		data := file
		if name == "metadata" {
			media = "application/json"
			data = metadata
		}
		h.Set("Content-Type", media)
		if encoding != "" {
			h.Set("Content-Transfer-Encoding", encoding)
		}
		p, e := m.CreatePart(h)
		if e != nil {
			t.Fatal(e)
		}
		p.Write(data)
	}
	m.Close()
	return b.Bytes(), m.FormDataContentType()
}
func parse(t *testing.T, b []byte, media string) (body, int) {
	t.Helper()
	r := httptest.NewRequest("POST", "https://invalid.localhost/", bytes.NewReader(b))
	r.Header.Set("Content-Type", media)
	return multipartBody(httptest.NewRecorder(), r)
}
func TestMultipartExactRawAndZero(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("=41\x00\xff\r\nBinary")} {
		b, m := multipartFixture(t, []string{"file", "metadata"}, []byte(`{"strict":true}`), data, "")
		out, status := parse(t, b, m)
		if status != 0 || !bytes.Equal(fileBytes(out), data) || string(out.metadata) != `{"strict":true}` {
			t.Fatal("raw bytes changed", status)
		}
	}
}
func TestMultipartRefusals(t *testing.T) {
	b, m := multipartFixture(t, []string{"metadata", "file"}, []byte("{}"), []byte("a"), "")
	for _, tt := range []struct {
		name string
		b    []byte
		m    string
		want int
	}{
		{"truncated", b[:len(b)-5], m, 400}, {"epilogue", append(append([]byte{}, b...), []byte("bad")...), m, 400}, {"media", b, "application/json", 400}, {"missing boundary", b, "multipart/form-data", 400},
		{"marker ended epilogue", append(append(append([]byte{}, b...), []byte("JUNK")...), b[bytes.LastIndex(b, []byte("\r\n--")):]...), m, 400},
		{"preamble", append([]byte("preamble\r\n"), b...), m, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, status := parse(t, tt.b, tt.m)
			if status != tt.want {
				t.Fatal(status)
			}
		})
	}
	for _, names := range [][]string{{"metadata"}, {"file", "file"}, {"metadata", "metadata"}, {"metadata", "file", "extra"}, {"meta", "file"}} {
		t.Run(strings.Join(names, "-"), func(t *testing.T) {
			b, m := multipartFixture(t, names, []byte("{}"), nil, "")
			if _, status := parse(t, b, m); status != 400 {
				t.Fatal(status)
			}
		})
	}
	for _, encoding := range []string{"quoted-printable", "base64", "binary", "identity"} {
		b, m := multipartFixture(t, []string{"metadata", "file"}, []byte("{}"), []byte("=41"), encoding)
		if _, status := parse(t, b, m); status != 400 {
			t.Fatal("encoded raw disagreement", encoding, status)
		}
	}
}
func TestMultipartLimits(t *testing.T) {
	for _, tt := range []struct {
		name           string
		metadata, file []byte
		status         int
	}{{"metadata exact", bytes.Repeat([]byte("x"), maxMetadata), nil, 0}, {"metadata over", bytes.Repeat([]byte("x"), maxMetadata+1), nil, 413}, {"file exact", nil, bytes.Repeat([]byte("x"), B.MaxFileBytes), 0}, {"file over", nil, bytes.Repeat([]byte("x"), B.MaxFileBytes+1), 413}} {
		t.Run(tt.name, func(t *testing.T) {
			b, m := multipartFixture(t, []string{"metadata", "file"}, tt.metadata, tt.file, "")
			if _, status := parse(t, b, m); status != tt.status {
				t.Fatal(status)
			}
		})
	}
	b, m := multipartFixture(t, []string{"metadata", "file"}, nil, nil, "")
	r := httptest.NewRequest("POST", "https://invalid.localhost/", bytes.NewReader(b))
	r.ContentLength = -1
	r.Header.Set("Content-Type", m)
	r.Header.Set("Content-Encoding", "gzip")
	if _, s := multipartBody(httptest.NewRecorder(), r); s != 400 {
		t.Fatal(s)
	}
	r.Header.Del("Content-Encoding")
	r.ContentLength = int64(len(b) + 1)
	if _, s := multipartBody(httptest.NewRecorder(), r); s != 400 {
		t.Fatal("raw length mismatch", s)
	}
}
func TestExactRoutes(t *testing.T) {
	b := "10000000-0000-0000-0000-000000000001"
	c := "20000000-0000-0000-0000-000000000001"
	upload := "/api/v1/boards/" + b + "/cards/" + c + "/attachments"
	download := "/api/v1/boards/" + b + "/attachments/" + c
	for _, tt := range []struct {
		method, path string
		status       int
	}{{"POST", upload, 0}, {"GET", download, 0}, {"GET", upload, 405}, {"POST", download, 405}, {"HEAD", download, 405}, {"GET", download + "/", 404}, {"DELETE", download, 405}, {"POST", upload + "?x=1", 400}, {"POST", upload + "?", 400}, {"GET", "/api/v1/boards/invalid/attachments/" + c, 404}, {"GET", download + "/replace", 404}} {
		r := httptest.NewRequest(tt.method, "https://invalid.localhost"+tt.path, nil)
		_, s := selectRoute(r)
		if s != tt.status {
			t.Fatal(tt.method, tt.path, s)
		}
	}
}

func fileBytes(b body) []byte { data, _ := io.ReadAll(b.file.Reader()); return data }

type fragmentedReader struct {
	r io.Reader
	n int
}

func (f fragmentedReader) Read(p []byte) (int, error) {
	if len(p) > f.n {
		p = p[:f.n]
	}
	return f.r.Read(p)
}
func TestRawClosingDelimiterAcrossReadBoundaries(t *testing.T) {
	b, m := multipartFixture(t, []string{"metadata", "file"}, []byte("{}"), []byte("raw\x00=41"), "")
	for _, size := range []int{1, 7, 31, 4096} {
		for _, newline := range []bool{false, true} {
			data := b
			if !newline {
				data = b[:len(b)-2]
			}
			r := httptest.NewRequest("POST", "https://invalid.localhost/", fragmentedReader{bytes.NewReader(data), size})
			r.Header.Set("Content-Type", m)
			out, status := multipartBody(httptest.NewRecorder(), r)
			if status != 0 || !bytes.Equal(fileBytes(out), []byte("raw\x00=41")) {
				t.Fatal("fragmented terminal boundary", size, newline, status)
			}
		}
	}
}
