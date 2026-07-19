// Copyright 2026 The oksvg Authors. All rights reserved.
//
// text_layout_test.go covers the T7 text-layout refactor: single-application of
// dx/dy, per-chunk text-anchor, cross-fragment whitespace trimming, .notdef
// advance, measurement/layout parity for the sbix path, forward paint
// references, and the fill/stroke-opacity replace vs group-opacity multiply
// semantics.

package oksvg

import (
	"math"
	"testing"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

// textBounds unions the user-space bounding boxes of every glyph path in the
// icon. It fails the test if no glyph paths were produced.
func textBounds(t *testing.T, icon *SvgIcon) ObjectBounds {
	t.Helper()
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	any := false
	for _, sp := range icon.SVGPaths {
		b, ok := pathBounds(sp.Path)
		if !ok {
			continue
		}
		any = true
		if b.X < minX {
			minX = b.X
		}
		if b.Y < minY {
			minY = b.Y
		}
		if b.X+b.W > maxX {
			maxX = b.X + b.W
		}
		if b.Y+b.H > maxY {
			maxY = b.Y + b.H
		}
	}
	if !any {
		t.Fatal("no glyph paths produced")
	}
	return ObjectBounds{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}
}

// --- T7.2 dx/dy single application ---------------------------------------

// TestTextDxDySingleApplication checks that a <text> element's dx/dy is applied
// exactly once. Historically the first fragment's dx/dy was folded into the pen
// seed AND re-added in the layout loop, doubling it. So <text dx="10" dy="5">
// must place its glyph at the same spot as <text x="10" y="5">.
func TestTextDxDySingleApplication(t *testing.T) {
	dxIcon, err, panicked := parse(t,
		`<svg xmlns="http://www.w3.org/2000/svg"><text dx="10" dy="5" font-family="sans-serif" font-size="30">X</text></svg>`,
		StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("dx/dy parse: panicked=%v err=%v", panicked, err)
	}
	refIcon, err, panicked := parse(t,
		`<svg xmlns="http://www.w3.org/2000/svg"><text x="10" y="5" font-family="sans-serif" font-size="30">X</text></svg>`,
		StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("x/y parse: panicked=%v err=%v", panicked, err)
	}

	dxB := textBounds(t, dxIcon)
	refB := textBounds(t, refIcon)
	if math.Abs(dxB.X-refB.X) > 1.0 || math.Abs(dxB.Y-refB.Y) > 1.0 {
		t.Errorf("dx/dy applied more than once: dx/dy bbox origin (%.2f,%.2f) != x/y bbox origin (%.2f,%.2f)",
			dxB.X, dxB.Y, refB.X, refB.Y)
	}
}

// --- T7.2 per-chunk text-anchor ------------------------------------------

// TestTextPerChunkAnchor checks that with text-anchor="middle" each explicit-x
// run (chunk) is centered on its own x, instead of one block-level shift derived
// from the whole run.
func TestTextPerChunkAnchor(t *testing.T) {
	icon, err, panicked := parse(t,
		`<svg xmlns="http://www.w3.org/2000/svg"><text text-anchor="middle" font-family="sans-serif" font-size="20"><tspan x="50">ab</tspan><tspan x="150">cd</tspan></text></svg>`,
		StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 2 {
		t.Fatalf("expected 2 glyph paths (one per chunk), got %d", len(icon.SVGPaths))
	}
	center := func(p rasterx.Path) float64 {
		b, ok := pathBounds(p)
		if !ok {
			t.Fatal("empty chunk path")
		}
		return b.X + b.W/2
	}
	c0 := center(icon.SVGPaths[0].Path)
	c1 := center(icon.SVGPaths[1].Path)
	if math.Abs(c0-50) > 15 {
		t.Errorf("first chunk not centered on x=50: center=%.2f", c0)
	}
	if math.Abs(c1-150) > 15 {
		t.Errorf("second chunk not centered on x=150: center=%.2f", c1)
	}
}

// --- T7.2 cross-fragment whitespace --------------------------------------

// TestTextLeadingWhitespaceTrimmed checks that leading whitespace introduced by
// pretty-printing the SVG does not offset the text: the first non-empty
// fragment's leading spaces are trimmed.
func TestTextLeadingWhitespaceTrimmed(t *testing.T) {
	pretty, err, panicked := parse(t,
		"<svg xmlns=\"http://www.w3.org/2000/svg\"><text x=\"0\" y=\"20\" font-family=\"sans-serif\" font-size=\"20\">\n    Hello\n  </text></svg>",
		StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("pretty parse: panicked=%v err=%v", panicked, err)
	}
	ref, err, panicked := parse(t,
		`<svg xmlns="http://www.w3.org/2000/svg"><text x="0" y="20" font-family="sans-serif" font-size="20">Hello</text></svg>`,
		StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("ref parse: panicked=%v err=%v", panicked, err)
	}
	pb := textBounds(t, pretty)
	rb := textBounds(t, ref)
	if math.Abs(pb.X-rb.X) > 1.0 {
		t.Errorf("leading whitespace not trimmed: pretty bbox X=%.2f, ref bbox X=%.2f", pb.X, rb.X)
	}
}

// TestTextBoundaryWhitespaceCollapses checks that whitespace straddling a
// fragment boundary collapses to a single space: an internal newline+indent
// between text and a <tspan>, and an explicit space on both sides of the
// boundary, both render the same width as a single-space reference.
func TestTextBoundaryWhitespaceCollapses(t *testing.T) {
	ref, err, panicked := parse(t,
		`<svg xmlns="http://www.w3.org/2000/svg"><text x="0" y="20" font-family="sans-serif" font-size="20">Hello <tspan>World</tspan></text></svg>`,
		StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("ref parse: panicked=%v err=%v", panicked, err)
	}
	refW := textBounds(t, ref).W

	cases := map[string]string{
		"newline+indent boundary": "<svg xmlns=\"http://www.w3.org/2000/svg\"><text x=\"0\" y=\"20\" font-family=\"sans-serif\" font-size=\"20\">Hello\n    <tspan>World</tspan></text></svg>",
		"double space boundary":   `<svg xmlns="http://www.w3.org/2000/svg"><text x="0" y="20" font-family="sans-serif" font-size="20">Hello <tspan> World</tspan></text></svg>`,
	}
	for name, svg := range cases {
		icon, err, panicked := parse(t, svg, StrictErrorMode)
		if panicked || err != nil {
			t.Fatalf("%s parse: panicked=%v err=%v", name, panicked, err)
		}
		w := textBounds(t, icon).W
		if math.Abs(w-refW) > 1.0 {
			t.Errorf("%s: width %.2f != single-space reference width %.2f", name, w, refW)
		}
	}
}

// --- T7.2 .notdef advance ------------------------------------------------

// TestTextNotdefAdvance checks that a rune present in no font (U+FFFF, a
// permanent noncharacter) is laid out as the fragment font's .notdef glyph with
// a non-zero advance, rather than being skipped zero-width.
func TestTextNotdefAdvance(t *testing.T) {
	c := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: &SvgIcon{}}
	style := DefaultStyle
	style.FontFamily = "sans-serif"
	frags := []textFragment{{text: "￿", style: style}}

	var buf sfnt.Buffer
	var visited []glyphVisit
	endX, _ := c.walkTextGlyphs(frags, 0, 0, 0, &buf, func(g glyphVisit) {
		visited = append(visited, g)
	})

	if len(visited) != 1 {
		t.Fatalf("expected exactly 1 glyph visit for U+FFFF, got %d", len(visited))
	}
	if visited[0].idx != 0 {
		t.Errorf("U+FFFF should resolve to .notdef (idx 0), got idx %d", visited[0].idx)
	}
	if endX <= 0 {
		t.Errorf(".notdef must advance the pen (>0), got endX=%.4f", endX)
	}
}

// --- T7.2 measurement == layout on the sbix (bitmap) path ----------------

// TestTextSbixMeasurementEqualsLayout checks that the measurement pass and the
// layout pass agree on the pen advance for a color-bitmap (sbix) glyph, so an
// emoji does not shift under anchoring. Skipped when the runner has no emoji
// font, or when the emoji glyph does not take the bitmap path there.
func TestTextSbixMeasurementEqualsLayout(t *testing.T) {
	LoadSystemFonts()
	if registryFont("emoji") == nil {
		t.Skip("no emoji font registered on this runner")
	}

	c := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: &SvgIcon{}}
	style := DefaultStyle
	style.FontFamily = "sans-serif"
	style.FontSize = 32
	frags := []textFragment{{text: "\U0001F600", style: style}} // grinning face

	var buf sfnt.Buffer
	measEndX, _ := c.walkTextGlyphs(frags, 0, 0, 0, &buf, func(glyphVisit) {})

	em := textEmitter{c: c, buf: &buf}
	layoutEndX, _ := c.walkTextGlyphs(frags, 0, 0, 0, &buf, em.emit)
	em.flush()

	if len(c.icon.SVGImages) == 0 {
		t.Skip("emoji glyph did not take the sbix bitmap path on this runner")
	}
	if math.Abs(measEndX-layoutEndX) > 1e-9 {
		t.Errorf("sbix measurement advance %.6f != layout advance %.6f", measEndX, layoutEndX)
	}
}

