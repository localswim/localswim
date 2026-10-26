package validate

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func fixture(t testing.TB) []member {
	t.Helper()
	root := filepath.Join("..", "..", "..", "fixtures", "contracts", "archive-recovery", "package")
	names := []string{"manifest.json", "board.json", "history.json", "activity.json", "outcomes.json"}
	blobs, e := filepath.Glob(filepath.Join(root, "blobs", "*"))
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range blobs {
		names = append(names, "blobs/"+filepath.Base(b))
	}
	names = append(names, "COMPLETE")
	ms := make([]member, 0, len(names))
	for _, n := range names {
		b, e := os.ReadFile(filepath.Join(root, filepath.FromSlash(n)))
		if e != nil {
			t.Fatal(e)
		}
		ms = append(ms, member{n, b})
	}
	return ms
}

// This ordinary ZIP producer is independent of the product parser. Perturbations
// below change both valid container output and individual raw header fields.
func stored(t testing.TB, ms []member, metadata bool) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	if metadata {
		if e := z.SetComment("benign package comment PK\x05\x06"); e != nil {
			t.Fatal(e)
		}
	}
	for _, m := range ms {
		h := &zip.FileHeader{Name: m.name, Method: zip.Store, ReaderVersion: 20, CreatorVersion: 20, CRC32: crc32.ChecksumIEEE(m.data), CompressedSize64: uint64(len(m.data)), UncompressedSize64: uint64(len(m.data))}
		if metadata {
			h.Flags = 0x800
			h.Comment = "inert comment"
			h.Extra = []byte{0x55, 0x54, 5, 0, 1, 1, 2, 3, 4}
			h.ModifiedDate = 123
			h.ModifiedTime = 456
			h.SetMode(0600)
		}
		w, e := z.CreateRaw(h)
		if e != nil {
			t.Fatal(e)
		}
		if strings.HasSuffix(m.name, "/") {
			continue
		}
		if _, e = w.Write(m.data); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return out.Bytes()
}

func resign(t *testing.T, ms []member) {
	t.Helper()
	m, e := A.DecodeReadable[A.Manifest](ms[0].data)
	if e != nil {
		t.Fatal(e)
	}
	m.Inventory = []A.Entry{}
	for _, v := range ms[1 : len(ms)-1] {
		m.Inventory = append(m.Inventory, A.Entry{Path: v.name, ByteLength: A.Number(len(v.data)), SHA256: digest(v.data)})
	}
	sort.Slice(m.Inventory, func(i, j int) bool { return m.Inventory[i].Path < m.Inventory[j].Path })
	ms[0].data, e = A.Readable(m)
	if e != nil {
		t.Fatal(e)
	}
	ms[len(ms)-1].data = []byte(string(digest(ms[0].data)) + "\n")
}

func requireReject(t *testing.T, raw []byte) error {
	t.Helper()
	p, e := Read(context.Background(), bytes.NewReader(raw))
	if e == nil || p != nil {
		t.Fatal("invalid package produced a handle")
	}
	return e
}

func central(raw []byte) []int {
	e := len(raw) - 22
	for e >= 0 && (u32(raw, e) != 0x06054b50 || e+22+int(u16(raw, e+20)) != len(raw)) {
		e--
	}
	if e < 0 {
		return nil
	}
	pos := int(u32(raw, e+16))
	out := []int{}
	for i := 0; i < int(u16(raw, e+10)); i++ {
		out = append(out, pos)
		pos += 46 + int(u16(raw, pos+28)) + int(u16(raw, pos+30)) + int(u16(raw, pos+32))
	}
	return out
}
func put16(b []byte, p int, n uint16) { binary.LittleEndian.PutUint16(b[p:], n) }
func put32(b []byte, p int, n uint32) { binary.LittleEndian.PutUint32(b[p:], n) }

