package v2

import (
	"math"
	"regexp"
	"strconv"
	"testing"

	"github.com/localswim/localswim/web"
)

// The standard logo colors in docs/ui-standards.md: the near-black tile,
// then the blue, amber and green bars.
var standardLogoColors = []string{"1e2126", "92abf1", "e3ac39", "85b79e"}

// The favicon and the workspace header MUST draw the mark in the standard
// colors, and each bar MUST meet 7:1 contrast against the near-black.
func TestLogoColorsMeetAAAAndMatchHeader(t *testing.T) {
	data, err := web.Assets.ReadFile("generated/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	// The tile is the first fill, then the bars from tallest to shortest.
	fills := regexp.MustCompile(`fill="#([0-9a-f]{6})"`).FindAllStringSubmatch(string(data), -1)
	if len(fills) != len(standardLogoColors) {
		t.Fatalf("expected a tile and three bars, found %d fills", len(fills))
	}
	for i, fill := range fills {
		if fill[1] != standardLogoColors[i] {
			t.Errorf("favicon fill %d is #%s, want the standard #%s", i, fill[1], standardLogoColors[i])
		}
	}
	tile := relativeLuminance(t, standardLogoColors[0])
	for _, color := range standardLogoColors[1:] {
		bar := relativeLuminance(t, color)
		if ratio := (math.Max(bar, tile) + 0.05) / (math.Min(bar, tile) + 0.05); ratio < 7 {
			t.Errorf("bar #%s has contrast %.2f:1 against #%s, want at least 7:1", color, ratio, standardLogoColors[0])
		}
	}

	// The release CSS is minified by esbuild, which keeps these six-digit
	// colors as background:#rrggbb inside each rule. A change of form fails
	// the match rather than passing it.
	css, err := web.Assets.ReadFile("generated/app.css")
	if err != nil {
		t.Fatal(err)
	}
	rules := []string{
		`\.lsw-workspace-header\{`,
		`\.lsw-workspace-mark i\{`,
		`\.lsw-workspace-mark i:nth-child\(2\)\{`,
		`\.lsw-workspace-mark i:nth-child\(3\)\{`,
	}
	for i, rule := range rules {
		match := regexp.MustCompile(rule + `[^}]*background:#([0-9a-f]{6})`).FindSubmatch(css)
		if match == nil {
			t.Errorf("no background color found for %s in app.css", rule)
		} else if string(match[1]) != standardLogoColors[i] {
			t.Errorf("%s background is #%s, want the standard #%s", rule, match[1], standardLogoColors[i])
		}
	}
}

func relativeLuminance(t *testing.T, hex string) float64 {
	t.Helper()
	var channels [3]float64
	for i := range channels {
		value, err := strconv.ParseUint(hex[2*i:2*i+2], 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		c := float64(value) / 255
		if c <= 0.04045 {
			channels[i] = c / 12.92
		} else {
			channels[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}
