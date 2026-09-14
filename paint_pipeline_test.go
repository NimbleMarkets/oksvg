// Copyright 2026 The oksvg Authors. All rights reserved.
//
// paint_pipeline_test.go covers T4: gradient coordinate spaces, pattern
// tiling under transforms, SetTarget math, draw-time concurrency safety,
// z-order plumbing, and useF hardening.

package oksvg

import (
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// renderSVG parses svgStr and renders it into a w x h RGBA image using
// SetTarget so the icon fills the target rectangle.
func renderSVG(t *testing.T, svgStr string, w, h int, mode ErrorMode) *image.RGBA {
	t.Helper()
	icon, err := ReadIconStream(strings.NewReader(svgStr), mode)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.SetTarget(0, 0, float64(w), float64(h))
	icon.Draw(raster, 1.0)
	return img
}

// rgba8 returns 8-bit r,g,b,a for the pixel at (x,y).
func rgba8(img image.Image, x, y int) (r, g, b, a uint8) {
	cr, cg, cb, ca := img.At(x, y).RGBA()
	return uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), uint8(ca >> 8)
}

func isRed(img image.Image, x, y int) bool {
	r, g, b, a := rgba8(img, x, y)
	return r > 200 && g < 80 && b < 80 && a > 200
}

// TestSetTargetNonZeroViewBoxOrigin verifies that a non-zero viewBox origin is
// translated correctly. viewBox="100 0 100 100" with SetTarget(0,0,200,200)
// must map user (100,0)->(0,0) and (200,100)->(200,200).
func TestSetTargetNonZeroViewBoxOrigin(t *testing.T) {
	icon := &SvgIcon{Transform: rasterx.Identity}
	icon.ViewBox.X = 100
	icon.ViewBox.Y = 0
	icon.ViewBox.W = 100
	icon.ViewBox.H = 100

	icon.SetTarget(0, 0, 200, 200)

	const eps = 1e-9
	if x, y := icon.Transform.Transform(100, 0); absDiff(x, 0) > eps || absDiff(y, 0) > eps {
		t.Errorf("(100,0) mapped to (%v,%v), want (0,0)", x, y)
	}
	if x, y := icon.Transform.Transform(200, 100); absDiff(x, 200) > eps || absDiff(y, 200) > eps {
		t.Errorf("(200,100) mapped to (%v,%v), want (200,200)", x, y)
	}
}

// TestSetTargetZeroViewBoxDimensions guards against Inf/NaN when a viewBox axis
// is zero.
func TestSetTargetZeroViewBoxDimensions(t *testing.T) {
	icon := &SvgIcon{Transform: rasterx.Identity}
	icon.ViewBox.W = 0
	icon.ViewBox.H = 0
	icon.SetTarget(0, 0, 100, 100)
	m := icon.Transform
	for _, v := range []float64{m.A, m.B, m.C, m.D, m.E, m.F} {
		if isNaNOrInf(v) {
			t.Fatalf("SetTarget with zero viewBox produced non-finite matrix: %+v", m)
		}
	}
}

// TestUserSpaceOnUseGradientRespectsTransform verifies a userSpaceOnUse linear
// gradient scales with the draw transform. Rendered at 2x, the gradient must
// transition across the full device width (midpoint is a real blend), not be
// frozen to the unscaled user space.
func TestUserSpaceOnUseGradientRespectsTransform(t *testing.T) {
	const svg = `<svg width="100" height="100" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="100" y2="0" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#FF0000"/>
      <stop offset="1" stop-color="#0000FF"/>
    </linearGradient>
  </defs>
  <rect x="0" y="0" width="100" height="100" fill="url(#g)"/>
</svg>`
	img := renderSVG(t, svg, 200, 200, StrictErrorMode)

	// Near start: red dominant.
	if r, _, b, _ := rgba8(img, 10, 100); r < 200 || b > 60 {
		t.Errorf("(10,100) = r%d b%d, want near-start red", r, b)
	}
	// Near end: blue dominant.
	if r, _, b, _ := rgba8(img, 190, 100); b < 200 || r > 60 {
		t.Errorf("(190,100) = r%d b%d, want near-end blue", r, b)
	}
	// Midpoint: a real blend of red and blue. This is what fails when the
	// gradient is frozen to unscaled space (it would be fully blue there).
	if r, _, b, _ := rgba8(img, 100, 100); r < 60 || b < 60 {
		t.Errorf("(100,100) = r%d b%d, want a red/blue blend (transform-scaled gradient)", r, b)
	}
}

