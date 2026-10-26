//go:build windows && amd64

package publication

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	E "github.com/localswim/localswim/internal/archive/export"
	R "github.com/localswim/localswim/internal/archive/restore"
	V "github.com/localswim/localswim/internal/archive/validate"
	"github.com/localswim/localswim/internal/board/grants"
	A "github.com/localswim/localswim/internal/contract/archive"
	S "github.com/localswim/localswim/internal/store/open"
	"github.com/localswim/localswim/internal/store/schema"
	"github.com/localswim/localswim/internal/store/writer"
	"golang.org/x/sys/windows"
)

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	return c
}
func uuid(t *testing.T) A.UUID {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	return A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
func nativeRoot(t *testing.T) (*native, string) {
	t.Helper()
	root := os.Getenv("A04_TEST_ROOT")
	if root == "" {
		t.Skip("actual native restore requires private A04_TEST_ROOT")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("private absolute fixture root required")
	}
	wd, _ := os.Getwd()
	repo := filepath.Clean(filepath.Join(wd, "../../.."))
	if strings.HasPrefix(strings.ToLower(filepath.Clean(root))+`\`, strings.ToLower(repo)+`\`) {
		t.Fatal("native fixtures must remain outside public repository")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	n, e := newNative()
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, string(uuid(t))[:8])
	if e = n.mkdir(path); e != nil {
		t.Fatal(e)
	}
	return n, path
}
func fixtureZIP(t *testing.T) []byte {
	t.Helper()
	root := filepath.Join("..", "..", "..", "fixtures", "contracts", "archive-recovery", "package")
	names := []string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json"}
	blobs, e := filepath.Glob(filepath.Join(root, "blobs", "*"))
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range blobs {
		names = append(names, "blobs/"+filepath.Base(p))
	}
	names = append(names, "COMPLETE")
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range names {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if e != nil {
			t.Fatal(e)
		}
		w, e := z.CreateRaw(&zip.FileHeader{Name: name, Method: zip.Store, ReaderVersion: 20, CreatorVersion: 20, CRC32: crc32.ChecksumIEEE(b), CompressedSize64: uint64(len(b)), UncompressedSize64: uint64(len(b))})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

// actualSource builds one native synthetic fixture then obtains its source ZIP
// through the real A02 authenticated export path. No native installation or real
// human authority is used. Destination rights must remain empty after restore.
func actualSource(t *testing.T) (*native, Request, []byte) {
	t.Helper()
	n, root := nativeRoot(t)
	sourceData := filepath.Join(root, "source")
	input := filepath.Join(root, "input")
	output := filepath.Join(root, "output")
	for _, p := range []string{sourceData, input, output} {
		if e := n.mkdir(p); e != nil {
			t.Fatal(e)
		}
	}
	packageSource, e := V.Read(ctx(t), bytes.NewReader(fixtureZIP(t)))
	if e != nil {
		t.Fatal(e)
	}
	prepared, e := R.Prepare(ctx(t), packageSource, stamp(time.Now))
	if e != nil {
		t.Fatal(e)
	}
	if e = prepared.Construct(ctx(t), sourceData); e != nil {
		t.Fatal("synthetic source construction", e)
	}
	id := prepared.Identity()
	s, e := S.Existing(ctx(t), sourceData, schema.Identity{BoardID: string(id.BoardID), Incarnation: string(id.Incarnation)})
	if e != nil {
		t.Fatal(e)
	}
	actor := "00000000-0000-4000-8000-00000000000a"
	grant := string(uuid(t))
	installation := "60000000-0000-4000-8000-000000000001"
	origin := "https://synthetic.localhost:47443"
	l, e := s.Writer(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	e = l.Do(ctx(t), func(c *sql.Conn) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{{"INSERT INTO grant_fact VALUES(?,?)", []any{id.Incarnation, grant}}, {"INSERT INTO grant_record VALUES(?,?,?,1)", []any{id.Incarnation, grant, actor}}, {"INSERT INTO grant_right VALUES(?,?,'read')", []any{id.Incarnation, grant}}, {"INSERT INTO grant_right VALUES(?,?,'export')", []any{id.Incarnation, grant}}} {
			if _, e := c.ExecContext(ctx(t), q.sql, q.args...); e != nil {
				return e
			}
		}
		return nil
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	g, e := grants.New(installation, origin, func(context.Context, writer.Read, time.Time) (grants.Credential, error) {
		return grants.Credential{InstallationID: installation, Origin: origin, BoardID: string(id.BoardID), Incarnation: string(id.Incarnation), ActorID: actor, GrantID: grant, Mode: "cli", IssuedAt: time.Now().Add(-time.Minute), ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	m, _ := packageSource.Manifest()
	exported, e := E.Prepare(ctx(t), s, E.Request{Identity: id, Revision: m.Revision, ExportedAt: stamp(time.Now), Verify: func(c context.Context, r writer.Read) error {
		return g.Require(c, r, string(id.BoardID), string(id.Incarnation), actor, "read", "export")
	}})
	if e != nil {
		t.Fatal(e)
	}
	var raw bytes.Buffer
	if e = exported.Write(ctx(t), &raw); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(input, "source.zip")
	if e = n.write(source, raw.Bytes()); e != nil {
		t.Fatal(e)
	}
	return n, Request{SourceArchive: source, Destination: filepath.Join(output, "restored"), RequestID: uuid(t), ProtectedRoots: []string{sourceData}}, raw.Bytes()
}

func TestNativeRestoreStatusSourcePreservationAndIsolation(t *testing.T) {
	n, q, before := actualSource(t)
	result := Restore(ctx(t), q)
	if result.Exit != 0 || result.Status.Outcome != "confirmed" {
		r, _ := n.receipt(q.Destination + ".partial-" + string(q.RequestID))
		validation := R.ValidateNative(ctx(t), filepath.Join(q.Destination+".partial-"+string(q.RequestID), "data"), r)
		t.Fatalf("native restore failed: exit=%d phase=%s validation=%v", result.Exit, r.Phase, validation)
	}
	if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 0 {
		t.Fatalf("native status exit=%d", got.Exit)
	}
	after, e := os.ReadFile(q.SourceArchive)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("source archive changed")
	}
	r, e := n.receipt(q.Destination)
	if e != nil || r.Phase != "published" || r.Result != "complete" || r.TargetInventory == nil {
		t.Fatal("completion receipt", e)
	}
	if e = R.ValidateNative(ctx(t), filepath.Join(q.Destination, "data"), r); e != nil {
		t.Fatal("native destination verification", e)
	}
	if r.Destination.Incarnation == r.SourceManifest.Source.Incarnation {
		t.Fatal("source incarnation adopted")
	}
	// Real S02 lookup at the new destination must never adopt archived source
	// receipts, even with their exact actor/op/fingerprint evidence available.
	s, e := S.Existing(ctx(t), filepath.Join(q.Destination, "data"), schema.Identity{BoardID: string(r.Destination.BoardID), Incarnation: string(r.Destination.Incarnation)})
	if e != nil {
		t.Fatal(e)
	}
	l, e := s.Reader(ctx(t))
	if e != nil {
		t.Fatal(e)
	}
	var key writer.LookupIdentity
	e = l.Do(ctx(t), func(c *sql.Conn) error {
		return c.QueryRowContext(ctx(t), "SELECT origin_board,origin_incarnation,op_id,actor_id,fingerprint_version,fingerprint FROM operation WHERE current_ledger=0 LIMIT 1").Scan(&key.BoardID, &key.Incarnation, &key.OpID, &key.ActorID, &key.FingerprintVersion, &key.Fingerprint)
	})
	ce := l.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
	called := false
	lookup := writer.New(s).Lookup(ctx(t), key, func(context.Context, writer.Read, writer.AuthorityRequest, writer.Purpose) error {
		called = true
		return errors.New("synthetic destination authority denied")
	})
	if lookup.Terminal != nil || lookup.Unknown == nil || lookup.Unknown.Error.Code != "SOURCE_AUTHORITY_UNAVAILABLE" || called {
		t.Fatal("source evidence settled original outcome at destination")
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	if got := Restore(ctx(t), q); got.Exit != 4 {
		t.Fatal("existing destination replaced")
	}
	if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 0 {
		t.Fatal("repeat attempt damaged prior confirmation")
	}
	if e = n.mkdir(q.Destination + ".partial-" + string(q.RequestID)); e != nil {
		t.Fatal(e)
	}
	if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 4 {
		t.Fatal("both locations ambiguity confirmed")
	}
}

func TestNativeInterruptionAndFlushNeverInferCompletion(t *testing.T) {
	for _, stage := range []string{"file-flush", "file-readback", "after-preparing", "after-construct", "after-prepared", "before-rename", "after-rename"} {
		t.Run(stage, func(t *testing.T) {
			_, q, before := actualSource(t)
			got := restoreWithFault(ctx(t), q, time.Now, func(s string) error {
				if s == stage {
					return errors.New("synthetic interruption")
				}
				return nil
			})
			if got.Exit != 4 {
				t.Fatalf("interruption classified exit=%d", got.Exit)
			}
			if got = Status(ctx(t), q.Destination, q.RequestID); got.Exit != 4 {
				t.Fatal("incomplete candidate settled")
			}
			after, _ := os.ReadFile(q.SourceArchive)
			if !bytes.Equal(before, after) {
				t.Fatal("interruption changed source")
			}
			if got = Restore(ctx(t), q); got.Exit != 4 {
				t.Fatal("candidate automatically retried")
			}
		})
	}
}

func TestNativeVerifiedPrepublicationFailureAndMissingStatus(t *testing.T) {
	_, q, _ := actualSource(t)
	got := restoreWithFault(ctx(t), q, time.Now, func(s string) error {
		if s == "construction-error" {
			return errors.New("synthetic returned preparation error")
		}
		return nil
	})
	if got.Exit != 1 {
		t.Fatalf("definitive prepublication failure exit=%d", got.Exit)
	}
	if got = Status(ctx(t), q.Destination, q.RequestID); got.Exit != 1 {
		t.Fatal("failed receipt not reconciled")
	}
	if got = Status(ctx(t), q.Destination, uuid(t)); got.Exit != 4 {
		t.Fatal("missing receipt means failure")
	}
}

func TestNativeReceiptAndDestinationCorruptionStayUnknown(t *testing.T) {
	for _, damage := range []string{"wrong-request", "receipt-hash", "anchor-hash", "missing-database", "missing-lock", "missing-sibling", "wrong-binding", "extra-file", "activated-grant", "lineage", "logical-bytes", "hardlink-receipt"} {
		t.Run(damage, func(t *testing.T) {
			n, q, _ := actualSource(t)
			if got := Restore(ctx(t), q); got.Exit != 0 {
				t.Fatal("restore", got.Exit)
			}
			r, e := n.receipt(q.Destination)
			if e != nil {
				t.Fatal(e)
			}
			switch damage {
			case "wrong-request", "receipt-hash", "anchor-hash":
				if damage == "wrong-request" {
					r.RequestID = uuid(t)
				} else if damage == "receipt-hash" {
					r.SourceManifestSHA256 = A.Digest(strings.Repeat("0", 64))
				} else {
					r.TargetInventory.SHA256 = A.Digest(strings.Repeat("0", 64))
				}
				raw, e := A.Readable(r)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(filepath.Join(q.Destination, receiptName), raw, 0600); e != nil {
					t.Fatal(e)
				}
			case "missing-database":
				if e = os.Remove(filepath.Join(q.Destination, "data", "board.sqlite")); e != nil {
					t.Fatal(e)
				}
			case "missing-lock":
				if e = os.Remove(filepath.Join(q.Destination, "data", "board.lock")); e != nil {
					t.Fatal(e)
				}
			case "missing-sibling":
				if e = os.Remove(leasePath(q.Destination, q.RequestID)); e != nil {
					t.Fatal(e)
				}
			case "wrong-binding":
				if e = os.WriteFile(leasePath(q.Destination, q.RequestID), []byte(strings.Repeat("0", 64)+"\n"), 0600); e != nil {
					t.Fatal(e)
				}
			case "hardlink-receipt":
				if e = windows.CreateHardLink(pointer(filepath.Join(filepath.Dir(q.Destination), "receipt-alias")), pointer(filepath.Join(q.Destination, receiptName)), 0); e != nil {
					t.Fatal(e)
				}
			case "extra-file":
				if e = n.write(filepath.Join(q.Destination, "unexpected"), []byte("x")); e != nil {
					t.Fatal(e)
				}
			case "activated-grant", "lineage", "logical-bytes":
				s, e := S.Existing(ctx(t), filepath.Join(q.Destination, "data"), schema.Identity{BoardID: string(r.Destination.BoardID), Incarnation: string(r.Destination.Incarnation)})
				if e != nil {
					t.Fatal(e)
				}
				l, e := s.Writer(ctx(t))
				if e != nil {
					t.Fatal(e)
				}
				e = l.Do(ctx(t), func(c *sql.Conn) error {
					if damage == "lineage" {
						_, e := c.ExecContext(ctx(t), "DELETE FROM lineage")
						return e
					}
					if damage == "logical-bytes" {
						_, e := c.ExecContext(ctx(t), "UPDATE card SET title='Unanchored synthetic change'")
						return e
					}
					id := string(uuid(t))
					if _, e := c.ExecContext(ctx(t), "INSERT INTO grant_fact VALUES(?,?)", r.Destination.Incarnation, id); e != nil {
						return e
					}
					_, e := c.ExecContext(ctx(t), "INSERT INTO grant_record VALUES(?,?,?,1)", r.Destination.Incarnation, id, "00000000-0000-4000-8000-00000000000a")
					return e
				})
				ce := l.Close()
				se := s.Close()
				if e != nil || ce != nil || se != nil {
					t.Fatal(e, ce, se)
				}
			}
			if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 4 {
				t.Fatal("corruption confirmed", damage)
			}
			if damage == "missing-lock" {
				if _, e = os.Stat(filepath.Join(q.Destination, "data", "board.lock")); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("status recreated metadata")
				}
			}
		})
	}
}

func TestNativeJunctionTraversalRefusedBeforeCreation(t *testing.T) {
	n, q, _ := actualSource(t)
	junction := filepath.Join(filepath.Dir(filepath.Dir(q.Destination)), "redirect")
	if e := n.mkdir(junction); e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(pointer(junction), windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		t.Fatal(e)
	}
	target := filepath.Dir(q.Destination)
	sub, printName := utf16.Encode([]rune(`\??\`+target)), utf16.Encode([]rune(target))
	paths := append(append(append(sub, 0), printName...), 0)
	buf := make([]byte, 16+len(paths)*2)
	binary.LittleEndian.PutUint32(buf, windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:], uint16(len(sub)*2))
	binary.LittleEndian.PutUint16(buf[12:], uint16((len(sub)+1)*2))
	binary.LittleEndian.PutUint16(buf[14:], uint16(len(printName)*2))
	for j, v := range paths {
		binary.LittleEndian.PutUint16(buf[16+j*2:], v)
	}
	var returned uint32
	e = windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil)
	windows.CloseHandle(h)
	if e != nil {
		t.Fatal(e)
	}
	copy := q
	copy.Destination = filepath.Join(junction, "refused")
	if got := Restore(ctx(t), copy); got.Exit != 4 {
		t.Fatal("junction traversal accepted")
	}
	if _, e = os.Stat(filepath.Join(target, "refused")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("junction wrote destination")
	}
}

func TestNativeLeaseHelperProcess(t *testing.T) {
	destination := os.Getenv("A04_HELPER_DESTINATION")
	if destination == "" {
		return
	}
	n, e := newNative()
	if e != nil {
		t.Fatal(e)
	}
	f, e := n.lease(destination, A.UUID(os.Getenv("A04_HELPER_REQUEST")), false)
	if e == nil {
		f.Close()
		t.Fatal("child process crossed active publication lease")
	}
}
func TestNativeSiblingLeaseExcludesProcessAcrossRename(t *testing.T) {
	_, q, _ := actualSource(t)
	observed := false
	got := restoreWithFault(ctx(t), q, time.Now, func(stage string) error {
		if stage != "after-prepared" {
			return nil
		}
		observed = true
		if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 4 {
			t.Fatal("status crossed active writer lease")
		}
		child := exec.CommandContext(ctx(t), os.Args[0], "-test.run=^TestNativeLeaseHelperProcess$")
		child.Env = append(os.Environ(), "A04_HELPER_DESTINATION="+q.Destination, "A04_HELPER_REQUEST="+string(q.RequestID))
		if output, e := child.CombinedOutput(); e != nil {
			t.Fatalf("native process exclusion: %v (%s)", e, output)
		}
		return nil
	})
	if !observed || got.Exit != 0 {
		t.Fatal("publication lease test did not complete", got.Exit)
	}
}

func TestNativeNoOverwriteWhenTargetAppearsAtRename(t *testing.T) {
	n, q, before := actualSource(t)
	appeared := false
	got := restoreWithFault(ctx(t), q, time.Now, func(stage string) error {
		if stage != "before-rename" {
			return nil
		}
		appeared = true
		if e := n.mkdir(q.Destination); e != nil {
			t.Fatal(e)
		}
		if e := n.write(filepath.Join(q.Destination, "marker"), []byte("retain competing target")); e != nil {
			t.Fatal(e)
		}
		return nil
	})
	if !appeared || got.Exit != 4 {
		t.Fatal("no-overwrite publication race was not exercised", got.Exit)
	}
	marker, e := os.ReadFile(filepath.Join(q.Destination, "marker"))
	if e != nil || !bytes.Equal(marker, []byte("retain competing target")) {
		t.Fatal("native rename overwrote competing target")
	}
	after, e := os.ReadFile(q.SourceArchive)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("race changed source")
	}
	if got := Status(ctx(t), q.Destination, q.RequestID); got.Exit != 4 {
		t.Fatal("both-location race settled")
	}
}

func TestNativeExclusionOverlapAliasesAndNoOverwrite(t *testing.T) {
	n, q, before := actualSource(t)
	for _, path := range []string{filepath.Dir(q.SourceArchive), filepath.Join(filepath.Dir(q.SourceArchive), "inside"), filepath.Dir(filepath.Dir(q.SourceArchive)), q.ProtectedRoots[0], filepath.Join(q.ProtectedRoots[0], "child")} {
		copy := q
		copy.Destination = path
		if got := Restore(ctx(t), copy); got.Exit != 4 {
			t.Fatal("overlap accepted")
		}
	}
	// Source aliases are rejected even when the bytes themselves are valid.
	alias := filepath.Join(filepath.Dir(q.SourceArchive), "alias.zip")
	if e := windows.CreateHardLink(pointer(alias), pointer(q.SourceArchive), 0); e != nil {
		t.Fatal(e)
	}
	copy := q
	copy.SourceArchive = alias
	if got := Restore(ctx(t), copy); got.Exit != 4 {
		t.Fatal("hardlinked source accepted")
	}
	if e := n.mkdir(q.Destination); e != nil {
		t.Fatal(e)
	}
	marker := filepath.Join(q.Destination, "marker")
	if e := n.write(marker, []byte("preserve")); e != nil {
		t.Fatal(e)
	}
	if got := Restore(ctx(t), q); got.Exit != 4 {
		t.Fatal("existing target accepted")
	}
	markerBytes, _ := os.ReadFile(marker)
	after, _ := os.ReadFile(q.SourceArchive)
	if !bytes.Equal(markerBytes, []byte("preserve")) || !bytes.Equal(before, after) {
		t.Fatal("existing material overwritten")
	}
}

func TestNativeSourceLockedAgainstConcurrentModification(t *testing.T) {
	n, q, _ := actualSource(t)
	f, e := n.file(q.SourceArchive, false, false, false, false)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	writer, e := os.OpenFile(q.SourceArchive, os.O_WRONLY, 0)
	if e == nil {
		writer.Close()
		t.Fatal("source write crossed read lease")
	}
	raw, e := io.ReadAll(f)
	if e != nil || len(raw) == 0 {
		t.Fatal(e)
	}
}

func TestNativeOfflineFacadeIdentityRevisionAndClosedSnapshot(t *testing.T) {
	_, q, raw := actualSource(t)
	p, e := V.Read(ctx(t), bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	m, e := p.Manifest()
	if e != nil {
		t.Fatal(e)
	}
	var retained S.InspectionRead
	e = S.InspectExisting(ctx(t), q.ProtectedRoots[0], schema.Identity{BoardID: string(m.Source.BoardID), Incarnation: string(m.Source.Incarnation)}, func(r S.InspectionRead) error {
		retained = r // Deliberate negative probe: production callbacks never retain it.
		bad := m.Source
		bad.Incarnation = uuid(t)
		if _, e := E.ProjectOffline(r, bad, m.Revision); !errors.Is(e, E.ErrPrecondition) {
			t.Fatal("offline facade admitted another identity", e)
		}
		if _, e := E.ProjectOffline(r, m.Source, m.Revision+1); !errors.Is(e, E.ErrPrecondition) {
			t.Fatal("offline facade admitted another revision", e)
		}
		_, e := E.ProjectOffline(r, m.Source, m.Revision)
		return e
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = E.ProjectOffline(retained, m.Source, m.Revision); e == nil {
		t.Fatal("closed inspection snapshot exposed a reader")
	}
}
