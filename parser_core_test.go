// Copyright 2026 The oksvg Authors. All rights reserved.
//
// parser_core_test.go covers the T5 core-parser rework: the depth-aware defs
// collection model, the error-mode contract, style semantics, color parsing,
// and gradient fixes.

package oksvg

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
)

// parse is a helper that parses an SVG string, recovering from panics so a
// regression surfaces as a test failure instead of aborting the whole binary.
func parse(t *testing.T, svg string, mode ErrorMode) (icon *SvgIcon, err error, panicked bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	icon, err = ReadIconStream(strings.NewReader(svg), mode)
	return
}

// renderRGBA rasterizes an icon into an RGBA image at its viewBox size.
func renderRGBA(t *testing.T, icon *SvgIcon) *image.RGBA {
	t.Helper()
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	if w <= 0 {
		w = 20
	}
	if h <= 0 {
		h = 20
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.SetTarget(0, 0, float64(w), float64(h))
	icon.Draw(raster, 1.0)
	return img
}

func countRedPixels(img *image.RGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0x8000 && r > 0x8000 && g < 0x4000 && bl < 0x4000 {
				n++
			}
		}
	}
	return n
}

// --- T5.1 Defs collection model ------------------------------------------

// svgNestedDefsPanic reproduces the confirmed stack-underflow panic: flushing
// the flat currentDef on each ID'd child strips two group starts but keeps two
// endg markers, so replaying Defs["c"] pops the style stack below its base.
const svgNestedDefsPanic = `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><g id="a"><g id="b"><path id="c" d="M0 0 L10 0 L10 10 Z"/></g></g></defs>
<use href="#c"/>
</svg>`

