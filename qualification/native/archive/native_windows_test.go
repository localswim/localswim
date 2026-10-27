//go:build nativequalification && windows && amd64

package archivequalification

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	V "github.com/localswim/localswim/internal/archive/validate"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	L "github.com/localswim/localswim/internal/transport/local"
)

var required = []string{"V03-01-readable-consistent-export", "V03-02-restored-local-bootstrap", "V03-03-refusal-no-overwrite", "V03-04-interrupted-publication-status", "V03-05-original-unknown-preserved", "V03-06-source-unchanged"}

func id() A.UUID {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic("random unavailable")
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
func port(t *testing.T) int {
	t.Helper()
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal("loopback unavailable")
	}
	p := l.Addr().(*net.TCPAddr).Port
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	return p
}
func wire(t *testing.T, info L.ClientInfo, card string, rev, cr int, input any) []byte {
	t.Helper()
	var target, cv any
	if card != "" {
		target = map[string]string{"cardId": card}
		cv = cr
	}
	b, e := json.Marshal(map[string]any{"incarnation": info.Incarnation, "opId": id(), "target": target, "expectedRevision": rev, "expectedCardRevision": cv, "input": input})
	if e != nil {
		t.Fatal(e)
	}
	return b
}

type reply struct {
	code    int
	raw     []byte
	cookies []*http.Cookie
}