func TestFixtureAndBenignMetadata(t *testing.T) {
	for _, meta := range []bool{false, true} {
		raw := stored(t, fixture(t), meta)
		if meta {
			// Local/central timestamps need not agree. They never define content.
			put16(raw, 10, 999)
			put16(raw, 12, 888)
			put16(raw, 4, 10)
			cs0 := central(raw)[0]
			put16(raw, cs0+4, 3<<8|63)             // Creator's version is informational.
			put16(raw, cs0+36, 1)                  // Inert text advisory; bytes are never converted.
			raw[cs0+46+len("manifest.json")+4] = 7 // Central UT may omit access/create values.
			// Reverse central records while retaining the required physical order.
			cs := central(raw)
			copyRaw := bytes.Clone(raw)
			pos := cs[0]
			for i := len(cs) - 1; i >= 0; i-- {
				c := cs[i]
				n := 46 + int(u16(copyRaw, c+28)) + int(u16(copyRaw, c+30)) + int(u16(copyRaw, c+32))
				copy(raw[pos:], copyRaw[c:c+n])
				pos += n
			}
		}
		p, e := Read(context.Background(), bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		if p.ByteLength() != int64(len(raw)) || len(p.MemberNames()) != len(fixture(t)) {
			t.Fatal("package metadata")
		}
		m, e := p.Manifest()
		if e != nil || m.Source.BoardID == "" || p.ManifestSHA256() == "" {
			t.Fatal("manifest")
		}
		if b, e := p.Member("COMPLETE"); e != nil || string(b) != string(p.ManifestSHA256())+"\n" {
			t.Fatal("complete")
		}
	}
}

func TestUntrustedInputAndOutputHaveNoAliases(t *testing.T) {
	raw := stored(t, fixture(t), false)
	r := &retainingReader{data: raw}
	p, e := Read(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := p.Member("manifest.json")
	clear(raw)
	for _, b := range r.buffers {
		clear(b)
	}
	got, _ := p.Member("manifest.json")
	if !bytes.Equal(got, before) {
		t.Fatal("source alias")
	}
	clear(got)
	got, _ = p.Member("manifest.json")
	if !bytes.Equal(got, before) {
		t.Fatal("member alias")
	}
	names := p.MemberNames()
	names[0] = "changed"
	m, _ := p.Manifest()
	m.Inventory[0].Path = "changed"
	m.Lineage = append(m.Lineage, A.Lineage{})
	m2, _ := p.Manifest()
	if m2.Inventory[0].Path == "changed" || p.MemberNames()[0] != "manifest.json" {
		t.Fatal("nested accessor alias")
	}
	if _, e = p.Member("missing"); e == nil {
		t.Fatal("unknown member")
	}
	var zero Package
	for _, p := range []*Package{nil, &zero} {
		if _, e = p.Member("manifest.json"); e == nil {
			t.Fatal("zero handle")
		}
		if _, e = p.Manifest(); e == nil {
			t.Fatal("zero manifest")
		}
	}
}

type retainingReader struct {
	data    []byte
	buffers [][]byte
}

func (r *retainingReader) Read(p []byte) (int, error) {
	r.buffers = append(r.buffers, p)
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestHostileHeaderAndContainerMutations(t *testing.T) {
	base := stored(t, fixture(t), false)
	c := central(base)[0]
	cases := map[string]func([]byte){
		"local name":                  func(b []byte) { b[30] = 'M' },
		"local method":                func(b []byte) { put16(b, 8, 8) },
		"local flags":                 func(b []byte) { put16(b, 6, 8) },
		"local crc":                   func(b []byte) { b[14] ^= 1 },
		"local compressed size":       func(b []byte) { b[18] ^= 1 },
		"local actual size":           func(b []byte) { b[22] ^= 1 },
		"local reader version":        func(b []byte) { put16(b, 4, 45) },
		"central method":              func(b []byte) { put16(b, c+10, 8) },
		"central encryption":          func(b []byte) { put16(b, c+8, 1) },
		"central descriptor":          func(b []byte) { put16(b, c+8, 8) },
		"central unknown flags":       func(b []byte) { put16(b, c+8, 0x4000) },
		"central size":                func(b []byte) { b[c+20] ^= 1 },
		"central offset":              func(b []byte) { put32(b, c+42, 1) },
		"central unix symlink":        func(b []byte) { put16(b, c+4, 3<<8|20); put32(b, c+38, 0120777<<16) },
		"central unix directory":      func(b []byte) { put16(b, c+4, 3<<8|20); put32(b, c+38, 0040755<<16) },
		"central dos directory":       func(b []byte) { put32(b, c+38, 16) },
		"central unknown platform":    func(b []byte) { put16(b, c+4, 7<<8|20) },
		"central internal attributes": func(b []byte) { put16(b, c+36, 2) },
		"zip64 sizes":                 func(b []byte) { put32(b, c+20, 0xffffffff); put32(b, c+24, 0xffffffff) },
		"zip64 offset":                func(b []byte) { put32(b, c+42, 0xffffffff) },
		"multidisk member":            func(b []byte) { put16(b, c+34, 1) },
		"multidisk eocd":              func(b []byte) { put16(b, len(b)-18, 1) },
		"disk count disagreement":     func(b []byte) { put16(b, len(b)-14, 1) },
		"directory size":              func(b []byte) { b[len(b)-10] ^= 1 },
		"directory offset":            func(b []byte) { b[len(b)-6] ^= 1 },
		"data crc failure":            func(b []byte) { b[30+len("manifest.json")] ^= 1 },
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) { b := bytes.Clone(base); f(b); requireReject(t, b) })
	}
	requireReject(t, append(bytes.Clone(base), 0))
	requireReject(t, append([]byte("SFX-prefix"), base...))
	for i := 0; i < len(base); i++ {
		if _, e := parseZIP(context.Background(), base[:i]); e == nil {
			t.Fatal("accepted truncation", i)
		}
	}
}

func TestUnsafeDuplicateExtraMissingAndOrder(t *testing.T) {
	for _, name := range []string{"../board.json", "/board.json", `C:\board.json`, `\\host\board.json`, `blobs\sha256-` + strings.Repeat("0", 64), "./board.json", "board.json/", "board.json:stream", "BOARD.JSON", "board\x00.json", "blobs/sha256-" + strings.Repeat("A", 64)} {
		t.Run(name, func(t *testing.T) { ms := fixture(t); ms[1].name = name; requireReject(t, stored(t, ms, false)) })
	}
	ms := fixture(t)
	ms[2].name = ms[1].name
	requireReject(t, stored(t, ms, false))
	ms = fixture(t)
	ms = append(ms[:2], ms[3:]...)
	requireReject(t, stored(t, ms, false))
	ms = fixture(t)
	ms = append(ms[:len(ms)-1], member{"unlisted", []byte{}}, ms[len(ms)-1])
	requireReject(t, stored(t, ms, false))
	ms = fixture(t)
	ms[1], ms[2] = ms[2], ms[1]
	requireReject(t, stored(t, ms, false))
	ms = fixture(t)
	ms[len(ms)-1].data = []byte(strings.Repeat("0", 64) + "\n")
	requireReject(t, stored(t, ms, false))
	ms = fixture(t)
	ms[1].data = append(ms[1].data, ' ')
	requireReject(t, stored(t, ms, false))
}

func TestRehashedInvalidJSONAndGraphRefuse(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"non-R whitespace": func(b []byte) []byte { return append([]byte(" "), b...) },
		"unknown field": func(b []byte) []byte {
			return bytes.Replace(b, []byte("{\n"), []byte("{\n  \"unsupportedRecoveryScope\": true,\n"), 1)
		},
		"duplicate field": func(b []byte) []byte {
			return bytes.Replace(b, []byte("{\n"), []byte("{\n  \"schemaVersion\": 1,\n"), 1)
		},
		"unknown version": func(b []byte) []byte {
			return bytes.Replace(b, []byte("\"schemaVersion\": 1"), []byte("\"schemaVersion\": 2"), 1)
		},
		"identity mismatch": func(b []byte) []byte {
			return bytes.Replace(b, []byte("\"boardId\": \""), []byte("\"boardId\": \"f"), 1)
		},
		"invalid Unicode": func(b []byte) []byte { return append(b, 0xff) },
		"deep JSON":       func(b []byte) []byte { return []byte(strings.Repeat("[", 33) + "0" + strings.Repeat("]", 33)) },
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			ms := fixture(t)
			ms[1].data = f(ms[1].data)
			resign(t, ms)
			requireReject(t, stored(t, ms, false))
		})
	}
	for _, field := range []string{"formatVersion", "schemaVersion", "hashAlgorithm"} {
		ms := fixture(t)
		old, next := []byte("\""+field+"\": 1"), []byte("\""+field+"\": 2")
		if field == "hashAlgorithm" {
			old = []byte("\"sha256\"")
			next = []byte("\"sha512\"")
		}
		ms[0].data = bytes.Replace(ms[0].data, old, next, 1)
		ms[len(ms)-1].data = []byte(string(digest(ms[0].data)) + "\n")
		requireReject(t, stored(t, ms, false))
	}
	// Change a valid UUID reference while preserving exact schema and R encoding.
	ms := fixture(t)
	b, e := A.DecodeReadable[A.Board](ms[1].data)
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Cards) == 0 {
		t.Fatal("fixture lacks card")
	}
	b.Cards[0].CommentIDs = []A.UUID{"ffffffff-ffff-ffff-ffff-ffffffffffff"}
	ms[1].data, e = A.Readable(b)
	if e != nil {
		t.Fatal(e)
	}
	resign(t, ms)
	requireReject(t, stored(t, ms, false))
	// Raw bytes with a correct CRC and rewritten inventory still cannot change a
	// protected digest: the BLOB path and logical pin must match its actual hash.
	ms = fixture(t)
	for i := range ms {
		if strings.HasPrefix(ms[i].name, "blobs/") && len(ms[i].data) > 0 {
			ms[i].data[0] ^= 1
			break
		}
	}
	resign(t, ms)
	requireReject(t, stored(t, ms, false))
}