func TestDefsNestedNoPanic(t *testing.T) {
	icon, err, panicked := parse(t, svgNestedDefsPanic, IgnoreErrorMode)
	if panicked {
		t.Fatal("ReadIconStream panicked on valid nested defs + use")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// use #c references a single path.
	if got := len(icon.SVGPaths); got != 1 {
		t.Errorf("use #c: got %d paths, want 1", got)
	}
}

const svgDefsGroupUse = `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><g id="a"><path id="b" d="M0 0 L10 0 L10 10 Z"/><path d="M0 0 L0 10 L10 10 Z"/></g></defs>
%s
</svg>`

func TestDefsUseGroupRendersBothPaths(t *testing.T) {
	svg := strings.Replace(svgDefsGroupUse, "%s", `<use href="#a"/>`, 1)
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(icon.SVGPaths); got != 2 {
		t.Errorf("use #a: got %d paths, want 2", got)
	}
}

func TestDefsUseChildRendersOne(t *testing.T) {
	svg := strings.Replace(svgDefsGroupUse, "%s", `<use href="#b"/>`, 1)
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(icon.SVGPaths); got != 1 {
		t.Errorf("use #b: got %d paths, want 1", got)
	}
}

func TestDefsUseIdempotent(t *testing.T) {
	svg := strings.Replace(svgDefsGroupUse, "%s", `<use href="#a"/><use href="#a"/>`, 1)
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(icon.SVGPaths); got != 4 {
		t.Errorf("use #a x2: got %d paths, want 4", got)
	}
}

func TestPatternWithIDdFirstChild(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg">
<defs><pattern id="p3" width="4" height="4" patternUnits="userSpaceOnUse"><rect id="inner" width="4" height="4" fill="red"/></pattern></defs>
<rect width="10" height="10" fill="url(#p3)"/>
</svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p, ok := icon.Patterns["p3"]
	if !ok {
		t.Fatal("pattern p3 not compiled")
	}
	if got := len(p.Paths); got != 1 {
		t.Errorf("pattern p3: got %d paths, want 1", got)
	}
}

// TestNestedDefsNotEndedEarly reproduces F6: with inDefs a plain bool, a nested
// </defs> closed the whole defs collection and cleared inDefs, so the outer
// defs' remaining children rendered directly and lost their ids. A nesting
// counter must keep defs mode open until the outermost </defs>.
func TestNestedDefsNotEndedEarly(t *testing.T) {
	svg := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><defs></defs><rect id="r" width="10" height="10" fill="red"/></defs>
</svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if _, ok := icon.Defs["r"]; !ok {
		t.Error("rect id r was lost from Defs (nested </defs> ended the outer defs early)")
	}
	if len(icon.SVGPaths) != 0 {
		t.Errorf("rect inside defs must not render directly, got %d paths", len(icon.SVGPaths))
	}

	// And <use href="#r"> must resolve the retained definition.
	svg2 := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><defs></defs><rect id="r" width="10" height="10" fill="red"/></defs>
<use href="#r"/>
</svg>`
	icon2, err, panicked := parse(t, svg2, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse(use): panicked=%v err=%v", panicked, err)
	}
	if len(icon2.SVGPaths) != 1 {
		t.Errorf("use href=#r should render exactly 1 rect, got %d paths", len(icon2.SVGPaths))
	}
}

// TestPatternChildCompileErrorNoLeak reproduces the F8 leak: a pattern tile
// child whose compile fails left its partial c.Path in the cursor (compileDefs
// returned without clearing it), so the NEXT element's geometry absorbed the
// stray tokens. The referencing rect's geometry must be exactly the rect.
func TestPatternChildCompileErrorNoLeak(t *testing.T) {
	bad := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><pattern id="p" width="10" height="10" patternUnits="userSpaceOnUse"><path d="M0 0 L 5"/></pattern></defs>
<rect x="0" y="0" width="20" height="20" fill="url(#p)"/>
</svg>`
	ref := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<rect x="0" y="0" width="20" height="20"/>
</svg>`
	badIcon, err, panicked := parse(t, bad, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("bad parse: panicked=%v err=%v", panicked, err)
	}
	refIcon, err, panicked := parse(t, ref, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("ref parse: panicked=%v err=%v", panicked, err)
	}
	if len(badIcon.SVGPaths) != 1 || len(refIcon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path each, got bad=%d ref=%d", len(badIcon.SVGPaths), len(refIcon.SVGPaths))
	}
	if !pathsEqual(badIcon.SVGPaths[0].Path, refIcon.SVGPaths[0].Path) {
		t.Errorf("pattern-child compile error leaked partial geometry into the rect:\n got:  %s\n want: %s",
			badIcon.SVGPaths[0].Path.ToSVGPath(), refIcon.SVGPaths[0].Path.ToSVGPath())
	}
}

// TestPatternChildCompileErrorStrict reproduces the F8 swallowed-error case:
// ReadPatternURL dropped the pattern-child compile error, so strict mode never
// saw it. Strict mode must now surface it.
func TestPatternChildCompileErrorStrict(t *testing.T) {
	bad := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><pattern id="p" width="10" height="10" patternUnits="userSpaceOnUse"><path d="M0 0 L 5"/></pattern></defs>
<rect x="0" y="0" width="20" height="20" fill="url(#p)"/>
</svg>`
	_, err, panicked := parse(t, bad, StrictErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err == nil {
		t.Error("strict: pattern-child compile error should surface, got nil")
	}
}

// --- T5.3 Error-mode contract --------------------------------------------

func TestStrictModeBadPath(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0 L 5"/></svg>`
	_, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err == nil {
		t.Error("strict mode: expected error for bad path data, got nil")
	}
}

func TestIgnoreModeBadStyleProperty(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><g font-size="bogus"><rect width="5" height="5"/></g></svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("ignore mode: whole doc failed on bad font-size: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Errorf("ignore mode: rect missing, got %d paths", len(icon.SVGPaths))
	}
}

func TestErrorModeMatrix(t *testing.T) {
	cases := []struct {
		name string
		svg  string
	}{
		{"unknownElement", `<svg xmlns="http://www.w3.org/2000/svg"><bogusElement/></svg>`},
		{"badPath", `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0 L 5"/></svg>`},
		{"badStyle", `<svg xmlns="http://www.w3.org/2000/svg"><rect width="5" height="5" font-size="bogus"/></svg>`},
	}
	for _, c := range cases {
		// Strict: must error.
		if _, err, p := parse(t, c.svg, StrictErrorMode); p || err == nil {
			t.Errorf("%s strict: expected error (panicked=%v)", c.name, p)
		}
		// Warn: must not error.
		if _, err, p := parse(t, c.svg, WarnErrorMode); p || err != nil {
			t.Errorf("%s warn: unexpected error %v (panicked=%v)", c.name, err, p)
		}
		// Ignore: must not error.
		if _, err, p := parse(t, c.svg, IgnoreErrorMode); p || err != nil {
			t.Errorf("%s ignore: unexpected error %v (panicked=%v)", c.name, err, p)
		}
	}
}

// TestEmptyClassRuleAllModes reproduces the F7 primary case: an empty CSS rule
// body ".a{}" is valid CSS, but parseClasses rejected it and the <style> handler
// returned the error directly, killing the document in every error mode.
func TestEmptyClassRuleAllModes(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
<defs><style>.a{}</style></defs>
<rect width="5" height="5" class="a"/>
</svg>`
	for _, m := range []ErrorMode{StrictErrorMode, WarnErrorMode, IgnoreErrorMode} {
		icon, err, panicked := parse(t, svg, m)
		if panicked {
			t.Fatalf("mode %d: panicked", m)
		}
		if err != nil {
			t.Errorf("mode %d: empty CSS rule .a{} should not error, got %v", m, err)
			continue
		}
		if len(icon.SVGPaths) != 1 {
			t.Errorf("mode %d: rect should still render, got %d paths", m, len(icon.SVGPaths))
		}
	}
}

// TestMalformedClassErrorModes reproduces the F7 error-routing case: genuinely
// malformed CSS (a rule with no braces) must abort only in strict mode and be
// tolerated (doc still parses) in warn/ignore, like every other style error.
func TestMalformedClassErrorModes(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
<defs><style>.a garbage</style></defs>
<rect width="5" height="5"/>
</svg>`
	// strict: must error.
	if _, err, p := parse(t, svg, StrictErrorMode); p || err == nil {
		t.Errorf("strict: malformed CSS should error (panicked=%v)", p)
	}
	// warn/ignore: must not error, doc still parses.
	for _, m := range []ErrorMode{WarnErrorMode, IgnoreErrorMode} {
		icon, err, p := parse(t, svg, m)
		if p || err != nil {
			t.Errorf("mode %d: malformed CSS should be tolerated, got err=%v panicked=%v", m, err, p)
			continue
		}
		if len(icon.SVGPaths) != 1 {
			t.Errorf("mode %d: rect should still render, got %d paths", m, len(icon.SVGPaths))
		}
	}
}

// --- T5.4 Style semantics ------------------------------------------------

func TestStylePrecedence(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="5" height="5" style="fill:blue" fill="red"/></svg>`
	icon, err, _ := parse(t, svg, StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("got %d paths", len(icon.SVGPaths))
	}
	got := icon.SVGPaths[0].GetFillColor()
	blue := color.NRGBA{0, 0, 0xFF, 0xFF}
	if got != color.Color(blue) {
		t.Errorf("style should win over presentation attribute: got %v, want blue", got)
	}
}

func TestClassMultiple(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
<defs><style>.a{fill:red}.b{stroke:blue;stroke-width:2}</style></defs>
<rect width="5" height="5" class="a b"/>
</svg>`
	icon, err, _ := parse(t, svg, StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("got %d paths", len(icon.SVGPaths))
	}
	sp := icon.SVGPaths[0]
	if sp.GetFillColor() != color.Color(color.NRGBA{0xFF, 0, 0, 0xFF}) {
		t.Errorf("class a fill: got %v want red", sp.GetFillColor())
	}
	if sp.GetLineColor() != color.Color(color.NRGBA{0, 0, 0xFF, 0xFF}) {
		t.Errorf("class b stroke: got %v want blue", sp.GetLineColor())
	}
}

func TestDashArrayNone(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg">
<g stroke-dasharray="4 2"><rect width="5" height="5" stroke-dasharray="none" stroke="black"/></g>
</svg>`
	icon, err, _ := parse(t, svg, StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("got %d paths", len(icon.SVGPaths))
	}
	if icon.SVGPaths[0].Dash != nil {
		t.Errorf("stroke-dasharray:none should clear dash, got %v", icon.SVGPaths[0].Dash)
	}
}

func TestOpacityClamp(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="5" height="5" fill-opacity="3" stroke-opacity="-1" stroke="black"/></svg>`
	icon, err, _ := parse(t, svg, StrictErrorMode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sp := icon.SVGPaths[0]
	if sp.FillOpacity != 1.0 {
		t.Errorf("fill-opacity=3 should clamp to 1, got %v", sp.FillOpacity)
	}
	if sp.LineOpacity != 0.0 {
		t.Errorf("stroke-opacity=-1 should clamp to 0, got %v", sp.LineOpacity)
	}
}

func TestTransformCommas(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="5" height="5" transform="translate(1,1),rotate(45)"/></svg>`
	_, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Errorf("comma-separated transform list should parse, got %v", err)
	}
}

func TestFontSizeUnits(t *testing.T) {
	mk := func(size string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg"><g font-size="10"><text font-size="` + size + `" x="0" y="0">Hi</text></g></svg>`
	}
	// The <text> style is captured in a fragment; inspect via a manual cursor
	// instead so we can read the resolved FontSize directly.
	check := func(size string, inherited, want float64) {
		c := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: &SvgIcon{}}
		cur := DefaultStyle
		cur.FontSize = inherited
		if err := c.readStyleAttr(&cur, "font-size", size); err != nil {
			t.Errorf("font-size %q: unexpected error %v", size, err)
			return
		}
		if cur.FontSize != want {
			t.Errorf("font-size %q (inherited %v): got %v want %v", size, inherited, cur.FontSize, want)
		}
	}
	check("1.2em", 10, 12)
	check("150%", 20, 30)
	check("12pt", 12, 16)
	check("14px", 10, 14)
	check("16", 10, 16)
	_ = mk

	// keyword "medium" is an ignorable property error.
	c := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: &SvgIcon{}}
	cur := DefaultStyle
	if err := c.readStyleAttr(&cur, "font-size", "medium"); err == nil {
		t.Error("font-size medium should return an error")
	}
}