// --- T7.3 forward paint references ---------------------------------------

// TestForwardFillReference checks that a fill="url(#g)" that references a
// gradient declared LATER in the document resolves at finalize() and renders the
// gradient (rather than falling back to none / transparent).
func TestForwardFillReference(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
  <rect width="100" height="100" fill="url(#g)"/>
  <linearGradient id="g" x1="0" y1="0" x2="1" y2="0">
    <stop offset="0" stop-color="#FF0000"/>
    <stop offset="1" stop-color="#0000FF"/>
  </linearGradient>
</svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(icon.SVGPaths))
	}
	if _, ok := icon.SVGPaths[0].fillerColor.(rasterx.Gradient); !ok {
		t.Fatalf("forward fill ref not resolved to a gradient: %T", icon.SVGPaths[0].fillerColor)
	}
	if icon.SVGPaths[0].pendingFillURL != "" {
		t.Errorf("pendingFillURL should be cleared after finalize, got %q", icon.SVGPaths[0].pendingFillURL)
	}

	img := renderRGBA(t, icon)
	rl, gl, bl, al := rgba8(img, 5, 50)
	rr, gr, br, ar := rgba8(img, 95, 50)
	if al < 200 || ar < 200 {
		t.Fatalf("gradient not painted (alpha left=%d right=%d)", al, ar)
	}
	if !(rl > bl) {
		t.Errorf("left edge should be reddish: rgba=(%d,%d,%d,%d)", rl, gl, bl, al)
	}
	if !(br > rr) {
		t.Errorf("right edge should be bluish: rgba=(%d,%d,%d,%d)", rr, gr, br, ar)
	}
}

