package v2

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestM86ZIPRestoreOutlivesOrdinaryTCPDeadlines(t *testing.T) {
	app, editor, source, _ := backupCaptureFixture(t)
	_, archive := captureRecoveryBackup(t, app, source)
	staging := t.TempDir()
	t.Setenv("TMP", staging)
	t.Setenv("TMPDIR", staging)

	const ordinary = 250 * time.Millisecond
	server := httptest.NewUnstartedServer(nil)
	server.Config.ReadHeaderTimeout = 2 * time.Second
	server.Config.ReadTimeout = ordinary
	server.Config.WriteTimeout = ordinary
	handler := app.Handler(server.Listener.Addr().(*net.TCPAddr))
	finished := make(chan struct{}, 1)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		finished <- struct{}{}
	})
	server.Start()
	defer server.Close()

	tag := snapshotRepresentationTag(source)
	durable := m96FileDigest(t, app.store.snapshotPath(source.Board.ID))
	for _, test := range []struct {
		name string
		body []byte
		want int
	}{
		{"corrupt", archive[:len(archive)/2], http.StatusBadRequest},
		{"verified", archive, http.StatusOK},
	} {
		if !t.Run(test.name, func(t *testing.T) {
			conn, err := net.DialTimeout("tcp", server.Listener.Addr().String(), 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			_, err = fmt.Fprintf(conn, "PUT %s/snapshots/%s HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n"+
				"Content-Type: application/zip\r\nContent-Length: %d\r\nIf-Match: %s\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n",
				Prefix, source.Board.ID, server.Listener.Addr(), editor.token, len(test.body), tag)
			if err != nil {
				t.Fatal(err)
			}
			reader := bufio.NewReader(conn)
			request := &http.Request{Method: http.MethodPut}
			continueReply, err := http.ReadResponse(reader, request)
			if err != nil || continueReply.StatusCode != http.StatusContinue {
				t.Fatalf("server did not begin ZIP upload: %v", err)
			}
			_ = continueReply.Body.Close()
			// No body bytes arrive until both ordinary server deadlines pass.
			time.Sleep(2 * ordinary)
			if test.want == http.StatusOK {
				// Delay the admitted transaction after body transfer as well.
				app.store.mu.Lock()
			}
			_, err = conn.Write(test.body)
			if test.want == http.StatusOK {
				time.Sleep(2 * ordinary)
				app.store.mu.Unlock()
			}
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.ReadResponse(reader, request)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != test.want {
				t.Fatalf("restore status=%d want=%d, read error=%v", response.StatusCode, test.want, err)
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("restore handler did not finish")
			}
			files, err := os.ReadDir(staging)
			if err != nil || len(files) != 0 {
				t.Fatalf("restore staging was not cleaned: %v", err)
			}
			state, ok := app.store.accepted(source.Board.ID)
			if !ok {
				t.Fatal("board missing after restore")
			}
			if test.want == http.StatusBadRequest {
				if !reflect.DeepEqual(state, source) || m96FileDigest(t, app.store.snapshotPath(source.Board.ID)) != durable {
					t.Fatal("corrupt delayed upload changed board state")
				}
			} else if state.Board.Revision != source.Board.Revision+1 || response.Header.Get("X-Board-ETag") != state.Board.entityTag() {
				t.Fatal("delayed upload did not confirm the single committed revision")
			}
		}) {
			return
		}
	}
}