// TestFontSizePhysicalUnits reproduces F14: physical/absolute font-size units
// (mm, cm, in, pc) were no longer accepted (HEAD stripped mm/cm, treating 10mm
// as 10; the current parser errors on them). They must convert via the CSS
// px-per-unit factors, while px/pt/em/rem/%/unitless stay as-is.
func TestFontSizePhysicalUnits(t *testing.T) {
	check := func(size string, want float64) {
		got, err := parseFontSize(size, 12)
		if err != nil {
			t.Errorf("parseFontSize(%q): unexpected error %v", size, err)
			return
		}
		if got != want {
			t.Errorf("parseFontSize(%q) = %v, want %v", size, got, want)
		}
	}
	check("10mm", 10*(96.0/25.4)) // ~37.795
	check("1cm", 1*(96.0/2.54))   // ~37.795
	check("1in", 1*96.0)
	check("1pc", 1*16.0)
	// Unaffected units still work.
	check("14px", 14)
	check("12pt", 12*(4.0/3.0))
	check("16", 16)

	// Strict mode accepts font-size:10mm on a real element.
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><text x="0" y="0" font-size="10mm">Hi</text></svg>`
	if _, err, panicked := parse(t, svg, StrictErrorMode); panicked || err != nil {
		t.Errorf("strict mode: font-size 10mm should be accepted, err=%v panicked=%v", err, panicked)
	}
}

// --- T5.5 Color parsing --------------------------------------------------

func TestHSLNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("hsl(1,,1) panicked: %v", r)
		}
	}()
	_, err := ParseSVGColor("hsl(1,,1)")
	if err == nil {
		t.Error("hsl(1,,1) should return an error")
	}
}

func TestHSLHueWrap(t *testing.T) {
	c1, err := ParseSVGColor("hsl(0,100%,50%)")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := ParseSVGColor("hsl(360,100%,50%)")
	if err != nil {
		t.Fatal(err)
	}
	if c1 != c2 {
		t.Errorf("hue 0 and 360 should match: %v vs %v", c1, c2)
	}
}

func TestRGBA(t *testing.T) {
	c, err := ParseSVGColor("rgba(255,0,0,0.5)")
	if err != nil {
		t.Fatal(err)
	}
	n := c.(color.NRGBA)
	if n.R != 255 || n.G != 0 || n.B != 0 || n.A < 126 || n.A > 129 {
		t.Errorf("rgba parse wrong: %+v", n)
	}
}

func TestHSLA(t *testing.T) {
	c, err := ParseSVGColor("hsla(0,100%,50%,0.5)")
	if err != nil {
		t.Fatal(err)
	}
	n := c.(color.NRGBA)
	if n.R != 255 || n.A < 126 || n.A > 129 {
		t.Errorf("hsla parse wrong: %+v", n)
	}
}

func TestTransparentColor(t *testing.T) {
	c, err := ParseSVGColor("transparent")
	if err != nil {
		t.Fatal(err)
	}
	if c != color.Color(color.NRGBA{0, 0, 0, 0}) {
		t.Errorf("transparent: got %v", c)
	}
}

func TestTransparentFillAllModes(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="5" height="5" fill="transparent"/></svg>`
	for _, m := range []ErrorMode{StrictErrorMode, WarnErrorMode, IgnoreErrorMode} {
		icon, err, panicked := parse(t, svg, m)
		if panicked || err != nil {
			t.Errorf("mode %d: fill=transparent failed err=%v panicked=%v", m, err, panicked)
			continue
		}
		if len(icon.SVGPaths) != 1 {
			t.Errorf("mode %d: got %d paths", m, len(icon.SVGPaths))
		}
	}
}