// TestForwardFillReferenceUnresolved checks that a fill="url(#missing)" that
// never resolves degrades to none (nil fill) rather than erroring or panicking.
func TestForwardFillReferenceUnresolved(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10" fill="url(#missing)"/></svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(icon.SVGPaths))
	}
	if icon.SVGPaths[0].fillerColor != nil {
		t.Errorf("unresolved fill ref should be none (nil), got %v", icon.SVGPaths[0].fillerColor)
	}
	if icon.SVGPaths[0].pendingFillURL != "" {
		t.Errorf("pendingFillURL should be cleared after finalize, got %q", icon.SVGPaths[0].pendingFillURL)
	}
}

// --- T7.3 opacity semantics ----------------------------------------------

// TestFillOpacityReplaces checks that fill-opacity SETS (replaces) FillOpacity
// rather than inherit-multiplying: nested fill-opacity="0.5" groups must not
// compound to 0.25.
func TestFillOpacityReplaces(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg"><g fill-opacity="0.5"><g fill-opacity="0.5"><rect width="10" height="10" fill="#000000"/></g></g></svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(icon.SVGPaths))
	}
	if got := icon.SVGPaths[0].FillOpacity; math.Abs(got-0.5) > 1e-9 {
		t.Errorf("nested fill-opacity should replace to 0.5, got %.4f (compounded?)", got)
	}
	img := renderRGBA(t, icon)
	_, _, _, a := rgba8(img, 5, 5)
	if a < 110 || a > 145 {
		t.Errorf("fill-opacity 0.5 -> alpha %d, want ~128 (not ~64)", a)
	}
}

