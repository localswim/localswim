//go:build windows && amd64

package validate

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	E "github.com/localswim/localswim/internal/archive/export"
	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

func TestNativeActualA02Output(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	parent := t.TempDir()
	if root := os.Getenv("A03_TEST_ROOT"); root != "" {
		if !filepath.IsAbs(root) {
			t.Fatal("absolute synthetic fixture root required")
		}
		var e error
		parent, e = os.MkdirTemp(root, "native-")
		if e != nil {
			t.Fatal(e)
		}
	}
	token, e := windows.OpenCurrentProcessToken()
	if e != nil {
		t.Fatal(e)
	}
	defer token.Close()
	u, e := token.GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + u.User.Sid.String() + "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")(A;OICI;FA;;;SY)")
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "source")
	if e = windows.CreateDirectory(windows.StringToUTF16Ptr(dir), &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); e != nil {
		t.Fatal(e)
	}
	id := A.Identity{BoardID: "10000000-0000-0000-0000-000000000001", Incarnation: "20000000-0000-0000-0000-000000000001"}
	s, e := S.Create(ctx, dir, schema.Identity{BoardID: string(id.BoardID), Incarnation: string(id.Incarnation)})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := s.Close(); e != nil {
			t.Error(e)
		}
	}()
	const actor = "30000000-0000-0000-0000-000000000001"
	const grant = "30000000-0000-0000-0000-000000000002"
	l, e := s.Writer(ctx)
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(ctx, func(c *sql.Conn) error {
		if _, e := c.ExecContext(ctx, "INSERT INTO actor VALUES(?,?,?,?,?,?)", string(id.Incarnation), actor, "human", "Synthetic export validator fixture", string(id.BoardID), string(id.Incarnation)); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO grant_fact VALUES(?,?)", string(id.Incarnation), grant); e != nil {
			return e
		}
		if _, e := c.ExecContext(ctx, "INSERT INTO grant_record VALUES(?,?,?,1)", string(id.Incarnation), grant, actor); e != nil {
			return e
		}
		for _, r := range []string{"read", "export"} {
			if _, e := c.ExecContext(ctx, "INSERT INTO grant_right VALUES(?,?,?)", string(id.Incarnation), grant, r); e != nil {
				return e
			}
		}
		return nil
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	const installation = "60000000-0000-4000-8000-000000000001"
	const origin = "https://synthetic.localhost:47443"
	g, e := grants.New(installation, origin, func(ctx context.Context, r writer.Read, n time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: string(id.BoardID), Incarnation: string(id.Incarnation), ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: n.Add(-time.Minute), ExpiresAt: n.Add(time.Hour)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := E.Prepare(ctx, s, E.Request{Identity: id, Revision: 0, ExportedAt: "2026-10-26T12:00:00Z", Verify: func(ctx context.Context, r writer.Read) error {
		return g.Require(ctx, r, string(id.BoardID), string(id.Incarnation), actor, "read", "export")
	}})
	if e != nil {
		t.Fatal(e)
	}
	var output bytes.Buffer
	if e = prepared.Write(ctx, &output); e != nil {
		t.Fatal(e)
	}
	raw := output.Bytes()
	p, e := Read(ctx, bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	if p.ManifestSHA256() != prepared.ManifestSHA256() || p.ByteLength() != prepared.ByteLength() {
		t.Fatal("A02/A03 metadata mismatch")
	}
	m, e := p.Manifest()
	if e != nil || m.Source != id || m.Revision != 0 {
		t.Fatal("source identity")
	}
	// Preserve the exact generated package when the private evidence destination
	// is explicitly supplied. This is synthetic fixture evidence, not publication.
	if dst := os.Getenv("A03_EVIDENCE_DIR"); dst != "" {
		if e = os.WriteFile(filepath.Join(dst, "actual-a02.zip"), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[14] ^= 1 }, func(b []byte) { b[30] = 'M' }, func(b []byte) { put16(b, 6, 1) }} {
		bad := bytes.Clone(raw)
		mutate(bad)
		requireReject(t, bad)
	}
	if _, e = os.Stat(filepath.Join(parent, "destination")); !os.IsNotExist(e) {
		t.Fatal("validator created destination")
	}
	var again bytes.Buffer
	if e = prepared.Write(ctx, &again); e != nil || !bytes.Equal(raw, again.Bytes()) {
		t.Fatal("validator altered A02 source")
	}
}
