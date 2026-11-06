package core

import (
	"context"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
	"io"
	"net/http"
	"testing"
)

type fileCapReader struct {
	remaining int64
	reads     int
}

func (r *fileCapReader) Read(p []byte) (int, error) {
	r.reads++
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	clear(p[:n])
	r.remaining -= n
	return int(n), nil
}
func TestFileReadTransportHardCap(t *testing.T) {
	for _, declared := range []bool{false, true} {
		for _, extra := range []int64{0, 1} {
			reader := &fileCapReader{remaining: T.FileReadResponseLimit + extra}
			c := &Connection{context: evidence.Context{Origin: "https://fixture.localhost:47443"}, do: func(*http.Request) (*http.Response, error) {
				length := int64(-1)
				if declared {
					length = T.FileReadResponseLimit + extra
				}
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(reader), ContentLength: length}, nil
			}}
			_, _, b, e := c.requestRaw(context.Background(), "GET", "/api/v1/boards/"+testBoard+"/cards/"+testActor+"/attachments", nil, "", "application/json", T.FileReadResponseLimit)
			if extra == 0 && (e != nil || len(b) != T.FileReadResponseLimit) {
				t.Fatal("exact cap refused", e)
			}
			if extra == 1 && (e == nil || len(b) != 0) {
				t.Fatal("over cap accepted")
			}
		}
	}
}