func TestInclusiveDeclaredAndAggregateCaps(t *testing.T) {
	for _, depth := range []int{32, 33} {
		_, e := contract.DecodeJSON([]byte(strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth)))
		if (e == nil) != (depth == 32) {
			t.Fatal("shared decoder depth boundary")
		}
	}
	for _, limit := range []struct{ each, total uint64 }{{MaxJSONBytes, MaxJSONTotalBytes}, {MaxBlobBytes, MaxBlobTotalBytes}} {
		var n uint64
		if e := budget(&n, limit.each, limit.each, limit.total); e != nil {
			t.Fatal(e)
		}
		n = limit.total - limit.each
		if e := budget(&n, limit.each, limit.each, limit.total); e != nil || n != limit.total {
			t.Fatal("inclusive aggregate")
		}
		if !errors.Is(budget(&n, 1, limit.each, limit.total), ErrLimit) {
			t.Fatal("aggregate overflow")
		}
		n = 0
		if !errors.Is(budget(&n, limit.each+1, limit.each, limit.total), ErrLimit) {
			t.Fatal("per-member overflow")
		}
		n = ^uint64(0)
		if !errors.Is(budget(&n, 1, limit.each, limit.total), ErrLimit) {
			t.Fatal("integer overflow")
		}
	}
	raw := stored(t, fixture(t), false)
	cs := central(raw)
	put32(raw, cs[0]+20, MaxJSONBytes+1)
	put32(raw, cs[0]+24, MaxJSONBytes+1)
	if !errors.Is(requireReject(t, raw), ErrLimit) {
		t.Fatal("declared size before payload")
	}
	raw = stored(t, fixture(t), false)
	cs = central(raw)
	for _, c := range cs[:3] {
		put32(raw, c+20, MaxJSONBytes)
		put32(raw, c+24, MaxJSONBytes)
	}
	if !errors.Is(requireReject(t, raw), ErrLimit) {
		t.Fatal("declared aggregate before payload")
	}
	raw = stored(t, fixture(t), false)
	put16(raw, len(raw)-12, MaxMembers+1)
	if !errors.Is(requireReject(t, raw), ErrLimit) {
		t.Fatal("member count")
	}
}

