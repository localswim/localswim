package workspace

import (
	"strings"
	"unicode"
)

// catalogFold implements Unicode 17 default full case folding, without locale
// tailoring or normalization. The generated exceptions supplement Go's simple
// lowercase mapping with multi-rune and non-lowercase folding mappings.
func catalogFold(label string) string {
	var folded strings.Builder
	for _, r := range label {
		if exception, ok := catalogFoldExceptions[r]; ok {
			folded.WriteString(exception)
		} else {
			folded.WriteRune(unicode.ToLower(r))
		}
	}
	return folded.String()
}