// TestGroupOpacityMultipliesFillOpacity checks that plain opacity accumulates
// into groupOpacity (group compositing approximation) while a child's
// fill-opacity="1" replaces FillOpacity, so the effective alpha is still 0.5.
func TestGroupOpacityMultipliesFillOpacity(t *testing.T) {
	svg := `<svg viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg"><g opacity="0.5"><rect width="10" height="10" fill="#000000" fill-opacity="1"/></g></svg>`
	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(icon.SVGPaths))
	}
	sp := icon.SVGPaths[0]
	if math.Abs(sp.FillOpacity-1.0) > 1e-9 {
		t.Errorf("child fill-opacity=1 should set FillOpacity=1, got %.4f", sp.FillOpacity)
	}
	if math.Abs(sp.groupOpacity-0.5) > 1e-9 {
		t.Errorf("parent opacity=0.5 should set groupOpacity=0.5, got %.4f", sp.groupOpacity)
	}
	img := renderRGBA(t, icon)
	_, _, _, a := rgba8(img, 5, 5)
	if a < 110 || a > 145 {
		t.Errorf("effective opacity should be ~0.5 -> alpha %d, want ~128", a)
	}
}

// TestLoadSystemFontsDoesNotClobberUserFont reproduces F4: a font the caller
// registered explicitly (e.g. a custom "emoji" font) was silently overwritten
// when the lazy system-font loader ran on the first <text> layout. loadSystemFonts
// must register a name only when it is not already present.
func TestLoadSystemFontsDoesNotClobberUserFont(t *testing.T) {
	// Snapshot and restore the "emoji" registration so this test doesn't perturb
	// the shared font registry for other tests.
	fontRegistryMu.RLock()
	prevFont := fontRegistry["emoji"]
	prevBytes := fontBytesRegistry["emoji"]
	prevIdx := fontIndexRegistry["emoji"]
	hadEmoji := prevFont != nil
	fontRegistryMu.RUnlock()
	t.Cleanup(func() {
		fontRegistryMu.Lock()
		if hadEmoji {
			fontRegistry["emoji"] = prevFont
			fontBytesRegistry["emoji"] = prevBytes
			fontIndexRegistry["emoji"] = prevIdx
		} else {
			delete(fontRegistry, "emoji")
			delete(fontBytesRegistry, "emoji")
			delete(fontIndexRegistry, "emoji")
		}
		fontRegistryMu.Unlock()
	})

	// Register a fake "emoji" font (the built-in goregular bytes parse fine).
	if err := RegisterFont("emoji", goregular.TTF); err != nil {
		t.Fatalf("RegisterFont(emoji) failed: %v", err)
	}
	fake := registryFont("emoji")
	if fake == nil {
		t.Fatal("fake emoji font not registered")
	}

	// Force the lazy system-font loader to run its full logic (bypassing the
	// sync.Once, which an earlier test may already have consumed).
	loadSystemFonts()

	if got := registryFont("emoji"); got != fake {
		t.Error("loadSystemFonts clobbered the user-registered emoji font")
	}
}

// TestLoadSystemFontsIdempotent ensures LoadSystemFonts is safe to call
// repeatedly (sync.Once) and always leaves the built-in gofont faces registered.
func TestLoadSystemFontsIdempotent(t *testing.T) {
	LoadSystemFonts()
	LoadSystemFonts()
	if registryFont("default") == nil {
		t.Error("default (goregular) face missing after LoadSystemFonts")
	}
}