func TestParseSVGColorEmptyGuard(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseSVGColor panicked: %v", r)
		}
	}()
	if _, err := ParseSVGColor(""); err != nil {
		t.Errorf(`"" should be treated as none: %v`, err)
	}
	if _, err := ParseSVGColor("#"); err == nil {
		t.Error(`"#" should error, not panic`)
	}
}

// TestHexColorSurroundingWhitespace reproduces F15: the '#' hex branch tested
// the UNtrimmed colorStr (and passed it to ParseSVGColorNum) while every other
// branch used the trimmed value, so a hex color with surrounding whitespace
// (e.g. stop-color=" #ff0000") failed to parse.
func TestHexColorSurroundingWhitespace(t *testing.T) {
	red := color.Color(color.NRGBA{0xFF, 0, 0, 0xFF})
	for _, s := range []string{" #ff0000", "#ff0000 ", "  #FF0000  ", " #f00 "} {
		c, err := ParseSVGColor(s)
		if err != nil {
			t.Errorf("ParseSVGColor(%q): unexpected error %v", s, err)
			continue
		}
		if c != red {
			t.Errorf("ParseSVGColor(%q) = %v, want red", s, c)
		}
	}
	// And through a real document in strict mode (stop-color with leading space).
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg">
<defs><linearGradient id="g"><stop offset="0" stop-color=" #ff0000"/><stop offset="1" stop-color="#0000ff"/></linearGradient></defs>
<rect width="10" height="10" fill="url(#g)"/>
</svg>`
	if _, err, panicked := parse(t, svg, StrictErrorMode); panicked || err != nil {
		t.Errorf("strict: stop-color with leading whitespace should parse, err=%v panicked=%v", err, panicked)
	}
}

func TestURLMissingFallback(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10" fill="url(#missing)"/></svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("got %d paths", len(icon.SVGPaths))
	}
	// spec fallback for an unresolved url() paint is none (nil), not opaque black.
	if icon.SVGPaths[0].GetFillColor() == color.Color(color.NRGBA{0, 0, 0, 0xFF}) {
		t.Error("unresolved url() fill should fall back to none, not opaque black")
	}
}

func TestRGBEmptyComponentNoPanic(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="5" height="5" fill="rgb(1,,1)"/></svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("rgb(1,,1) panicked")
	}
	if err != nil {
		t.Fatalf("ignore mode should skip bad fill: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Errorf("got %d paths", len(icon.SVGPaths))
	}
}

// --- T5.6 Gradient fixes -------------------------------------------------

func TestGradientTransformNotDoubleApplied(t *testing.T) {
	svg := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><g transform="translate(5,5)"><linearGradient id="g" gradientTransform="scale(2)"><stop offset="0" stop-color="red"/></linearGradient></g></defs>
</svg>`
	icon, err, _ := parse(t, svg, IgnoreErrorMode)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, ok := icon.Grads["g"]
	if !ok {
		t.Fatal("gradient g missing")
	}
	if g.Matrix.A != 2 || g.Matrix.E != 0 || g.Matrix.F != 0 {
		t.Errorf("gradientTransform should seed from identity: got %+v", g.Matrix)
	}
}