func TestExactMemberCapAndDeclaredBlobCaps(t *testing.T) {
	base := fixture(t)
	ms := append([]member(nil), base[:5]...)
	for i := 0; i < MaxMembers-6; i++ {
		ms = append(ms, member{fmt.Sprintf("blobs/sha256-%064x", i), []byte{}})
	}
	ms = append(ms, base[len(base)-1])
	raw := stored(t, ms, false)
	parsed, e := parseZIP(context.Background(), raw)
	if e != nil || len(parsed) != MaxMembers {
		t.Fatal("inclusive member cap", e)
	}
	ms = append(ms[:len(ms)-1], member{fmt.Sprintf("blobs/sha256-%064x", MaxMembers), []byte{}}, base[len(base)-1])
	if !errors.Is(requireReject(t, stored(t, ms, false)), ErrLimit) {
		t.Fatal("member cap + 1")
	}
	ms = append([]member(nil), base[:5]...)
	for i := 0; i < 7; i++ {
		ms = append(ms, member{fmt.Sprintf("blobs/sha256-%064x", i), []byte{}})
	}
	ms = append(ms, base[len(base)-1])
	raw = stored(t, ms, false)
	cs := central(raw)
	put32(raw, cs[5]+20, MaxBlobBytes+1)
	put32(raw, cs[5]+24, MaxBlobBytes+1)
	if !errors.Is(requireReject(t, raw), ErrLimit) {
		t.Fatal("declared BLOB cap")
	}
	raw = stored(t, ms, false)
	cs = central(raw)
	for _, c := range cs[5:12] {
		put32(raw, c+20, MaxBlobBytes)
		put32(raw, c+24, MaxBlobBytes)
	}
	if !errors.Is(requireReject(t, raw), ErrLimit) {
		t.Fatal("declared BLOB aggregate cap")
	}
}

