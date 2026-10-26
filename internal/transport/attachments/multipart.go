package attachments

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	B "github.com/localswim/localswim/internal/board/attachments"
)

const maxMetadata = 16384
const maxFraming = 16384
const maxRequest = B.MaxFileBytes + maxMetadata + maxFraming

type body struct {
	metadata []byte
	file     *B.Upload
}

// rawReader bounds the entire encoded request independently of part lengths and
// tracks the FIRST closing delimiter, including bytes the multipart reader has
// buffered ahead. A later delimiter can never disguise an earlier epilogue.
type rawReader struct {
	r              io.Reader
	n              int64
	tail           []byte
	marker, prefix []byte
	firstEnd       int64
}

func (r *rawReader) Read(p []byte) (int, error) {
	n, e := r.r.Read(p)
	old := r.n
	r.n += int64(n)
	if len(r.prefix) < len(r.marker) {
		need := len(r.marker) - len(r.prefix)
		if need > n {
			need = n
		}
		r.prefix = append(r.prefix, p[:need]...)
	}
	window := append(append([]byte{}, r.tail...), p[:n]...)
	if r.firstEnd == 0 {
		for offset := 0; offset < len(window); {
			at := bytes.Index(window[offset:], r.marker)
			if at < 0 {
				break
			}
			at += offset
			end := at + len(r.marker)
			if end+2 <= len(window) && window[end] == '\r' && window[end+1] == '\n' {
				r.firstEnd = old - int64(len(r.tail)) + int64(end+2)
				break
			}
			offset = at + 1
		}
	}
	if n >= 160 {
		r.tail = append(r.tail[:0], p[n-160:n]...)
	} else if n > 0 {
		r.tail = append(r.tail, p[:n]...)
		if len(r.tail) > 160 {
			copy(r.tail, r.tail[len(r.tail)-160:])
			r.tail = r.tail[:160]
		}
	}
	return n, e
}

func multipartBody(w http.ResponseWriter, r *http.Request) (body, int) {
	var out body
	if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) != 0 || len(r.Header.Values("Content-Transfer-Encoding")) != 0 {
		return out, 400
	}
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	boundary := params["boundary"]
	if e != nil || media != "multipart/form-data" || len(params) != 1 || boundary == "" || len(boundary) > 70 {
		return out, 400
	}
	if r.ContentLength > maxRequest {
		return out, 413
	}
	raw := &rawReader{r: http.MaxBytesReader(w, r.Body, maxRequest), marker: []byte("\r\n--" + boundary + "--")}
	mr := multipart.NewReader(raw, boundary)
	seen := map[string]bool{}
	for n := 0; ; n++ {
		p, e := mr.NextRawPart()
		if e == io.EOF {
			if n != 2 {
				return body{}, 400
			}
			break
		}
		if e != nil {
			return body{}, bodyStatus(e)
		}
		if n >= 2 {
			return body{}, 400
		}
		// No implicit quoted-printable decoding: declared length/hash refer to
		// exactly these raw bytes. Filenames convey no name or path authority.
		if len(p.Header) != 2 || len(p.Header.Values("Content-Disposition")) != 1 || len(p.Header.Values("Content-Type")) != 1 {
			return body{}, 400
		}
		disposition, dp, e := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		name := dp["name"]
		if e != nil || disposition != "form-data" || (name != "metadata" && name != "file") || seen[name] {
			return body{}, 400
		}
		seen[name] = true
		for key := range dp {
			if key != "name" && key != "filename" {
				return body{}, 400
			}
		}
		want, limit := "application/octet-stream", B.MaxFileBytes
		if name == "metadata" {
			want, limit = "application/json", maxMetadata
		}
		if p.Header.Get("Content-Type") != want {
			return body{}, 400
		}
		if name == "metadata" {
			out.metadata, e = io.ReadAll(io.LimitReader(p, int64(limit)+1))
			if len(out.metadata) > limit {
				return body{}, 413
			}
		} else {
			out.file, e = B.Buffer(p)
		}
		if e != nil {
			if errors.Is(e, B.ErrTooLarge) {
				return body{}, 413
			}
			return body{}, bodyStatus(e)
		}
		if e = p.Close(); e != nil {
			return body{}, 400
		}
	}
	// Consume the underlying stream too: a parser's terminal boundary is not
	// proof that the request had no encoded tail or exceeded no global bound.
	if _, e = io.Copy(io.Discard, raw); e != nil {
		return body{}, bodyStatus(e)
	}
	start := []byte("--" + boundary + "\r\n")
	complete := raw.firstEnd == raw.n || raw.firstEnd == 0 && bytes.HasSuffix(raw.tail, raw.marker)
	if !complete || !bytes.HasPrefix(raw.prefix, start) || raw.n-int64(len(out.metadata)+out.file.Len()) > maxFraming || (r.ContentLength >= 0 && r.ContentLength != raw.n) {
		return body{}, 400
	}
	if !seen["metadata"] || !seen["file"] {
		return body{}, 400
	}
	return out, 0
}
func bodyStatus(e error) int {
	var limit *http.MaxBytesError
	if errors.As(e, &limit) {
		return 413
	}
	return 400
}