func TestGradientHrefInheritance(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
<defs>
<linearGradient id="a"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
<linearGradient id="b" xlink:href="#a" x1="0" y1="0" x2="1" y2="0"/>
</defs>
<rect width="100" height="100" fill="url(#b)"/>
</svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, ok := icon.Grads["b"]
	if !ok {
		t.Fatal("gradient b missing")
	}
	if len(b.Stops) != 2 {
		t.Errorf("gradient b should inherit a's 2 stops, got %d", len(b.Stops))
	}
}

// TestBackwardURLToHrefGradient reproduces F2: a fill="url(#b)" where b inherits
// its stops from an earlier gradient a via href resolved, at parse time, to a
// zero-stop VALUE COPY of b (whose stops finalize() only fills in later), so the
// shape rendered black. The paint must instead defer and pick up a's stops.
func TestBackwardURLToHrefGradient(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
<defs>
<linearGradient id="a"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
<linearGradient id="b" xlink:href="#a" x1="0" y1="0" x2="1" y2="0"/>
</defs>
<rect width="100" height="100" fill="url(#b)"/>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(icon.SVGPaths))
	}
	g, ok := icon.SVGPaths[0].fillerColor.(rasterx.Gradient)
	if !ok {
		t.Fatalf("fill did not resolve to a gradient: %T", icon.SVGPaths[0].fillerColor)
	}
	if len(g.Stops) != 2 {
		t.Fatalf("fill gradient should inherit a's 2 stops, got %d", len(g.Stops))
	}
	img := renderRGBA(t, icon)
	rl, _, bl, al := rgba8(img, 5, 50)
	rr, _, br, ar := rgba8(img, 95, 50)
	if al < 200 || ar < 200 {
		t.Fatalf("gradient not painted (alpha left=%d right=%d)", al, ar)
	}
	if rl <= bl {
		t.Errorf("left edge should be reddish, not black: r=%d b=%d", rl, bl)
	}
	if br <= rr {
		t.Errorf("right edge should be bluish, not black: r=%d b=%d", rr, br)
	}
}