// TestObjectBoundingBoxGradientRespectsTransform verifies an objectBoundingBox
// linear gradient scales with the draw transform. Rendered at 2x, the red->blue
// transition must span the full device width: device x=50 (25% of the object)
// leans red, the midpoint is a blend, and device x=150 (75%) leans blue.
//
// Note: the brief's prescribed mechanism (user-space Bounds +
// GetColorFunctionUS(opacity, madder.M)) does NOT achieve this for
// objectBoundingBox, because the pinned rasterx ignores objMatrix in that
// branch and samples in device space. This test locks in the corrected
// device-space-bounds handling. See task report for details.
func TestObjectBoundingBoxGradientRespectsTransform(t *testing.T) {
	const svg = `<svg width="100" height="100" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="1" y2="0" gradientUnits="objectBoundingBox">
      <stop offset="0" stop-color="#FF0000"/>
      <stop offset="1" stop-color="#0000FF"/>
    </linearGradient>
  </defs>
  <rect x="0" y="0" width="100" height="100" fill="url(#g)"/>
</svg>`
	img := renderSVG(t, svg, 200, 200, StrictErrorMode)

	if r, _, b, _ := rgba8(img, 50, 100); r <= b {
		t.Errorf("(50,100) = r%d b%d, want red-leaning (25%% across object)", r, b)
	}
	if r, _, b, _ := rgba8(img, 100, 100); r < 60 || b < 60 {
		t.Errorf("(100,100) = r%d b%d, want a red/blue blend (transform-scaled gradient)", r, b)
	}
	if r, _, b, _ := rgba8(img, 150, 100); b <= r {
		t.Errorf("(150,100) = r%d b%d, want blue-leaning (75%% across object)", r, b)
	}
}

// TestObjectBoundingBoxPatternTilesUnderTransform renders an objectBoundingBox
// pattern (w=h=0.5) on a 50x50 rect at 2x SetTarget. The result must tile 2x2.
func TestObjectBoundingBoxPatternTilesUnderTransform(t *testing.T) {
	const svg = `<svg width="50" height="50" viewBox="0 0 50 50" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <pattern id="p" width="0.5" height="0.5" patternUnits="objectBoundingBox">
      <rect x="0" y="0" width="12.5" height="12.5" fill="#FF0000"/>
    </pattern>
  </defs>
  <rect x="0" y="0" width="50" height="50" fill="url(#p)"/>
</svg>`
	// Render at 2x -> 100x100 device. Each tile is 50 device px, with a red
	// 25px square in its top-left quadrant. Expect a 2x2 grid of red marks.
	img := renderSVG(t, svg, 100, 100, StrictErrorMode)

	// Tile-mark centers (top-left quadrant of each 50px tile).
	markCenters := [][2]int{{6, 6}, {56, 6}, {6, 56}, {56, 56}}
	for _, p := range markCenters {
		if !isRed(img, p[0], p[1]) {
			r, g, b, a := rgba8(img, p[0], p[1])
			t.Errorf("expected red tile mark at (%d,%d), got r%d g%d b%d a%d", p[0], p[1], r, g, b, a)
		}
	}
	// Bottom-right quadrant of each tile must be empty (no mark).
	empties := [][2]int{{40, 40}, {90, 40}, {40, 90}, {90, 90}}
	for _, p := range empties {
		if isRed(img, p[0], p[1]) {
			t.Errorf("unexpected red at (%d,%d); tiling is wrong", p[0], p[1])
		}
	}
}

