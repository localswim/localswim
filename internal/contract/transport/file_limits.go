package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

const FileReadResponseLimit = 134217728
const FileReadPageTarget = 1048576
const FileActorLabelLimit = 33554432

// FileReadSize counts the exact compact Encoder wire, including the final newline,
// without first allocating escaped copies of potentially large actor labels.
func FileReadSize(value any) (int64, error) {
	var labels int64
	strip := func(f CardFile) (CardFile, error) {
		if f.Attribution.ActorLabel == nil {
			return f, nil
		}
		label := *f.Attribution.ActorLabel
		if len(label) > FileActorLabelLimit || !utf8.ValidString(label) {
			return f, fmt.Errorf("unsupported file actor label")
		}
		labels += jsonLabelSize(label) - 4
		f.Attribution.ActorLabel = nil
		return f, nil
	}
	var small any
	switch v := value.(type) {
	case CardFilesRead:
		v.Result.Items = append([]CardFile{}, v.Result.Items...)
		for i, f := range v.Result.Items {
			x, e := strip(f)
			if e != nil {
				return 0, e
			}
			v.Result.Items[i] = x
		}
		small = v
	case CardFileRead:
		f, e := strip(v.Result.File)
		if e != nil {
			return 0, e
		}
		v.Result.File = f
		small = v
	default:
		return 0, fmt.Errorf("unsupported file read")
	}
	var b bytes.Buffer
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if e := encoder.Encode(small); e != nil {
		return 0, e
	}
	return int64(b.Len()) + labels, nil
}
func jsonLabelSize(s string) int64 {
	n := int64(2)
	for _, r := range s {
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			n += 2
		case '\u2028', '\u2029':
			n += 6
		default:
			if r < 0x20 {
				n += 6
			} else {
				n += int64(utf8.RuneLen(r))
			}
		}
	}
	return n
}

// FileReadBytes is used only for service-built or strictly decoded file DTOs.
// The preflight prevents Encoder from allocating an over-cap escaped response.
func FileReadBytes(value any) ([]byte, error) {
	size, e := FileReadSize(value)
	if e != nil || size > FileReadResponseLimit {
		return nil, fmt.Errorf("file response exceeds limit")
	}
	var b bytes.Buffer
	b.Grow(int(size))
	encoder := json.NewEncoder(&b)
	encoder.SetEscapeHTML(false)
	if e = encoder.Encode(value); e != nil || int64(b.Len()) != size {
		return nil, fmt.Errorf("file response encoding failed")
	}
	return b.Bytes(), nil
}
