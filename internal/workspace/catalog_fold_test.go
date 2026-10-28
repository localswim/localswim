package workspace

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"os"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestCatalogUnicode17FullFoldExhaustive(t *testing.T) {
	if unicode.Version != catalogFoldUnicodeVersion {
		t.Fatal("Go Unicode version changed; regenerate and independently verify catalog folding")
	}
	var oracle *bufio.Reader
	if name := os.Getenv("CATALOG_FOLD_ORACLE"); name != "" {
		f, err := os.Open(name)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		oracle = bufio.NewReader(f)
	}
	digest := sha256.New()
	count := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		folded := catalogFold(string(r))
		var header [8]byte
		binary.BigEndian.PutUint32(header[:4], uint32(r))
		binary.BigEndian.PutUint32(header[4:], uint32(len(folded)))
		digest.Write(header[:])
		digest.Write([]byte(folded))
		if oracle != nil {
			var expectedHeader [8]byte
			if _, err := io.ReadFull(oracle, expectedHeader[:]); err != nil {
				t.Fatal(err)
			}
			length := binary.BigEndian.Uint32(expectedHeader[4:])
			if binary.BigEndian.Uint32(expectedHeader[:4]) != uint32(r) || length > 16 {
				t.Fatalf("invalid oracle record U+%04X", r)
			}
			expected := make([]byte, length)
			if _, err := io.ReadFull(oracle, expected); err != nil {
				t.Fatal(err)
			}
			if folded != string(expected) {
				t.Fatalf("Python17 fold mismatch U+%04X", r)
			}
		}
		count++
	}
	if count != catalogFoldScalarCount || fmt.Sprintf("%x", digest.Sum(nil)) != catalogFoldOracleSHA256 {
		t.Fatal("exhaustive Unicode17 full-fold oracle differs")
	}
	if oracle != nil {
		if _, err := oracle.ReadByte(); err != io.EOF {
			t.Fatal("extra oracle records")
		}
	}
}

func TestCatalogUnicode17FullFoldLabels(t *testing.T) {
	for input, expected := range map[string]string{
		"Straße SS": "strasse ss", "Σίσυφος ςΣσ": "σίσυφοσ σσσ", "İIıi": "i\u0307iıi",
		"ﬃ FFI": "ffi ffi", "Ꭰꭰ": "ᎠᎠ", "KKk": "kkk", "ẞßss": "ssssss",
		"é É e\u0301": "é é e\u0301", "Board 𐐀𐐨": "board 𐐨𐐨",
	} {
		if got := catalogFold(input); got != expected {
			t.Errorf("full fold %q = %q, want %q", input, got, expected)
		}
	}
	for _, pair := range [][2]string{{"ΟΣ", "ος"}, {"Straße", "STRASSE"}, {"İ", "i\u0307"}} {
		a, b := unknownCatalog(member{ProjectKey: "a", BoardKey: "z", Label: T.WorkspaceLabel(pair[0])}), unknownCatalog(member{ProjectKey: "b", BoardKey: "a", Label: T.WorkspaceLabel(pair[1])})
		if !catalogLess(a, b) || catalogLess(b, a) {
			t.Fatal("case-fold ties must fall through to exact keys")
		}
	}
}