func call(t *testing.T, r *L.Runtime, info L.ClientInfo, method, path string, b []byte, cookie *http.Cookie, media string) reply {
	t.Helper()
	q, e := http.NewRequest(method, r.Origin()+path, bytes.NewReader(b))
	if e != nil {
		t.Fatal(e)
	}
	if b != nil {
		if media == "" {
			media = "application/json"
		}
		q.Header.Set("Content-Type", media)
	}
	if cookie == nil {
		q.Header.Set("Authorization", "Bearer "+string(info.Token))
	} else {
		q.AddCookie(cookie)
		q.Header.Set("Origin", r.Origin())
		q.Header.Set("X-Localswim-Client", "browser")
	}
	c := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer c.CloseIdleConnections()
	res, e := c.Do(q)
	if e != nil {
		t.Fatal("local HTTP request failed")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 160000000))
	if e != nil {
		t.Fatal("response read failed")
	}
	return reply{res.StatusCode, raw, res.Cookies()}
}
func terminal(t *testing.T, r reply) T.Terminal {
	t.Helper()
	v, e := T.Decode[T.Terminal](r.raw)
	if e != nil || v.Outcome != "committed" {
		t.Fatalf("mutation did not commit: HTTP %d", r.code)
	}
	return v
}
func ref(t *testing.T, v T.Terminal, kind string) string {
	t.Helper()
	for _, r := range v.ResultRefs {
		if string(r.Kind) == kind {
			return string(r.ID)
		}
	}
	t.Fatal("missing result reference")
	return ""
}
func snapshot(t *testing.T, r *L.Runtime, i L.ClientInfo) T.BoardRead {
	t.Helper()
	v := call(t, r, i, "GET", "/api/v1/boards/"+i.BoardID, nil, nil, "")
	b, e := T.Decode[T.BoardRead](v.raw)
	if v.code != 200 || e != nil {
		t.Fatal("board read failed")
	}
	return b
}
func attach(t *testing.T, r *L.Runtime, i L.ClientInfo, card string, rev int, data []byte) string {
	t.Helper()
	meta := wire(t, i, card, rev, 1, map[string]any{"name": "synthetic.bin", "mediaLabel": "application/octet-stream", "byteLength": len(data), "sha256": fmt.Sprintf("%x", sha256.Sum256(data))})
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	for _, p := range []struct {
		name, media string
		data        []byte
	}{{"metadata", "application/json", meta}, {"file", "application/octet-stream", data}} {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+p.name+`"`)
		h.Set("Content-Type", p.media)
		w, e := m.CreatePart(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(p.data); e != nil {
			t.Fatal(e)
		}
	}
	if e := m.Close(); e != nil {
		t.Fatal(e)
	}
	return ref(t, terminal(t, call(t, r, i, "POST", "/api/v1/boards/"+i.BoardID+"/cards/"+card+"/attachments", b.Bytes(), nil, m.FormDataContentType())), "attachment")
}
func hashTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = "directory"
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected link")
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		out[rel] = fmt.Sprintf("%x", sha256.Sum256(b))
		return nil
	})
	if e != nil {
		t.Fatal("inventory read failed")
	}
	return out
}
func productInventory(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range []string{"internal", "cmd"} {
		for path, hash := range hashTree(t, filepath.Join(repo, dir)) {
			out[dir+"/"+filepath.ToSlash(path)] = hash
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		raw, e := os.ReadFile(filepath.Join(repo, name))
		if e != nil {
			t.Fatal("module inventory unavailable")
		}
		out[name] = fmt.Sprintf("%x", sha256.Sum256(raw))
	}
	return out
}
func save(t *testing.T, p string, b []byte) {
	t.Helper()
	f, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, e = f.Write(b)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Sync(); e != nil {
		t.Fatal(e)
	}
	if e = f.Close(); e != nil {
		t.Fatal(e)
	}
}

func TestV03LocalArchive(t *testing.T) {
	if runtime.Version() != "go1.27.3" {
		t.Fatal("pinned Go 1.27.3 required")
	}
	root := os.Getenv("V03_NATIVE_ROOT")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		t.Fatal("explicit canonical private V03_NATIVE_ROOT required")
	}
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(root)+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("fixture root must be outside public repository")
	}
	inv, e := os.ReadFile("inventory.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(inv, &manifest) != nil || !reflect.DeepEqual(required, manifest.Required) {
		t.Fatal("frozen required inventory mismatch")
	}
	attempt := privateAttempt(t, root)
	save(t, filepath.Join(attempt, "inventory-frozen.json"), inv)
	providerBefore := productInventory(t, repo)
	providerProof, _ := json.MarshalIndent(providerBefore, "", "  ")
	save(t, filepath.Join(attempt, "provider-hashes-before.json"), providerProof)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	source := filepath.Join(attempt, "src")
	r, e := L.Start(ctx, L.Options{DataDir: source, Port: port(t)})
	if e != nil {
		t.Fatal("normal source startup failed")
	}
	defer r.Close()
	info, e := L.ReadClient(source)
	if e != nil {
		t.Fatal("normal bot metadata unavailable")
	}
	base := "/api/v1/boards/" + info.BoardID
	blobs := [][]byte{{0, 255, 1, 'V', '0', '3', '\n'}, {}}
	for n, data := range blobs {
		card := ref(t, terminal(t, call(t, r, info, "POST", base+"/cards", wire(t, info, "", 2*n, 0, map[string]any{"title": fmt.Sprint("Synthetic ", n), "description": "Readable backup"}), nil, "")), "card")
		attach(t, r, info, card, 2*n+1, data)
	}
	// Assign through the actual typed HTTP edit and retain an assigned revision
	// before the final snapshot. The historical actor selector is data, not a grant.
	assignmentBase := snapshot(t, r, info)
	assignedCard := assignmentBase.Result.Cards[0]
	terminal(t, call(t, r, info, "PATCH", base+"/cards/"+string(assignedCard.CardID), wire(t, info, string(assignedCard.CardID), int(assignmentBase.Result.Revision), int(assignedCard.Revision), map[string]any{"ownerId": info.ActorID}), nil, ""))
	assignedState := snapshot(t, r, info)
	for _, card := range assignedState.Result.Cards {
		if card.CardID == assignedCard.CardID {
			assignedCard = card
		}
	}
	terminal(t, call(t, r, info, "PATCH", base+"/cards/"+string(assignedCard.CardID), wire(t, info, string(assignedCard.CardID), int(assignedState.Result.Revision), int(assignedCard.Revision), map[string]any{"description": "Readable backup with assigned owner"}), nil, ""))
	before := snapshot(t, r, info)
	body, _ := json.Marshal(map[string]any{"incarnation": info.Incarnation, "expectedRevision": before.Result.Revision, "input": map[string]any{}})
	out := call(t, r, info, "POST", base+"/exports", body, nil, "")
	if out.code != 200 {
		t.Fatal("actual local export unavailable")
	}
	input := filepath.Join(attempt, "input")
	if e = os.Mkdir(input, 0700); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(input, "backup.zip")
	save(t, archive, out.raw)
	// A later acknowledged mutation must never appear in the already exported snapshot.
	terminal(t, call(t, r, info, "POST", base+"/cards", wire(t, info, "", int(before.Result.Revision), 0, map[string]any{"title": "After snapshot", "description": "Not in backup"}), nil, ""))
	unknownPath := base + "/operations/" + string(id()) + "?incarnation=" + info.Incarnation + "&fingerprintVersion=1&fingerprint=" + strings.Repeat("a", 64)
	unknown := call(t, r, info, "GET", unknownPath, nil, nil, "")
	u, e := T.Decode[T.KnownIdentityUnknown](unknown.raw)
	if e != nil || u.Outcome != "unknown" {
		t.Fatal("original absent evidence did not stay UNKNOWN")
	}
	save(t, filepath.Join(attempt, "original-unknown.json"), unknown.raw)
	if e = r.Close(); e != nil {
		t.Fatal("source close failed")
	}
	sourceHashes := hashTree(t, source)
	inputHashes := hashTree(t, input)
	completed := 0
	run := func(n int, f func(*testing.T)) { t.Run(required[n], f); completed++ }
	run(0, func(t *testing.T) {
		p, e := V.Read(ctx, bytes.NewReader(out.raw))
		if e != nil {
			t.Fatal("export validation failed")
		}
		m, e := p.Manifest()
		if e != nil || m.Revision != before.Result.Revision {
			t.Fatal("snapshot revision changed")
		}
		z, e := zip.NewReader(bytes.NewReader(out.raw), int64(len(out.raw)))
		if e != nil {
			t.Fatal(e)
		}
		found := map[string][]byte{}
		for _, f := range z.File {
			q, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(q)
			q.Close()
			if e != nil {
				t.Fatal(e)
			}
			found[f.Name] = b
			if bytes.Contains(b, []byte(info.Token)) {
				t.Fatal("credential exported")
			}
		}
		if !bytes.Contains(found["board.json"], []byte("\n  ")) || !bytes.Contains(found["board.json"], []byte("Readable backup")) || bytes.Contains(found["board.json"], []byte("After snapshot")) {
			t.Fatal("readable consistent board projection failed")
		}
		exported, e := A.DecodeReadable[A.Board](found["board.json"])
		if e != nil {
			t.Fatal("readable board decode failed")
		}
		assigned := false
		for _, card := range exported.Cards {
			if card.CardID == assignedCard.CardID && card.OwnerID != nil && string(*card.OwnerID) == info.ActorID {
				assigned = true
			}
		}
		if !assigned {
			t.Fatal("readable export lost current assigned historical actor")
		}
		history, e := A.DecodeReadable[A.History](found["history.json"])
		if e != nil {
			t.Fatal("readable history decode failed")
		}
		ownedHistory := false
		for _, record := range history.Records {
			if record.CardID == assignedCard.CardID && record.State != nil && record.State.OwnerID != nil && string(*record.State.OwnerID) == info.ActorID {
				ownedHistory = true
			}
		}
		if !ownedHistory {
			t.Fatal("readable retained history lost owner change")
		}
		for _, data := range blobs {
			key := fmt.Sprintf("blobs/sha256-%x", sha256.Sum256(data))
			b, ok := found[key]
			if !ok || !bytes.Equal(b, data) {
				t.Fatal("attachment including zero bytes lost")
			}
		}
	})
	// Adapter/bootstrap cases below deliberately require the normal product path.
	run(1, func(t *testing.T) { testRestored(t, ctx, attempt, archive, source, info, before, blobs) })
	run(2, func(t *testing.T) { testRefusals(t, ctx, attempt, archive, source) })
	run(3, func(t *testing.T) { testInterrupted(t, ctx, attempt, archive, source) })
	run(4, func(t *testing.T) { testUnknown(t, ctx, attempt, archive, source, info, unknownPath, unknown.raw) })
	run(5, func(t *testing.T) {
		if !reflect.DeepEqual(providerBefore, productInventory(t, repo)) {
			t.Fatal("production providers changed during qualification")
		}
		if !reflect.DeepEqual(sourceHashes, hashTree(t, source)) {
			t.Fatal("source or archive changed")
		}
		currentInput := hashTree(t, input)
		for path, hash := range inputHashes {
			if currentInput[path] != hash {
				t.Fatal("original input archive changed")
			}
		}
		for path := range currentInput {
			if _, original := inputHashes[path]; !original && path != "corrupt.zip" && path != "unsafe.zip" {
				t.Fatal("unexpected input fixture addition")
			}
		}
		b, _ := json.MarshalIndent(map[string]any{"source": sourceHashes, "input": inputHashes}, "", "  ")
		save(t, filepath.Join(attempt, "source-hashes.json"), b)
	})
	if completed != len(required) {
		t.Fatal("required cases missing")
	}
}