// TestBackwardURLToHrefGradientStroke is the stroke variant of F2.
func TestBackwardURLToHrefGradientStroke(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink">
<defs>
<linearGradient id="a"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
<linearGradient id="b" xlink:href="#a" x1="0" y1="0" x2="1" y2="0"/>
</defs>
<rect width="100" height="100" fill="none" stroke="url(#b)" stroke-width="4"/>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(icon.SVGPaths))
	}
	g, ok := icon.SVGPaths[0].linerColor.(rasterx.Gradient)
	if !ok {
		t.Fatalf("stroke did not resolve to a gradient: %T", icon.SVGPaths[0].linerColor)
	}
	if len(g.Stops) != 2 {
		t.Errorf("stroke gradient should inherit a's 2 stops, got %d", len(g.Stops))
	}
}

// TestPatternTileForwardGradientResolves reproduces F3: a pattern tile shape
// that forward-references a gradient (declared after the shape that triggers the
// pattern's compilation) kept a nil pending paint because finalize() only walked
// icon.SVGPaths, never icon.Patterns[*].Paths, so the tile rendered transparent.
func TestPatternTileForwardGradientResolves(t *testing.T) {
	svg := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<defs><pattern id="p" width="10" height="10" patternUnits="userSpaceOnUse"><rect width="10" height="10" fill="url(#g)"/></pattern></defs>
<rect width="20" height="20" fill="url(#p)"/>
<linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="red"/></linearGradient>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	p, ok := icon.Patterns["p"]
	if !ok {
		t.Fatal("pattern p not compiled")
	}
	if len(p.Paths) != 1 {
		t.Fatalf("pattern p: got %d paths, want 1", len(p.Paths))
	}
	if p.Paths[0].pendingFillURL != "" {
		t.Errorf("pattern tile pendingFillURL should be cleared after finalize, got %q", p.Paths[0].pendingFillURL)
	}
	if _, ok := p.Paths[0].fillerColor.(rasterx.Gradient); !ok {
		t.Errorf("pattern tile fill did not resolve to a gradient: %T", p.Paths[0].fillerColor)
	}
}

