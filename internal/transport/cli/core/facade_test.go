package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
	"github.com/localswim/localswim/internal/transport/cli/evidence"
)

type currentProvider struct {
	c     *Connection
	calls int
}

type snapshotEvidence struct {
	memoryEvidence
	afterSave func()
}

func (s *snapshotEvidence) Save(r evidence.Record) (string, error) {
	p, e := s.memoryEvidence.Save(r)
	if s.afterSave != nil {
		s.afterSave()
	}
	return p, e
}

func (p *currentProvider) NativeCurrent(context.Context) (*Connection, error) {
	p.calls++
	return p.c, nil
}

func TestFacadeSessionBindingAndLifetime(t *testing.T) {
	for _, field := range []string{"actorId", "boardId", "incarnation", "grantId"} {
		t.Run(field, func(t *testing.T) {
			closed := 0
			c := connection(t, func(*http.Request) (int, []byte, error) { t.Fatal("board dispatch before binding"); return 0, nil, nil })
			c.close = func() { closed++ }
			c.do = func(*http.Request) (*http.Response, error) {
				var s map[string]any
				_ = json.Unmarshal(fixture(t, "session-read.json"), &s)
				if field == "actorId" {
					s[field] = "00000000-0000-4000-8000-000000000099"
				} else {
					s["grant"].(map[string]any)[field] = "00000000-0000-4000-8000-000000000099"
				}
				b, _ := json.Marshal(s)
				return response(200, b), nil
			}
			if cl, e := Open(context.Background(), &provider{conn: c}, selected); cl != nil || e == nil || closed != 1 {
				t.Fatal("mismatched session accepted or leaked")
			}
		})
	}
	c := connection(t, func(*http.Request) (int, []byte, error) { return 200, []byte("binary"), nil })
	closed := 0
	c.close = func() { closed++ }
	cl, e := Open(context.Background(), &provider{conn: c}, selected)
	if e != nil {
		t.Fatal(e)
	}
	m := cl.Metadata()
	m.Selected.BoardID = testActor
	if cl.Metadata().Selected != selected {
		t.Fatal("mutable metadata")
	}
	cl.Close()
	cl.Close()
	if closed != 1 {
		t.Fatal("lifetime closed more than once")
	}
	if _, _, _, e = cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/octet-stream", 10); e == nil {
		t.Fatal("closed dispatch")
	}
}

func TestFacadeBoundedBinaryAndRouteScope(t *testing.T) {
	calls := 0
	c := connection(t, func(*http.Request) (int, []byte, error) { calls++; return 200, []byte("abcd"), nil })
	cl, e := Open(context.Background(), &provider{conn: c}, selected)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	for _, path := range []string{"https://evil.localhost:47443/api/v1/boards/" + testBoard, "/api/v1/boards/" + testActor, "/api/v1/boards/" + testBoard + "/../" + testBoard, "/api/v1/boards/" + testBoard + "?x=1", "/api/v1/session/cli-credentials", "/api/v1/boards/" + testBoard + "/operations/" + testActor} {
		if _, _, _, e := cl.Request(context.Background(), "GET", path, nil, "", "application/octet-stream", 10); e == nil {
			t.Fatal("route escape")
		}
	}
	if calls != 0 {
		t.Fatal("invalid route transmitted")
	}
	if _, _, _, e := cl.Request(context.Background(), "POST", "/api/v1/boards/"+testBoard+"/cards", strings.NewReader("{}"), "application/json", "application/json", 10); e == nil {
		t.Fatal("mutation bypass")
	}
	if _, _, _, e := cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/octet-stream", 3); e == nil {
		t.Fatal("oversize accepted")
	}
	if _, _, b, e := cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/octet-stream", 4); e != nil || !bytes.Equal(b, []byte("abcd")) {
		t.Fatal("exact bytes lost")
	}
	c.do = func(*http.Request) (*http.Response, error) {
		r := response(200, []byte("abc"))
		r.ContentLength = 4
		return r, nil
	}
	if _, _, _, e := cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/octet-stream", 4); e == nil {
		t.Fatal("truncated body accepted")
	}
	c.do = func(*http.Request) (*http.Response, error) {
		r := response(200, []byte("abc"))
		r.Header.Set("Content-Encoding", "gzip")
		return r, nil
	}
	if _, _, _, e := cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/octet-stream", 4); e == nil {
		t.Fatal("encoded body accepted")
	}
}

