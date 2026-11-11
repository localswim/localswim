package client

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestT07PublicationAcceptanceAndUnknownResponse(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusCreated, http.StatusInternalServerError} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			w.WriteHeader(status)
			io.WriteString(w, `{"id":"00000000-0000-4000-8000-000000000001"}`)
		}))
		api, err := New(server.URL, "test-token")
		if err != nil {
			t.Fatal(err)
		}
		_, err = api.Do(http.MethodPost, "/publications", json.RawMessage(`{}`), "")
		server.Close()
		if status == http.StatusAccepted && err != nil {
			t.Fatal(err)
		}
		if status != http.StatusAccepted && !errors.Is(err, ErrMutationOutcomeUnknown) {
			t.Fatalf("status=%d error=%v", status, err)
		}
		if requests != 1 {
			t.Fatal("mutation replayed")
		}
	}
}

func TestT07PreviewRetainsOriginalBoardValidator(t *testing.T) {
	const tag = `"1234567890abcdef"`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Board-ETag", tag)
		io.WriteString(w, `{"board_id":"00000000-0000-4000-8000-000000000001","lane_id":"00000000-0000-4000-8000-000000000002","board_revision":1,"selected_card_ids":[],"current":[],"proposed":[],"changed":false}`)
	}))
	defer server.Close()
	api, err := New(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := api.Do(http.MethodGet, "/ticket-orders/00000000-0000-4000-8000-000000000002?preview=true", nil, "")
	if err != nil || reply.BoardETag != tag {
		t.Fatalf("lost board validator %v", err)
	}
}