// --- T5.8 Top-level pattern ----------------------------------------------

func TestTopLevelPatternNotRendered(t *testing.T) {
	// The pattern's red tile lives at (0,0); the referencing rect is placed
	// elsewhere so a stray tile can't be masked by the reference's own paint.
	svg := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<pattern id="p" width="4" height="4" patternUnits="userSpaceOnUse"><rect width="4" height="4" fill="red"/></pattern>
<rect x="10" y="10" width="8" height="8" fill="url(#p)"/>
</svg>`
	// strict: element is recognized, no error.
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("strict mode should not error on top-level pattern: %v", err)
	}
	// The pattern's tile children must not leak into the shape list.
	if len(icon.SVGPaths) != 1 {
		t.Errorf("top-level pattern tile leaked as stray shapes: got %d paths, want 1", len(icon.SVGPaths))
	}
	img := renderRGBA(t, icon)
	if n := countRedPixels(img); n != 0 {
		t.Errorf("top-level pattern tile should not be drawn, found %d red pixels", n)
	}
}

// TestPatternTileClassStyle reproduces F12: compileDefs built its temporary
// dummy icon without copying origIcon.classes, so pattern tile children could
// not resolve CSS class selectors and lost those styles.
func TestPatternTileClassStyle(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg">
<defs>
<style>.red{fill:red}</style>
<pattern id="p" width="10" height="10" patternUnits="userSpaceOnUse"><rect class="red" width="10" height="10"/></pattern>
</defs>
<rect width="10" height="10" fill="url(#p)"/>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	p, ok := icon.Patterns["p"]
	if !ok {
		t.Fatal("pattern p not compiled")
	}
	if len(p.Paths) != 1 {
		t.Fatalf("pattern p: got %d paths, want 1", len(p.Paths))
	}
	if got := p.Paths[0].GetFillColor(); got != color.Color(color.NRGBA{0xFF, 0, 0, 0xFF}) {
		t.Errorf("pattern tile .red class fill: got %v, want red", got)
	}
}

// TestNestedPatternNotPaintedIntoOuter reproduces F10: compileDefs skipped only
// the "pattern"/"endpattern" marker tags, so a nested pattern's tile children
// were compiled into the OUTER pattern's tile. The whole nested span must be
// skipped; the nested pattern is still independently compilable on its own.
func TestNestedPatternNotPaintedIntoOuter(t *testing.T) {
	svg := `<svg viewBox="0 0 40 40" xmlns="http://www.w3.org/2000/svg">
<defs>
<pattern id="p1" width="20" height="20" patternUnits="userSpaceOnUse">
<rect width="20" height="20" fill="red"/>
<pattern id="p2" width="10" height="10" patternUnits="userSpaceOnUse">
<rect width="10" height="10" fill="blue"/>
<circle cx="5" cy="5" r="3" fill="green"/>
</pattern>
</pattern>
</defs>
<rect width="40" height="40" fill="url(#p1)"/>
<rect x="0" y="0" width="10" height="10" fill="url(#p2)"/>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	p1, ok := icon.Patterns["p1"]
	if !ok {
		t.Fatal("pattern p1 not compiled")
	}
	if len(p1.Paths) != 1 {
		t.Errorf("p1 tile should contain only its own rect, got %d paths (nested p2 children leaked?)", len(p1.Paths))
	}
	p2, ok := icon.Patterns["p2"]
	if !ok {
		t.Fatal("pattern p2 not independently compiled")
	}
	if len(p2.Paths) != 2 {
		t.Errorf("p2 tile should contain its rect+circle (2 paths), got %d", len(p2.Paths))
	}
}

