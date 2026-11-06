package attachments

import (
	"encoding/base64"
	A "github.com/localswim/localswim/internal/contract/archive"
	"testing"
)

func TestFileQueryStrict(t *testing.T) {
	for _, raw := range []string{"view=", "view=current&view=current", "limit=01", "limit=0", "limit=101", "unknown=1", "cursor=not-base64", "cursor="} {
		if _, e := ParseListQuery(raw); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, raw := range []string{"", "view=retained&limit=1", "limit=100"} {
		if _, e := ParseListQuery(raw); e != nil {
			t.Fatalf("refused %q", raw)
		}
	}
	c := fileCursor{Version: 1, BoardID: "10000000-0000-0000-0000-000000000001", Incarnation: "20000000-0000-0000-0000-000000000001", CardID: "30000000-0000-0000-0000-000000000001", BoardRevision: 2, CardRevision: 2, View: "current", AfterCreatedAt: A.Timestamp("2026-01-01T00:00:00Z"), AfterAttachmentID: "40000000-0000-0000-0000-000000000001"}
	encoded := encodeFileCursor(c)
	if got, e := decodeFileCursor(encoded); e != nil || got != c {
		t.Fatal("cursor roundtrip", e)
	}
	bytes, _ := base64.RawURLEncoding.DecodeString(encoded)
	for _, bad := range []string{encoded + "=", base64.RawURLEncoding.EncodeToString(append([]byte(" "), bytes...)), base64.RawURLEncoding.EncodeToString([]byte(`{}`))} {
		if _, e := decodeFileCursor(bad); e == nil {
			t.Fatal("accepted malformed cursor")
		}
	}
}