// TestConcurrentDrawPatternAndGradient renders the same icon (with a gradient
// fill and a pattern fill) from two goroutines into separate images. Run under
// -race to catch shared-state mutation. Both outputs must be non-blank.
func TestConcurrentDrawPatternAndGradient(t *testing.T) {
	const svg = `<svg width="100" height="100" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <linearGradient id="g" x1="0" y1="0" x2="100" y2="0" gradientUnits="userSpaceOnUse">
      <stop offset="0" stop-color="#FF0000"/>
      <stop offset="1" stop-color="#0000FF"/>
    </linearGradient>
    <pattern id="p" width="20" height="20" patternUnits="userSpaceOnUse">
      <rect x="0" y="0" width="10" height="10" fill="#00FF00"/>
    </pattern>
  </defs>
  <rect x="0" y="0" width="50" height="100" fill="url(#g)"/>
  <rect x="50" y="0" width="50" height="100" fill="url(#p)"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	const n = 2
	imgs := make([]*image.RGBA, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			img := image.NewRGBA(image.Rect(0, 0, 100, 100))
			scanner := rasterx.NewScannerGV(100, 100, img, img.Bounds())
			raster := rasterx.NewDasher(100, 100, scanner)
			icon.Draw(raster, 1.0)
			imgs[idx] = img
		}(i)
	}
	wg.Wait()

	for i, img := range imgs {
		if blank(img) {
			t.Errorf("goroutine %d produced a blank image", i)
		}
	}
}

// TestUseDefsNestedNoPanic parses use referencing a path nested in a group in
// defs. In this worktree we only assert it does not panic; full correctness is
// validated after T5 integration.
func TestUseDefsNestedNoPanic(t *testing.T) {
	const svg = `<svg width="10" height="10" viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg">
  <defs><g id="a"><path id="b" d="M0 0h5v5z"/></g></defs>
  <use href="#b"/>
  <use href="#b"/>
</svg>`
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parsing use/defs panicked: %v", r)
		}
	}()
	if _, err := ReadIconStream(strings.NewReader(svg), WarnErrorMode); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestUseCycleNoStackOverflow reproduces F1: a cyclic <use> chain (self
// reference #a->#a, or mutual #a->#b->#a) drove useF into unbounded recursion
// and crashed the process with a fatal stack overflow in every error mode. The
// cycle must instead be detected and routed through the error-mode policy
// (strict -> error, warn/ignore -> skip), never recurse to overflow.
func TestUseCycleNoStackOverflow(t *testing.T) {
	cases := map[string]string{
		"self-cycle":   `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`,
		"mutual-cycle": `<svg viewBox="0 0 20 20" xmlns="http://www.w3.org/2000/svg"><defs><g id="a"><use href="#b"/></g><g id="b"><use href="#a"/></g></defs><use href="#a"/></svg>`,
	}
	for name, svg := range cases {
		t.Run(name, func(t *testing.T) {
			// Non-strict modes: must not crash/overflow and must not error.
			for _, m := range []ErrorMode{IgnoreErrorMode, WarnErrorMode} {
				_, err, panicked := parse(t, svg, m)
				if panicked {
					t.Fatalf("mode %d: cyclic use panicked/overflowed", m)
				}
				if err != nil {
					t.Errorf("mode %d: cyclic use should be skipped, got err %v", m, err)
				}
			}
			// Strict mode: must surface an error, not crash.
			_, err, panicked := parse(t, svg, StrictErrorMode)
			if panicked {
				t.Fatalf("strict: cyclic use panicked/overflowed")
			}
			if err == nil {
				t.Error("strict: cyclic use should return an error")
			}
		})
	}
}

// TestUseTitleDoesNotLeakMode reproduces F5: replaying a used group that
// contains a <title> ran titleF, which sets inTitleText=true with no matching
// EndElement in the replay loop, so the flag stayed on and later document text
// ("Hi") was swallowed into Titles instead of being rendered as glyphs.
func TestUseTitleDoesNotLeakMode(t *testing.T) {
	svg := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
<defs><g id="a"><title>grouptip</title><rect width="10" height="10" fill="red"/></g></defs>
<use href="#a"/>
<text x="0" y="40" font-family="sans-serif" font-size="20">Hi</text>
</svg>`
	icon, err, panicked := parse(t, svg, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("parse: panicked=%v err=%v", panicked, err)
	}
	// Reference: identical but with no <title> to leak. Both must produce the
	// same paths (used rect + rendered "Hi" glyphs).
	ref := `<svg viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
<defs><g id="a"><rect width="10" height="10" fill="red"/></g></defs>
<use href="#a"/>
<text x="0" y="40" font-family="sans-serif" font-size="20">Hi</text>
</svg>`
	refIcon, err, panicked := parse(t, ref, IgnoreErrorMode)
	if panicked || err != nil {
		t.Fatalf("ref parse: panicked=%v err=%v", panicked, err)
	}
	if len(icon.SVGPaths) != len(refIcon.SVGPaths) {
		t.Errorf("title in used group leaked mode: got %d paths, reference %d (text glyphs vanished?)",
			len(icon.SVGPaths), len(refIcon.SVGPaths))
	}
	// "Hi" must not have leaked into Titles.
	for _, title := range icon.Titles {
		if strings.Contains(title, "Hi") {
			t.Errorf("document text leaked into Titles: %q", icon.Titles)
		}
	}
}

