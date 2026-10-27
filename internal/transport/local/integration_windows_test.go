//go:build windows && amd64

package local

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"sync"
	"testing"

	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestLocalAttachmentExportLookupAndCAS(t *testing.T) {
	r, root := startTest(t)
	info, _ := ReadClient(root)
	base := "/api/v1/boards/" + info.BoardID
	wire := mutationWire(t, r, "", 0, 0, map[string]any{"title": "Synthetic attachment", "description": "Persist bytes"})
	created := term(t, request(t, r, "POST", base+"/cards", wire, info.Token, nil, nil), "committed")
	replay := term(t, request(t, r, "POST", base+"/cards", wire, info.Token, nil, nil), "committed")
	if created.OpID != replay.OpID || *created.Revision != *replay.Revision {
		t.Fatal("immutable operation replay changed")
	}
	lookup := base + "/operations/" + string(created.OpID) + "?incarnation=" + info.Incarnation + "&fingerprintVersion=1&fingerprint=" + string(created.Fingerprint)
	got := term(t, request(t, r, "GET", lookup, nil, info.Token, nil, nil), "committed")
	if got.OpID != created.OpID {
		t.Fatal("original lookup")
	}
	card := ""
	for _, ref := range created.ResultRefs {
		if ref.Kind == "card" {
			card = string(ref.ID)
		}
	}
	data := []byte{0, 1, 255, 8, 'L', '0', '2', '\n'}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	meta := mutationWire(t, r, card, 1, 1, map[string]any{"name": "synthetic.bin", "mediaLabel": "application/octet-stream", "byteLength": len(data), "sha256": digest})
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	for _, part := range []struct {
		name, media string
		data        []byte
	}{{"metadata", "application/json", meta}, {"file", "application/octet-stream", data}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+part.name+`"`)
		h.Set("Content-Type", part.media)
		w, e := mp.CreatePart(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(part.data); e != nil {
			t.Fatal(e)
		}
	}
	if e := mp.Close(); e != nil {
		t.Fatal(e)
	}
	attached := term(t, request(t, r, "POST", base+"/cards/"+card+"/attachments", body.Bytes(), info.Token, nil, func(q *http.Request) { q.Header.Set("Content-Type", mp.FormDataContentType()) }), "committed")
	attachment := ""
	for _, ref := range attached.ResultRefs {
		if ref.Kind == "attachment" {
			attachment = string(ref.ID)
		}
	}
	if attachment == "" {
		t.Fatal("attachment reference")
	}
	out := request(t, r, "GET", base+"/attachments/"+attachment, nil, info.Token, nil, nil)
	if out.status != 200 || !bytes.Equal(out.b, data) {
		t.Fatal("attachment bytes")
	}
	exportBody, _ := json.Marshal(map[string]any{"incarnation": info.Incarnation, "expectedRevision": 2, "input": map[string]any{}})
	out = request(t, r, "POST", base+"/exports", exportBody, info.Token, nil, nil)
	if out.status != 200 {
		t.Fatalf("export status %d", out.status)
	}
	archive, e := zip.NewReader(bytes.NewReader(out.b), int64(len(out.b)))
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, file := range archive.File {
		f, e := file.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(f)
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Equal(b, data) {
			found = true
		}
		if bytes.Contains(b, []byte(info.Token)) {
			t.Fatal("export leaked bot token")
		}
	}
	if !found {
		t.Fatal("export omitted exact BLOB bytes")
	}
	// Competing distinct operations at one board revision cannot both commit.
	var wg sync.WaitGroup
	outs := make(chan response, 2)
	for j := 0; j < 2; j++ {
		b := mutationWire(t, r, "", 2, 0, map[string]any{"title": fmt.Sprint("CAS ", j), "description": ""})
		wg.Add(1)
		go func() { defer wg.Done(); outs <- request(t, r, "POST", base+"/cards", b, info.Token, nil, nil) }()
	}
	wg.Wait()
	close(outs)
	committed, conflicts := 0, 0
	for out := range outs {
		v, e := T.Decode[T.Terminal](out.b)
		if e != nil {
			t.Fatal("CAS terminal contract")
		}
		if v.Outcome == "committed" {
			committed++
		} else if v.Code == "REVISION_CONFLICT" {
			conflicts++
		}
	}
	if committed != 1 || conflicts != 1 {
		t.Fatalf("CAS committed=%d conflicts=%d", committed, conflicts)
	}
}
