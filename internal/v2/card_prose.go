package v2

import "strings"

type proseSpan struct {
	start, end, width int
}

// proseSpans pairs consecutive delimiter runs of the same width on one line.
// A different width ends an unmatched opener. Emphasis must touch its text at
// both ends. Keep this grammar in sync with web/card-prose.mjs and the shared
// test cases.
func proseSpans(text string, delimiter byte, maxWidth int, code []proseSpan) []proseSpan {
	var spans []proseSpan
	start, width := -1, 0
	delimiters := string(delimiter) + "\r\n"
	for offset := 0; offset < len(text); {
		next := strings.IndexAny(text[offset:], delimiters)
		if next == -1 {
			break
		}
		offset += next
		index := offset
		offset++
		for len(code) > 0 && code[0].end <= index {
			code = code[1:]
		}
		if len(code) > 0 && code[0].start < index {
			offset = code[0].end
			continue
		}
		if text[index] != delimiter {
			start = -1
			continue
		}
		for offset < len(text) && text[offset] == delimiter {
			offset++
		}
		run := offset - index
		switch {
		case run > maxWidth:
			start = -1
		case start != -1 && width == run && (delimiter == '`' || !proseSpace(text[index-1])):
			spans = append(spans, proseSpan{start, offset, width})
			start = -1
		case delimiter == '`' || offset < len(text) && !proseSpace(text[offset]):
			start, width = index, run
		default:
			start = -1
		}
	}
	return spans
}

func proseSpace(value byte) bool {
	return value == ' ' || value == '\t'
}

// readableCardProse projects the rendered text without interpreting HTML.
// Code contents and ticket labels are searchable exactly as displayed.
func readableCardProse(text string) string {
	code := proseSpans(text, '`', 1, nil)
	emphasis := proseSpans(text, '*', 2, code)
	if len(code) == 0 && len(emphasis) == 0 {
		return text
	}
	var readable strings.Builder
	readable.Grow(len(text))
	appendWithCode := func(from, to int) {
		for len(code) > 0 && code[0].end <= to {
			span := code[0]
			readable.WriteString(text[from:span.start])
			readable.WriteString(text[span.start+1 : span.end-1])
			from = span.end
			code = code[1:]
		}
		readable.WriteString(text[from:to])
	}
	from := 0
	for _, span := range emphasis {
		appendWithCode(from, span.start)
		appendWithCode(span.start+span.width, span.end-span.width)
		from = span.end
	}
	appendWithCode(from, len(text))
	return readable.String()
}