// TestTwoPointPolylineProducesLine verifies that a 2-vertex polyline is no
// longer dropped and produces a single line segment.
func TestTwoPointPolylineProducesLine(t *testing.T) {
	const svg = `<svg width="100" height="100" viewBox="0 0 100 100" xmlns="http://www.w3.org/2000/svg">
  <polyline points="0,0 10,10" stroke="#000000" fill="none"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(icon.SVGPaths) != 1 {
		t.Fatalf("expected 1 SvgPath, got %d", len(icon.SVGPaths))
	}
	moves, lines := countPathOps(icon.SVGPaths[0].Path)
	if moves != 1 || lines != 1 {
		t.Errorf("2-point polyline produced moves=%d lines=%d, want moves=1 lines=1", moves, lines)
	}
}

// TestPathBounds verifies the user-space bounding box walk: the empty-path
// case, a polyline, and that curve extents are exact rather than the control
// hull (the SVG object bounding box excludes control points).
func TestPathBounds(t *testing.T) {
	var p rasterx.Path
	if _, ok := pathBounds(p); ok {
		t.Error("empty path should report ok=false")
	}

	p = rasterx.Path{}
	p.Start(fixed.Point26_6{X: 10 * 64, Y: 20 * 64})
	p.Line(fixed.Point26_6{X: 40 * 64, Y: 20 * 64})
	p.Line(fixed.Point26_6{X: 40 * 64, Y: 60 * 64})
	p.Stop(true)
	b, ok := pathBounds(p)
	if !ok {
		t.Fatal("non-empty path should report ok=true")
	}
	if b.X != 10 || b.Y != 20 || b.W != 30 || b.H != 40 {
		t.Errorf("bounds = %+v, want {X:10 Y:20 W:30 H:40}", b)
	}

	// Cubic (0,0) -> (50,50) with handles (100,-50) and (200,150): the control
	// hull is {0,-50,200,200} but the curve itself only reaches
	// x = 126.49 (t = sqrt(0.4)) and y in [-8.168, 78.416] (t = 0.1144, 0.7947).
	var q rasterx.Path
	q.Start(fixed.Point26_6{X: 0, Y: 0})
	q.CubeBezier(
		fixed.Point26_6{X: 100 * 64, Y: -50 * 64},
		fixed.Point26_6{X: 200 * 64, Y: 150 * 64},
		fixed.Point26_6{X: 50 * 64, Y: 50 * 64})
	b2, ok := pathBounds(q)
	if !ok {
		t.Fatal("cubic path should report ok=true")
	}
	want := ObjectBounds{X: 0, Y: -8.16794, W: 126.49111, H: 86.58381}
	near := func(a, b float64) bool { return a-b < 1e-4 && b-a < 1e-4 }
	if !near(b2.X, want.X) || !near(b2.Y, want.Y) || !near(b2.W, want.W) || !near(b2.H, want.H) {
		t.Errorf("cubic bounds = %+v, want %+v (tight curve extent, not control hull)", b2, want)
	}

	// Closing a subpath moves the current point back to its start, so a curve
	// that follows Z starts from (0,0), not from the last vertex (100,100).
	// Cubic (0,0) -> (0,0) with both handles at (-40,0) reaches x = -30 (t=0.5);
	// from (100,100) it would only reach about x = -23.
	var z rasterx.Path
	z.Start(fixed.Point26_6{X: 0, Y: 0})
	z.Line(fixed.Point26_6{X: 100 * 64, Y: 0})
	z.Line(fixed.Point26_6{X: 100 * 64, Y: 100 * 64})
	z.Stop(true)
	z.CubeBezier(fixed.Point26_6{X: -40 * 64, Y: 0}, fixed.Point26_6{X: -40 * 64, Y: 0}, fixed.Point26_6{X: 0, Y: 0})
	bz, _ := pathBounds(z)
	if !near(bz.X, -30) || !near(bz.W, 130) {
		t.Errorf("bounds after Z = %+v, want X=-30 W=130 (curve must start at the subpath origin)", bz)
	}

	// Quadratic (0,0) -> (100,0) with handle (50,100) peaks at y = 50 (t = 0.5).
	var r rasterx.Path
	r.Start(fixed.Point26_6{X: 0, Y: 0})
	r.QuadBezier(fixed.Point26_6{X: 50 * 64, Y: 100 * 64}, fixed.Point26_6{X: 100 * 64, Y: 0})
	b3, _ := pathBounds(r)
	if b3.X != 0 || b3.Y != 0 || b3.W != 100 || b3.H != 50 {
		t.Errorf("quad bounds = %+v, want {X:0 Y:0 W:100 H:50}", b3)
	}
}

// TestNextOrderMonotonic verifies the draw-order counter is a simple
// post-incrementing sequence.
func TestNextOrderMonotonic(t *testing.T) {
	s := &SvgIcon{}
	for want := 0; want < 5; want++ {
		if got := s.nextOrder(); got != want {
			t.Fatalf("nextOrder() = %d, want %d", got, want)
		}
	}
}

// TestLinearGradientEmptyIDErrors verifies that a gradient with an empty id
// attribute is rejected (the fix for the always-true len(id) >= 0 check).
func TestLinearGradientEmptyIDErrors(t *testing.T) {
	newCursor := func() *IconCursor {
		icon := &SvgIcon{Grads: map[string]*rasterx.Gradient{}, Transform: rasterx.Identity}
		return &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: icon}
	}
	emptyID := []xml.Attr{{Name: xml.Name{Local: "id"}, Value: ""}}
	if err := linearGradientF(newCursor(), emptyID); err != errZeroLengthID {
		t.Errorf("linearGradientF empty id: got %v, want errZeroLengthID", err)
	}
	if err := radialGradientF(newCursor(), emptyID); err != errZeroLengthID {
		t.Errorf("radialGradientF empty id: got %v, want errZeroLengthID", err)
	}
	// A non-empty id registers without error.
	goodID := []xml.Attr{{Name: xml.Name{Local: "id"}, Value: "g1"}}
	c := newCursor()
	if err := linearGradientF(c, goodID); err != nil {
		t.Errorf("linearGradientF good id: unexpected error %v", err)
	}
	if _, ok := c.icon.Grads["g1"]; !ok {
		t.Error("linearGradientF good id: gradient not registered")
	}
}

// TestEmptyLinearGradientIgnoreModeSkips verifies that in ignore mode a
// malformed (empty-id) gradient does not abort the parse.
func TestEmptyLinearGradientIgnoreModeSkips(t *testing.T) {
	const svg = `<svg width="10" height="10" viewBox="0 0 10 10" xmlns="http://www.w3.org/2000/svg">
  <linearGradient id="" x1="0" x2="1"/>
  <rect x="0" y="0" width="10" height="10" fill="#000000"/>
</svg>`
	if _, err := ReadIconStream(strings.NewReader(svg), IgnoreErrorMode); err != nil {
		t.Errorf("ignore mode should skip malformed gradient, got %v", err)
	}
}

// TestGroupOpacityMultiplied verifies groupOpacity multiplies into the painted
// fill alpha.
func TestGroupOpacityMultiplied(t *testing.T) {
	var p rasterx.Path
	p.Start(fixed.Point26_6{X: 0, Y: 0})
	p.Line(fixed.Point26_6{X: 100 * 64, Y: 0})
	p.Line(fixed.Point26_6{X: 100 * 64, Y: 100 * 64})
	p.Line(fixed.Point26_6{X: 0, Y: 100 * 64})
	p.Stop(true)

	style := DefaultStyle
	style.fillerColor = color.NRGBA{0, 0, 0, 0xff}
	style.groupOpacity = 0.5
	sp := SvgPath{PathStyle: style, Path: p}

	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	scanner := rasterx.NewScannerGV(100, 100, img, img.Bounds())
	raster := rasterx.NewDasher(100, 100, scanner)
	sp.DrawTransformed(raster, 1.0, rasterx.Identity)

	_, _, _, a := rgba8(img, 50, 50)
	if a < 110 || a > 145 { // ~0.5 * 255
		t.Errorf("group opacity 0.5 -> alpha %d, want ~128", a)
	}
}

// --- small helpers ---

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}
	return b - a
}

func isNaNOrInf(v float64) bool {
	return v != v || v > 1e308 || v < -1e308
}

func blank(img *image.RGBA) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a != 0 {
				return false
			}
		}
	}
	return true
}

func countPathOps(p rasterx.Path) (moves, lines int) {
	for i := 0; i < len(p); {
		switch rasterx.PathCommand(p[i]) {
		case rasterx.PathMoveTo:
			moves++
			i += 3
		case rasterx.PathLineTo:
			lines++
			i += 3
		case rasterx.PathQuadTo:
			i += 5
		case rasterx.PathCubicTo:
			i += 7
		case rasterx.PathClose:
			i++
		default:
			return
		}
	}
	return
}

// TestPatternTitleDescNoPanic: a <title>/<desc> inside a <pattern> ran titleF/
// descF against compileDefs' temporary icon, appending to ITS Titles slice and
// setting inTitleText/inDescText. After the original icon was restored the flag
// stayed on, so the next CharData indexed the original (empty) Titles at [-1].
func TestPatternTitleDescNoPanic(t *testing.T) {
	cases := map[string]string{
		"title": `<svg xmlns="http://www.w3.org/2000/svg"><defs><pattern id="p" width="2" height="2"><title>tile</title><rect width="2" height="2"/></pattern></defs><rect width="10" height="10" fill="url(#p)"/> </svg>`,
		"desc":  `<svg xmlns="http://www.w3.org/2000/svg"><defs><pattern id="p" width="2" height="2"><desc>tile</desc><rect width="2" height="2"/></pattern></defs><rect width="10" height="10" fill="url(#p)"/> </svg>`,
	}
	for name, svg := range cases {
		t.Run(name, func(t *testing.T) {
			icon, err, panicked := parse(t, svg, StrictErrorMode)
			if panicked {
				t.Fatal("pattern metadata panicked the parser")
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(icon.SVGPaths) != 1 {
				t.Errorf("got %d paths, want 1 (the pattern-filled rect)", len(icon.SVGPaths))
			}
			if len(icon.Titles) != 0 || len(icon.Descriptions) != 0 {
				t.Errorf("pattern metadata leaked into the document: titles=%q descs=%q", icon.Titles, icon.Descriptions)
			}
		})
	}
}

// exponentialUseSVG builds a "billion laughs" style document: level n is a
// group containing two <use>s of level n-1, so a single top-level <use> expands
// to 2^n rects while staying only n levels deep (well under maxUseDepth).
func exponentialUseSVG(levels int) string {
	var b strings.Builder
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg"><defs><g id="a0"><rect width="1" height="1"/></g>`)
	for i := 1; i <= levels; i++ {
		fmt.Fprintf(&b, `<g id="a%d"><use href="#a%d"/><use href="#a%d"/></g>`, i, i-1, i-1)
	}
	fmt.Fprintf(&b, `</defs><use href="#a%d"/></svg>`, levels)
	return b.String()
}

