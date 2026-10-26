package export

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"

	"github.com/localswim/localswim/internal/contract"
	A "github.com/localswim/localswim/internal/contract/archive"
)

var testIdentity = A.Identity{BoardID: "10000000-0000-0000-0000-000000000001", Incarnation: "20000000-0000-0000-0000-000000000001"}

const testTime A.Timestamp = "2026-10-26T12:00:00Z"

func emptyDocuments() (A.Board, A.History, A.Activity, A.Outcomes) {
	b := A.Board{SchemaVersion: 1, Identity: testIdentity, NextTicket: 1, PolicyVersion: 1, Retention: A.Retention{HistoryDays: 90, ActivityDays: 365, OutcomeDays: 180}, RetentionVersion: 1, Coverage: A.Coverage{Gaps: []A.Gap{}}, Actors: []A.Actor{}, Cards: []A.Card{}, Comments: []A.Comment{}, Links: []A.Link{}, Attachments: []A.Attachment{}, Approvals: []A.Approval{}, Tombstones: []A.EntityTombstone{}}
	return b, A.History{SchemaVersion: 1, Identity: testIdentity, Records: []A.Snapshot{}}, A.Activity{SchemaVersion: 1, Identity: testIdentity, Events: []A.Event{}}, A.Outcomes{SchemaVersion: 1, Identity: testIdentity, Operations: []A.Operation{}, SourceArchives: []A.SourceArchive{}}
}
func emptyPrepared(t *testing.T) *Prepared {
	t.Helper()
	b, h, a, o := emptyDocuments()
	p, e := assemble(context.Background(), b, h, a, o, []A.Lineage{}, testTime, []A.ByteFact{}, func(A.ByteFact) ([]byte, error) { t.Fatal("unexpected blob"); return nil, nil })
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func readArchive(t *testing.T, p *Prepared) ([]string, map[string][]byte) {
	t.Helper()
	var out bytes.Buffer
	if e := p.Write(context.Background(), &out); e != nil {
		t.Fatal(e)
	}
	if int64(out.Len()) != p.ByteLength() {
		t.Fatal("size")
	}
	z, e := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if e != nil {
		t.Fatal(e)
	}
	names := []string{}
	data := map[string][]byte{}
	for _, f := range z.File {
		names = append(names, f.Name)
		if f.Method != zip.Store || f.Flags != 0 || len(f.Extra) != 0 || f.CompressedSize64 != f.UncompressedSize64 {
			t.Fatal("unsupported ZIP metadata")
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(r)
		ce := r.Close()
		if e != nil || ce != nil {
			t.Fatal(e, ce)
		}
		data[f.Name] = raw
	}
	return names, data
}
func TestEmptyInventoryExactReadableHashAndComplete(t *testing.T) {
	p := emptyPrepared(t)
	names, data := readArchive(t, p)
	if strings.Join(names, ",") != "manifest.json,board.json,history.json,activity.json,outcomes.json,COMPLETE" {
		t.Fatal(names)
	}
	m, e := A.DecodeReadable[A.Manifest](data["manifest.json"])
	if e != nil {
		t.Fatal(e)
	}
	if string(data["COMPLETE"]) != string(digest(data["manifest.json"]))+"\n" || p.ManifestSHA256() != digest(data["manifest.json"]) {
		t.Fatal("completion hash")
	}
	if len(m.Inventory) != 4 {
		t.Fatal("inventory")
	}
	for _, v := range m.Inventory {
		if digest(data[v.Path]) != v.SHA256 || len(data[v.Path]) != int(v.ByteLength) {
			t.Fatal("inventory hash/length")
		}
	}
}
func TestInclusiveBudgetsAndOverflow(t *testing.T) {
	for _, v := range []struct {
		start, n int
		good     bool
	}{{0, MaxJSONBytes, true}, {0, MaxJSONBytes + 1, false}, {MaxJSONTotalBytes - MaxJSONBytes, MaxJSONBytes, true}, {MaxJSONTotalBytes - MaxJSONBytes + 1, MaxJSONBytes, false}, {0, -1, false}} {
		n := v.start
		if (jsonBudget(&n, v.n) == nil) != v.good {
			t.Fatal(v)
		}
	}
	name := "board.json"
	over := int64(76 + 2*len(name))
	n := int64(22)
	if e := addZIPSize(&n, name, MaxArchiveBytes-22-over); e != nil || n != MaxArchiveBytes {
		t.Fatal(e, n)
	}
	if !errors.Is(addZIPSize(&n, name, 0), ErrLimit) {
		t.Fatal("package cap")
	}
	n = 22
	if !errors.Is(addZIPSize(&n, name, -1), ErrLimit) {
		t.Fatal("negative length")
	}
}
func TestMemberAndBlobTotalCapsBeforeBlobAllocation(t *testing.T) {
	b, h, a, o := emptyDocuments()
	called := false
	load := func(A.ByteFact) ([]byte, error) { called = true; return nil, nil }
	if _, e := assemble(context.Background(), b, h, a, o, []A.Lineage{}, testTime, make([]A.ByteFact, MaxMembers-5), load); !errors.Is(e, ErrLimit) || called {
		t.Fatal("member cap", e)
	}
	facts := []A.ByteFact{}
	for i := 0; i < 7; i++ {
		facts = append(facts, A.ByteFact{SHA256: digest([]byte{byte(i)}), ByteLength: MaxBlobBytes})
	} // Sort and validation are independent of byte allocation.
	for i := range facts {
		for j := i + 1; j < len(facts); j++ {
			if facts[j].SHA256 < facts[i].SHA256 {
				facts[i], facts[j] = facts[j], facts[i]
			}
		}
	}
	if _, e := assemble(context.Background(), b, h, a, o, []A.Lineage{}, testTime, facts, load); !errors.Is(e, ErrLimit) || called {
		t.Fatal("blob total cap", e)
	}
}
func TestDepthCapAndMissingProtectedInventory(t *testing.T) {
	raw := []byte(strings.Repeat("[", 32) + "0" + strings.Repeat("]", 32))
	if _, e := contract.DecodeJSON(raw); e != nil {
		t.Fatal(e)
	}
	if _, e := contract.DecodeJSON(append(append([]byte{'['}, raw...), ']')); e == nil {
		t.Fatal("depth33")
	}
	b, h, a, o := emptyDocuments()
	if _, e := assemble(context.Background(), b, h, a, o, []A.Lineage{}, testTime, []A.ByteFact{{SHA256: digest(nil), ByteLength: 0}}, func(A.ByteFact) ([]byte, error) { t.Fatal("unprotected blob read"); return nil, nil }); e == nil {
		t.Fatal("extra pin accepted")
	}
}

type failWriter struct {
	left  int
	short bool
	out   bytes.Buffer
}

func (w *failWriter) Write(p []byte) (int, error) {
	if len(p) > w.left {
		n := w.left
		w.out.Write(p[:n])
		w.left = 0
		if w.short {
			return n, nil
		}
		return n, errors.New("synthetic delivery failure")
	}
	w.left -= len(p)
	return w.out.Write(p)
}
func TestInterruptedOutputNeverFinalizesZIP(t *testing.T) {
	p := emptyPrepared(t)
	for _, short := range []bool{false, true} {
		w := &failWriter{left: int(p.size) - 100, short: short}
		if e := p.Write(context.Background(), w); e == nil {
			t.Fatal("interruption accepted")
		}
		if _, e := zip.NewReader(bytes.NewReader(w.out.Bytes()), int64(w.out.Len())); e == nil {
			t.Fatal("partial zip finalized")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if e := p.Write(ctx, &out); !errors.Is(e, context.Canceled) || out.Len() != 0 {
		t.Fatal("cancelled output", e)
	}
}

type alteringWriter struct{ held [][]byte }

func (w *alteringWriter) Write(p []byte) (int, error) {
	w.held = append(w.held, p)
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}
func TestExternalWriterCannotChangePreparedBytes(t *testing.T) {
	p := emptyPrepared(t)
	var before bytes.Buffer
	if e := p.Write(context.Background(), &before); e != nil {
		t.Fatal(e)
	}
	evil := &alteringWriter{}
	if e := p.Write(context.Background(), evil); e != nil {
		t.Fatal(e)
	}
	for _, part := range evil.held {
		for i := range part {
			part[i] = 255
		}
	}
	var after bytes.Buffer
	if e := p.Write(context.Background(), &after); e != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("writer changed private staging", e)
	}
}

func TestReadablePreflightMatchesSharedEncoder(t *testing.T) {
	b, h, a, o := emptyDocuments()
	target := A.UUID("40000000-0000-0000-0000-000000000001")
	for _, v := range []any{b, h, a, o, A.Target{CardID: &target}, A.FullOutcome{Intent: []byte(`{"x":["\\u0001",{"text":"ÃŽÂ© <&>"}]}`), Result: []byte(`{}`)}, map[string]any{"empty": []any{}, "text": "\x00\r\n\"\\ÃŽÂ©", "nested": []any{true, nil, map[string]any{"id": json.Number("9007199254740991")}}}} {
		n, e := readableLength(v)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := A.Readable(v)
		if e != nil || n != len(raw) {
			t.Fatalf("preflight mismatch for %T: %d != %d (%v)", v, n, len(raw), e)
		}
	}
	if _, e := readableLength(strings.Repeat("x", MaxJSONBytes)); !errors.Is(e, ErrLimit) {
		t.Fatal("large scalar preflight", e)
	}
}

func TestStoredZIPLocalAndCentralHeadersAgree(t *testing.T) {
	p := emptyPrepared(t)
	var out bytes.Buffer
	if e := p.Write(context.Background(), &out); e != nil {
		t.Fatal(e)
	}
	raw := out.Bytes()
	at := 0
	offsets := []int{}
	u16 := func(pos int) uint16 { return binary.LittleEndian.Uint16(raw[pos:]) }
	u32 := func(pos int) uint32 { return binary.LittleEndian.Uint32(raw[pos:]) }
	for _, m := range p.members {
		offsets = append(offsets, at)
		if u32(at) != 0x04034b50 || u16(at+6) != 0 || u16(at+8) != 0 || u32(at+14) != crc32.ChecksumIEEE(m.data) || u32(at+18) != uint32(len(m.data)) || u32(at+22) != uint32(len(m.data)) || u16(at+26) != uint16(len(m.name)) || u16(at+28) != 0 || string(raw[at+30:at+30+len(m.name)]) != m.name {
			t.Fatal("local header mismatch", m.name)
		}
		at += 30 + len(m.name) + len(m.data)
	}
	central := at
	for i, m := range p.members {
		if u32(at) != 0x02014b50 || u16(at+8) != 0 || u16(at+10) != 0 || u32(at+16) != crc32.ChecksumIEEE(m.data) || u32(at+20) != uint32(len(m.data)) || u32(at+24) != uint32(len(m.data)) || u16(at+28) != uint16(len(m.name)) || u16(at+30) != 0 || u16(at+32) != 0 || u16(at+34) != 0 || u32(at+42) != uint32(offsets[i]) || string(raw[at+46:at+46+len(m.name)]) != m.name {
			t.Fatal("central header mismatch", m.name)
		}
		at += 46 + len(m.name)
	}
	if u32(at) != 0x06054b50 || u16(at+4) != 0 || u16(at+6) != 0 || u16(at+8) != uint16(len(p.members)) || u16(at+10) != uint16(len(p.members)) || u32(at+12) != uint32(at-central) || u32(at+16) != uint32(central) || u16(at+20) != 0 || at+22 != len(raw) {
		t.Fatal("directory/end mismatch")
	}
}
