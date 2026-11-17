package v2

import (
	"math"
	"regexp"
	"strconv"
	"testing"

	"github.com/localswim/localswim/web"
)

// The localswim mark's standard colours (docs/ui-standards.md): every bar
// meets WCAG AAA contrast (7:1) against the near-black tile, and the
// workspace header draws the same three colours as the favicon.
func TestLogoColoursMeetAAAAndMatchHeader(t *testing.T) {
	data, err := web.Assets.ReadFile("generated/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	fills := regexp.MustCompile(`fill="#([0-9a-f]{6})"`).FindAllStringSubmatch(string(data), -1)
	if len(fills) != 4 {
		t.Fatalf("expected a tile and three bars, found %d fills", len(fills))
	}
	tile := relativeLuminance(t, fills[0][1])
	for _, fill := range fills[1:] {
		bar := relativeLuminance(t, fill[1])
		ratio := (math.Max(bar, tile) + 0.05) / (math.Min(bar, tile) + 0.05)
		if ratio < 7 {
			t.Errorf("bar #%s has contrast %.2f:1 against tile #%s, want at least 7:1", fill[1], ratio, fills[0][1])
		}
	}
	css, err := web.Assets.ReadFile("generated/app.css")
	if err != nil {
		t.Fatal(err)
	}
	header := regexp.MustCompile(`\.lsw-workspace-header\{[^}]*background:#([0-9a-f]{6})`).FindSubmatch(css)
	if header == nil || string(header[1]) != fills[0][1] {
		t.Errorf("workspace header background %q differs from the favicon tile #%s", header, fills[0][1])
	}
	selectors := []string{`\.lsw-workspace-mark i\{`, `\.lsw-workspace-mark i:nth-child\(2\)\{`, `\.lsw-workspace-mark i:nth-child\(3\)\{`}
	for i, selector := range selectors {
		bar := regexp.MustCompile(selector + `[^}]*background:#([0-9a-f]{6})`).FindSubmatch(css)
		if bar == nil || string(bar[1]) != fills[i+1][1] {
			t.Errorf("header mark bar %d is %q, want the favicon's #%s", i+1, bar, fills[i+1][1])
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