// TestUseExpansionBudget: the depth cap alone does not bound <use> expansion;
// sibling references fan out exponentially. A document-wide replay budget must
// stop the expansion in every error mode, surfacing an error only in strict.
func TestUseExpansionBudget(t *testing.T) {
	svg := exponentialUseSVG(22) // 4M rects unbounded

	icon, err, panicked := parse(t, svg, StrictErrorMode)
	if panicked {
		t.Fatal("strict: panicked")
	}
	if err == nil {
		t.Error("strict: exponential use expansion should return an error")
	}
	if len(icon.SVGPaths) > maxUseReplay {
		t.Errorf("strict: %d paths emitted, budget is %d", len(icon.SVGPaths), maxUseReplay)
	}

	for _, m := range []ErrorMode{IgnoreErrorMode, WarnErrorMode} {
		icon, err, panicked := parse(t, svg, m)
		if panicked {
			t.Fatalf("mode %d: panicked", m)
		}
		if err != nil {
			t.Errorf("mode %d: expansion should be truncated, not fail: %v", m, err)
		}
		if len(icon.SVGPaths) > maxUseReplay {
			t.Errorf("mode %d: %d paths emitted, budget is %d", m, len(icon.SVGPaths), maxUseReplay)
		}
	}

	// A modest, legitimate fan-out must be unaffected.
	small, err, _ := parse(t, exponentialUseSVG(8), StrictErrorMode)
	if err != nil || len(small.SVGPaths) != 256 {
		t.Errorf("8-level use: paths=%d err=%v, want 256 paths and no error", len(small.SVGPaths), err)
	}
}