func TestFacadeAttachmentSnapshotEvidenceAnd201(t *testing.T) {
	original, wire := fixtureRecord(t, "attachment-add")
	raw := []byte("synthetic exact bytes")
	expectedRaw := bytes.Clone(raw)
	var v map[string]any
	_ = json.Unmarshal(wire, &v)
	input := v["input"].(map[string]any)
	h := sha256.Sum256(raw)
	input["sha256"] = hex.EncodeToString(h[:])
	input["byteLength"] = len(raw)
	wire, _ = json.Marshal(v)
	x, e := evidence.Freeze(original.Context(), original.Route(), contract.AuthenticatedActor{ActorID: testActor}, wire)
	if e != nil {
		t.Fatal(e)
	}
	terminal, e := T.Decode[T.Terminal](fixture(t, "command-attachment-add.json"))
	if e != nil {
		t.Fatal(e)
	}
	terminal.Fingerprint = A.Digest(x.Fingerprint())
	// Byte facts are the command's verified raw payload facts.
	terminal.ByteFacts[0].SHA256 = A.Digest(hex.EncodeToString(h[:]))
	terminal.ByteFacts[0].ByteLength = A.Number(len(raw))
	b, e := T.Readable(terminal)
	if e != nil {
		t.Fatal(e)
	}
	store := &snapshotEvidence{afterSave: func() { raw[0] ^= 1 }}
	calls := 0
	c := connection(t, func(req *http.Request) (int, []byte, error) {
		calls++
		if store.saved != 1 || store.loaded != 1 {
			t.Fatal("before private readback")
		}
		_, params, e := mime.ParseMediaType(req.Header.Get("Content-Type"))
		if e != nil {
			t.Fatal(e)
		}
		mr := multipart.NewReader(req.Body, params["boundary"])
		for _, name := range []string{"metadata", "file"} {
			p, e := mr.NextPart()
			if e != nil || p.FormName() != name {
				t.Fatal("multipart part")
			}
			data, _ := io.ReadAll(p)
			if name == "metadata" && !bytes.Equal(data, x.Wire()) || name == "file" && !bytes.Equal(data, expectedRaw) {
				t.Fatal("multipart bytes")
			}
		}
		if _, e := mr.NextPart(); e != io.EOF {
			t.Fatal("extra multipart part")
		}
		return 201, b, nil
	})
	cl, e := Open(context.Background(), &provider{conn: c}, selected)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	out := cl.DispatchAttachment(context.Background(), store, original.Route(), wire, raw, nil)
	if out.Exit() != 0 || calls != 1 || store.resolved != 1 {
		t.Fatal("fresh attachment not confirmed")
	}
	bad := bytes.Clone(expectedRaw)
	bad[0] ^= 1
	if o := cl.DispatchAttachment(context.Background(), store, original.Route(), wire, bad, nil); o.Exit() != 2 || calls != 1 || store.saved != 1 {
		t.Fatal("invalid bytes admitted")
	}
}

func TestFacadeNativeGrant201AndOriginalBoundary(t *testing.T) {
	original, wire := fixtureRecord(t, "grant-create")
	calls := 0
	c := connection(t, func(*http.Request) (int, []byte, error) {
		calls++
		return 201, fixture(t, "command-grant-create.json"), nil
	})
	c.native = true
	cl, e := OpenCurrent(context.Background(), &currentProvider{c: c})
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	store := &memoryEvidence{}
	out := cl.Dispatch(context.Background(), store, original.Route(), wire, nil)
	if out.Exit() != 0 || store.resolved != 1 || calls != 1 {
		t.Fatal("new native grant creation classification")
	}
	card, _ := fixtureRecord(t, "card-create")
	store.record = card
	if out := cl.Lookup(context.Background(), store, "synthetic", nil); out.Exit() != 4 || calls != 1 {
		t.Fatal("native domain outcome exposed")
	}
	store.record = original
	c.metadata.ActorID = testGrant
	if out := cl.Lookup(context.Background(), store, "synthetic", nil); out.Exit() != 4 || calls != 1 {
		t.Fatal("other actor outcome exposed")
	}
}

func TestFacadeNativeBoundariesAndEphemeralSecrets(t *testing.T) {
	c := connection(t, func(*http.Request) (int, []byte, error) { t.Fatal("unexpected ordinary path"); return 0, nil, nil })
	c.native = true
	p := &currentProvider{c: c}
	cl, e := OpenCurrent(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	defer cl.Close()
	if _, _, _, e := cl.Request(context.Background(), "GET", "/api/v1/boards/"+testBoard, nil, "", "application/json", 100); e == nil {
		t.Fatal("native board read")
	}
	_, wire := fixtureRecord(t, "card-create")
	if out := cl.Dispatch(context.Background(), &memoryEvidence{}, contract.ResolvedRoute{Command: "card.create", BoardID: testBoard}, wire, nil); out.Exit() != 2 {
		t.Fatal("native card mutation")
	}
	req := T.CodeRequest{InstallationID: T.UUIDv4(testInstallation), BoardID: A.UUID(testBoard), Incarnation: A.UUID(testInc), GrantID: A.UUID(testGrant)}
	secret := bytes.ReplaceAll(fixture(t, "credential-secret.json"), []byte("00000000-0000-4000-8000-000000000051"), []byte(testInstallation))
	called := 0
	for _, mode := range []string{"missing-no-store", "invalid", "mismatch", "callback-error", "valid"} {
		c.do = func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/v1/session/cli-credentials" {
				t.Fatal("wrong native route")
			}
			b := secret
			if mode == "invalid" {
				b = []byte(`{"token":"PRIVATE_SECRET"}`)
			}
			if mode == "mismatch" {
				b = bytes.ReplaceAll(b, []byte(testBoard), []byte(testActor))
			}
			resp := response(201, b)
			if mode != "missing-no-store" {
				resp.Header.Set("Cache-Control", "no-store")
			}
			return resp, nil
		}
		err := cl.IssueCredential(context.Background(), req, func(x T.CredentialSecret) error {
			called++
			if x.Token == "" {
				t.Fatal("missing token")
			}
			if mode == "callback-error" {
				return errors.New("PRIVATE_SECRET PRIVATE_PATH")
			}
			return nil
		})
		if mode == "valid" && err != nil || mode != "valid" && err == nil {
			t.Fatal("secret classification")
		}
		if err != nil {
			var f *Failure
			if !errors.As(err, &f) || f.Exit != 4 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unsafe secret error")
			}
		}
	}
	if called != 2 {
		t.Fatal("invalid secret callback")
	}
	if _, e := json.Marshal(cl); e == nil {
		t.Fatal("client serialized")
	}
}
