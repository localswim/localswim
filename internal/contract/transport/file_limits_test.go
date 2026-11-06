package transport

import (
	"bytes"
	"encoding/json"
	A "github.com/localswim/localswim/internal/contract/archive"
	"strings"
	"testing"
)

func TestFileReadWireSizeAndHardCap(t *testing.T) {
	for _, label := range []string{"", strings.Repeat("<>&", 7000), "é\x00\b\t\r\n\"\\\u2028\u2029"} {
		v := CardFileRead{APIVersion: 1, Scope: "card-file", Result: CardFileView{File: CardFile{Attribution: FileAttribution{ActorLabel: &label}}}}
		var expected bytes.Buffer
		encoder := json.NewEncoder(&expected)
		encoder.SetEscapeHTML(false)
		if e := encoder.Encode(v); e != nil {
			t.Fatal(e)
		}
		n, e := FileReadSize(v)
		if e != nil || n != int64(expected.Len()) {
			t.Fatal("wire count", n, expected.Len(), e)
		}
		actual, e := FileReadBytes(v)
		if e != nil || !bytes.Equal(actual, expected.Bytes()) {
			t.Fatal("wire encoding")
		}
	}
	label := strings.Repeat("\x00", FileActorLabelLimit)
	v := CardFileRead{Result: CardFileView{File: CardFile{Attachment: A.Attachment{}, Attribution: FileAttribution{ActorLabel: &label}}}}
	if n, e := FileReadSize(v); e != nil || n <= FileReadResponseLimit {
		t.Fatal("hard cap fixture")
	}
	if _, e := FileReadBytes(v); e == nil {
		t.Fatal("hard cap accepted")
	}
}