// TestTopLevelPatternGradientRegistered reproduces F9: a top-level <pattern> is
// skipped as a shape subtree, but the skip also swallowed gradient definitions
// inside it, so a url(#lg) reference elsewhere in the document no longer
// resolved. Gradient defs must still be registered while the subtree is skipped.
func TestTopLevelPatternGradientRegistered(t *testing.T) {
	svg := `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg">
<pattern id="p" width="4" height="4" patternUnits="userSpaceOnUse">
<linearGradient id="lg" x1="0" y1="0" x2="1" y2="0"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient>
<rect width="4" height="4" fill="url(#lg)"/>
</pattern>
<rect width="20" height="20" fill="url(#lg)"/>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	g, ok := icon.Grads["lg"]
	if !ok {
		t.Fatal("gradient lg inside top-level pattern was not registered")
	}
	if len(g.Stops) != 2 {
		t.Errorf("gradient lg should have 2 stops, got %d", len(g.Stops))
	}
	// Only the document rect renders (the pattern tile rect is still skipped).
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 rendered path (the document rect), got %d", len(icon.SVGPaths))
	}
	if _, ok := icon.SVGPaths[0].fillerColor.(rasterx.Gradient); !ok {
		t.Errorf("document rect fill url(#lg) did not resolve: %T", icon.SVGPaths[0].fillerColor)
	}
}

// --- T5.9 CharData routing -----------------------------------------------

func TestTextTitleNoLeak(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg"><text x="0" y="20"><title>tip</title>Hi</text></svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked {
		t.Fatal("panicked")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(icon.Titles) != 1 || icon.Titles[0] != "tip" {
		t.Errorf("title: got %v", icon.Titles)
	}
	// "tip" must not have leaked into the text render. Compare against a doc
	// whose text is exactly "Hi": both should produce identical glyph output.
	ref, _, _ := parse(t, `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg"><text x="0" y="20">Hi</text></svg>`, IgnoreErrorMode)
	if len(icon.SVGPaths) != len(ref.SVGPaths) {
		t.Errorf("title leaked into text: got %d glyph paths, reference %d", len(icon.SVGPaths), len(ref.SVGPaths))
	}
}

func TestAppendTextChunkWhitespace(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"\n    Hello\n  ", " Hello "}, // pretty-printed indentation collapses
		{"Hi", "Hi"},                   // plain text unchanged
		{"a\tb", "a b"},                // tab -> single space
		{"a   b", "a b"},               // space run collapsed
		{"\n\t \n", " "},               // whitespace-only collapses to one space
	}
	for _, tc := range cases {
		c := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: &SvgIcon{}}
		c.inText = true
		c.appendTextChunk([]byte(tc.in))
		if len(c.textFragments) != 1 {
			t.Errorf("appendTextChunk(%q): got %d fragments, want 1", tc.in, len(c.textFragments))
			continue
		}
		got := c.textFragments[0].text
		if got != tc.want {
			t.Errorf("appendTextChunk(%q): got %q, want %q", tc.in, got, tc.want)
		}
		if strings.ContainsAny(got, "\n\r\t") {
			t.Errorf("appendTextChunk(%q): result %q still contains raw whitespace", tc.in, got)
		}
	}

	// An empty result must not append a fragment.
	c := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: &SvgIcon{}}
	c.inText = true
	c.appendTextChunk([]byte(""))
	if len(c.textFragments) != 0 {
		t.Errorf("empty chunk should not append a fragment, got %d", len(c.textFragments))
	}
}

func TestDefsBalancedMarkers(t *testing.T) {
	// The panic input's group def list must be endg-balanced and complete.
	icon, err, panicked := parse(t, svgNestedDefsPanic, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse failed: err=%v panicked=%v", err, panicked)
	}
	balanced := func(id string) {
		defs, ok := icon.Defs[id]
		if !ok {
			t.Errorf("Defs[%q] missing", id)
			return
		}
		open, closes := 0, 0
		for _, d := range defs {
			switch d.Tag {
			case "g", "pattern":
				open++
			case "endg", "endpattern":
				closes++
			}
		}
		if open != closes {
			t.Errorf("Defs[%q] unbalanced: %d group starts vs %d end markers (%v)", id, open, closes, defs)
		}
	}
	balanced("a") // 2 group starts, 2 endg
	balanced("b") // 1 group start, 1 endg
	// c is a bare path: its own def only.
	if got := len(icon.Defs["c"]); got != 1 {
		t.Errorf("Defs[\"c\"] should hold just the path def, got %d entries", got)
	}
}
