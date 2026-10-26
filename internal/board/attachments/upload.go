package attachments

import (
	"bytes"
	"errors"
	"io"
)

var ErrTooLarge = errors.New("attachment exceeds byte limit")

// Upload owns one bounded raw byte buffer. Callers can obtain fresh read cursors
// but cannot mutate its storage. The multipart adapter transfers this value to
// Add without allocating a second file-sized defensive copy.
type Upload struct{ data []byte }

func Buffer(r io.Reader) (*Upload, error) {
	if r == nil {
		return nil, ErrUnavailable
	}
	b := make([]byte, MaxFileBytes+1)
	// Never give an external source access to final storage. Even a Reader
	// retaining its destination can only retain this bounded scratch buffer.
	var scratch [4096]byte
	n, empty := 0, 0
	for {
		part := scratch[:]
		if remaining := len(b) - n; remaining < len(part) {
			part = part[:remaining]
		}
		count, e := r.Read(part)
		if count < 0 || count > len(part) {
			return nil, ErrUnavailable
		}
		copy(b[n:], part[:count])
		n += count
		if n > MaxFileBytes {
			return nil, ErrTooLarge
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if count == 0 {
			empty++
			if empty == 100 {
				return nil, io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
	return &Upload{data: b[:n:n]}, nil
}
func (u *Upload) Len() int {
	if u == nil {
		return 0
	}
	return len(u.data)
}
func (u *Upload) Reader() io.Reader {
	if u == nil {
		return &uploadReader{}
	}
	return &uploadReader{reader: *bytes.NewReader(u.data)}
}

// Keep the cursor in a named field: embedding bytes.Reader would promote
// WriterTo, which can pass Upload's backing slice to a destination Writer.
// Read only copies into the caller's buffer, and every Reader has its own cursor.
type uploadReader struct{ reader bytes.Reader }

func (r *uploadReader) Read(p []byte) (int, error) { return r.reader.Read(p) }