// TestForwardPatternStrictErrorPropagates: a pattern whose child fails to
// compile errors in strict mode when declared before its use (readStyleAttr
// path) but was silently accepted when declared after it, because the
// finalize-time resolver went through ReadPatternURL, which drops the error.
func TestForwardPatternStrictErrorPropagates(t *testing.T) {
	backward := `<svg xmlns="http://www.w3.org/2000/svg"><defs><pattern id="p" width="2" height="2"><path d="M 0 0 L"/></pattern></defs><rect width="10" height="10" fill="url(#p)"/></svg>`
	forward := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="10" height="10" fill="url(#p)"/><defs><pattern id="p" width="2" height="2"><path d="M 0 0 L"/></pattern></defs></svg>`

	if _, err, _ := parse(t, backward, StrictErrorMode); err == nil {
		t.Fatal("backward reference: expected strict error (precondition)")
	}
	if _, err, _ := parse(t, forward, StrictErrorMode); err == nil {
		t.Error("forward reference: pattern compile error was swallowed in strict mode")
	}
	for _, m := range []ErrorMode{IgnoreErrorMode, WarnErrorMode} {
		if _, err, _ := parse(t, forward, m); err != nil {
			t.Errorf("mode %d: forward bad pattern should not error: %v", m, err)
		}
	}
}

// TestObjectBoundingBoxGradientRotatesWithShape: an objectBoundingBox gradient
// is defined in the shape's own (pre-transform) bounding box, so a horizontal
// gradient on a rect rotated 90 degrees must render as a vertical gradient.
// Building it from the device-space extent instead kept it horizontal.
func TestObjectBoundingBoxGradientRotatesWithShape(t *testing.T) {
	const svg = `<svg width="10" height="20" xmlns="http://www.w3.org/2000/svg"><defs><linearGradient id="g"><stop offset="0" stop-color="red"/><stop offset="1" stop-color="blue"/></linearGradient></defs><rect width="20" height="10" transform="translate(10 0) rotate(90)" fill="url(#g)"/></svg>`
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 10, 20))
	scanner := rasterx.NewScannerGV(10, 20, img, img.Bounds())
	icon.Draw(rasterx.NewDasher(10, 20, scanner), 1)

	// The rect's x axis (gradient direction) now points down the page.
	if r, _, b, _ := rgba8(img, 5, 2); r <= b {
		t.Errorf("top (5,2) = r%d b%d, want red-leaning", r, b)
	}
	if r, _, b, _ := rgba8(img, 5, 17); b <= r {
		t.Errorf("bottom (5,17) = r%d b%d, want blue-leaning", r, b)
	}
	lr, _, lb, _ := rgba8(img, 2, 10)
	rr, _, rb, _ := rgba8(img, 7, 10)
	if absInt(int(lr)-int(rr)) > 8 || absInt(int(lb)-int(rb)) > 8 {
		t.Errorf("left (2,10) = r%d b%d vs right (7,10) = r%d b%d: gradient still varies horizontally", lr, lb, rr, rb)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestDefaultLineJoinIsMiter checks that a stroke without an explicit
// stroke-linejoin uses the SVG initial value, miter, rather than bevel.
// A mitered 90-degree corner paints the outer corner of the stroke; a
// beveled one chamfers it, leaving that pixel empty.
// See https://github.com/srwiley/oksvg/issues/52.
func TestDefaultLineJoinIsMiter(t *testing.T) {
	if DefaultStyle.LineJoin != rasterx.Miter {
		t.Errorf("DefaultStyle.LineJoin = %v, want rasterx.Miter", DefaultStyle.LineJoin)
	}
	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100" viewBox="0 0 100 100">
  <rect x="20" y="20" width="60" height="60" fill="none" stroke="#000" stroke-width="8"/>
</svg>`
	img := renderSVG(t, svg, 100, 100, IgnoreErrorMode)
	// The stroke's outer edge is at x=16,y=16; (17,17) is inside a mitered
	// corner but outside the 45-degree bevel between (16,24) and (24,16).
	if _, _, _, a := rgba8(img, 17, 17); a == 0 {
		t.Errorf("outer corner (17,17) is unpainted: default stroke-linejoin is bevel, want miter")
	}
	// Explicit bevel still chamfers the corner.
	bevel := renderSVG(t, strings.Replace(svg, `stroke-width="8"`, `stroke-width="8" stroke-linejoin="bevel"`, 1), 100, 100, IgnoreErrorMode)
	if _, _, _, a := rgba8(bevel, 17, 17); a != 0 {
		t.Errorf("explicit stroke-linejoin=bevel painted the outer corner (alpha %d), want 0", a)
	}
}
