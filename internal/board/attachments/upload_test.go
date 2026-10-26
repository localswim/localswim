package attachments

import (
	"bytes"
	"io"
	"testing"
)

type mutatingWriter struct{ calls int }

type retainingReader struct {
	data     []byte
	retained []byte
}

func (r *retainingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.retained = p[:n]
	r.data = r.data[n:]
	return n, nil
}

func (r *retainingReader) mutate() {
	for n := range r.retained {
		r.retained[n] = 'x'
	}
}

func (w *mutatingWriter) Write(p []byte) (int, error) {
	w.calls++
	for n := range p {
		p[n] = 'x'
	}
	return len(p), nil
}

func TestUploadReaderDoesNotExposeBackingBytes(t *testing.T) {
	input := []byte("good")
	u, err := Buffer(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	r := u.Reader()
	if _, ok := r.(io.WriterTo); ok {
		t.Fatal("Reader exposes WriterTo backing-slice access")
	}
	if _, ok := r.(io.ReaderFrom); ok {
		t.Fatal("Reader exposes a mutation method")
	}
	if _, ok := r.(*bytes.Reader); ok {
		t.Fatal("Reader exposes its underlying cursor")
	}
	writer := &mutatingWriter{}
	if n, err := io.Copy(writer, r); err != nil || n != 4 || writer.calls != 1 {
		t.Fatal("copy mutation probe did not execute", n, err, writer.calls)
	}
	// Direct reads also expose only the caller's buffer.
	read := make([]byte, 4)
	if _, err := io.ReadFull(u.Reader(), read); err != nil {
		t.Fatal(err)
	}
	read[0] = 'Y'
	actual, err := io.ReadAll(u.Reader())
	if err != nil || string(actual) != "good" || string(u.data) != "good" {
		t.Fatal("reader mutated the upload")
	}
	if len(u.data) != 4 || cap(u.data) != 4 {
		t.Fatal("upload length/capacity changed")
	}
}

func TestUploadReadersHaveIndependentCursors(t *testing.T) {
	u, err := Buffer(bytes.NewReader([]byte("good")))
	if err != nil {
		t.Fatal(err)
	}
	a, b := u.Reader(), u.Reader()
	one := make([]byte, 1)
	if _, err := io.ReadFull(a, one); err != nil || string(one) != "g" {
		t.Fatal("first cursor", err)
	}
	whole, err := io.ReadAll(b)
	if err != nil || string(whole) != "good" {
		t.Fatal("second cursor shared position", err)
	}
	rest, err := io.ReadAll(a)
	if err != nil || string(rest) != "ood" {
		t.Fatal("first cursor moved by second", err)
	}
	var nilUpload *Upload
	if data, err := io.ReadAll(nilUpload.Reader()); err != nil || len(data) != 0 {
		t.Fatal("nil upload reader", err)
	}
}

func TestUploadSourceReaderCannotRetainPrivateStorage(t *testing.T) {
	for _, length := range []int{0, 4, 4096, 4097, MaxFileBytes} {
		expected := bytes.Repeat([]byte("g"), length)
		source := &retainingReader{data: expected}
		u, err := Buffer(source)
		if err != nil {
			t.Fatal(err)
		}
		source.mutate()
		actual, err := io.ReadAll(u.Reader())
		if err != nil || !bytes.Equal(actual, expected) || !bytes.Equal(u.data, expected) {
			t.Fatal("retained source buffer changed upload", length, err)
		}
		if len(source.retained) > 4096 || len(u.data) != length || cap(u.data) != length {
			t.Fatal("scratch/final allocation bounds", length)
		}
	}
}