func TestParsedCommentsCannotHideUnparsedContainerBytes(t *testing.T) {
	base := stored(t, fixture(t), false)
	// A signature-shaped sequence inside a parsed comment remains inert, even
	// when its own length looks like an EOCD terminating at physical EOF.
	comment := make([]byte, 22)
	put32(comment, 0, 0x06054b50)
	put16(base, len(base)-2, uint16(len(comment)))
	raw := append(base, comment...)
	if _, e := Read(context.Background(), bytes.NewReader(raw)); e != nil {
		t.Fatal("EOCD inside comment", e)
	}
	// Insert a byte between two local members and rebase every later local offset,
	// central-directory location and total directory length. The byte remains
	// unparsed and must not become accepted merely because offsets are plausible.
	base = stored(t, fixture(t), false)
	cs := central(base)
	gap := int(u32(base, cs[1]+42))
	raw = append(bytes.Clone(base[:gap]), append([]byte{0}, base[gap:]...)...)
	end := len(raw) - 22
	put32(raw, end+16, u32(base, len(base)-6)+1)
	for _, c := range cs {
		newC := c + 1
		offset := u32(raw, newC+42)
		if int(offset) >= gap {
			put32(raw, newC+42, offset+1)
		}
	}
	requireReject(t, raw)
}

func FuzzZIPBounds(f *testing.F) {
	f.Add(stored(f, fixture(f), false))
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04"))
	f.Add(make([]byte, 22))
	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) > MaxArchiveBytes {
			return
		}
		ms, e := parseZIP(context.Background(), b)
		if e == nil && (len(ms) < 6 || len(ms) > MaxMembers) {
			t.Fatal("unbounded members")
		}
	})
}

func TestInputEOFErrorsCancellationAndFullPackageCap(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Read(ctx, bytes.NewReader(nil)); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e := Read(nil, bytes.NewReader(nil)); e == nil {
		t.Fatal("nil context")
	}
	if _, e := Read(context.Background(), nil); e == nil {
		t.Fatal("nil input")
	}
	if _, e := Read(context.Background(), zeroReader{}); !errors.Is(e, io.ErrNoProgress) {
		t.Fatal(e)
	}
	if _, e := Read(context.Background(), badCount{}); e == nil {
		t.Fatal("invalid read count")
	}
	if _, e := Read(context.Background(), errorReader{}); !errors.Is(e, io.ErrUnexpectedEOF) {
		t.Fatal(e)
	}
	b, e := readBounded(context.Background(), io.LimitReader(zeroFill{}, MaxArchiveBytes))
	if e != nil || len(b) != MaxArchiveBytes {
		t.Fatal("exact full input cap", e)
	}
	b = nil
	if _, e = readBounded(context.Background(), io.LimitReader(zeroFill{}, MaxArchiveBytes+1)); !errors.Is(e, ErrLimit) {
		t.Fatal("actual package cap", e)
	}
}

type zeroReader struct{}

func (zeroReader) Read([]byte) (int, error) { return 0, nil }

type badCount struct{}

func (badCount) Read(p []byte) (int, error) { return len(p) + 1, nil }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type zeroFill struct{}

func (zeroFill) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestSupportedAndUnsupportedExtraMetadata(t *testing.T) {
	ntfs := make([]byte, 36)
	put16(ntfs, 0, 0x000a)
	put16(ntfs, 2, 32)
	put16(ntfs, 8, 1)
	put16(ntfs, 10, 24)
	for _, b := range [][]byte{nil, {0x55, 0x54, 1, 0, 0}, {0x55, 0x54, 13, 0, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, ntfs} {
		if !extras(b) {
			t.Fatal("valid timestamp metadata")
		}
	}
	for _, b := range [][]byte{{1, 0, 0, 0}, {0x75, 0x70, 0, 0}, {0x55, 0x54, 0, 0}, {0x55, 0x54, 1, 0, 8}, {0x55, 0x54, 5, 0, 1}, append(bytes.Clone(ntfs), ntfs...), {1}} {
		if extras(b) {
			t.Fatal("unsupported metadata")
		}
	}
	base := stored(t, fixture(t), true)
	c := central(base)[0]
	b := bytes.Clone(base)
	put16(b, 30+len("manifest.json"), 1)
	requireReject(t, b)
	b = bytes.Clone(base)
	put16(b, c+46+len("manifest.json"), 0x7075)
	requireReject(t, b)
}
