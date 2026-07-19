// Copyright 2026 The oksvg Authors. All rights reserved.
//
// render_golden_test.go implements T10: golden-image regression tests.
//
// It renders a small, fixed set of vector-only fixtures (five testdata SVGs
// plus one inline pattern fixture, since no checked-in testdata SVG uses
// <pattern>) at two sizes each -- native viewBox size, and 2x via SetTarget
// -- and compares the result against checked-in PNGs under testdata/golden/.
// Together the six fixtures cover:
//
//   - gradients, gradientUnits="objectBoundingBox" (default) -- TestShapes6.svg
//     (radial + linear gradients, one used as a stroke paint)
//   - gradients, gradientUnits="userSpaceOnUse"               -- testIcons/tagsremoved.svg
//     (gparted icon; many linear/radial gradients, incl. xlink:href stop
//     inheritance and gradientTransform)
//   - pattern fill                                             -- inline patternFillSVG below
//     (no testdata SVG contains <pattern>, so this is a small
//     hand-written fixture rather than a new testdata file)
//   - <use>/<defs> reuse                                       -- testIcons/defs.svg
//     (circle/rect/group/path definitions referenced multiple times via <use>)
//   - multi-arc / curve-heavy paths                            -- landscapeIcons/village.svg
//     (dozens of elliptical-arc path segments)
//   - dashed stroke, with opacity variants                     -- OpacityStrokeDashTest.svg
//     (stroke-dasharray/-dashoffset combined with stroke/fill/group opacity)
//
// Regenerate goldens (only after confirming the renderer is correct, since
// this mode blindly overwrites the checked-in images):
//
//	go test -run TestGoldenRender -update
//
// Verify against the checked-in goldens (normal CI mode):
//
//	go test -run TestGoldenRender
package oksvg_test

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/NimbleMarkets/oksvg"
	. "github.com/srwiley/rasterx"
)

// updateGolden regenerates testdata/golden/*.png from the current renderer
// output instead of comparing against it. Checked for collisions with other
// test files' flags first: no other _test.go in this module currently
// declares a "-update" (or any) flag, so this is safe at the package level.
var updateGolden = flag.Bool("update", false, "update golden images in testdata/golden instead of comparing against them")

const (
	goldenDir = "testdata/golden"

	// channelTolerance is the maximum allowed per-channel (R, G, B or A,
	// each 0-255) absolute difference before a pixel counts as mismatched.
	// This absorbs +/-1 LSB rounding from the RGBA<->NRGBA premultiplication
	// round trip that PNG's straight-alpha encoding forces on decode.
	channelTolerance = 2

	// maxMismatchFraction is the maximum fraction of pixels that may exceed
	// channelTolerance before the golden comparison fails. This absorbs
	// antialiasing/scan-conversion jitter along edges without masking a real
	// rendering regression, which would produce far more than a sliver of
	// mismatched edge pixels.
	maxMismatchFraction = 0.005

	// minNonTransparentFraction guards against the "all-blank golden" failure
	// mode: an empty/near-empty render (e.g. from a broken transform or a
	// silently-skipped draw) would otherwise happily match an equally blank
	// golden and the test would pass while testing nothing.
	minNonTransparentFraction = 0.01
)

// patternFillSVG is a small inline fixture covering pattern fill. No
// checked-in testdata SVG contains a <pattern> element, so per the task
// brief this is a hand-written string fixture rather than a new file under
// testdata/. It exercises a userSpaceOnUse tiling pattern used both as a
// background fill and as a stroked shape's fill, similar in spirit to the
// pattern fixtures in paint_pipeline_test.go.
const patternFillSVG = `<svg width="120" height="120" viewBox="0 0 120 120" xmlns="http://www.w3.org/2000/svg">
  <defs>
    <pattern id="p" width="20" height="20" patternUnits="userSpaceOnUse">
      <rect width="20" height="20" fill="#eeeeee"/>
      <circle cx="10" cy="10" r="8" fill="#3366cc"/>
    </pattern>
  </defs>
  <rect x="0" y="0" width="120" height="120" fill="url(#p)"/>
  <circle cx="60" cy="60" r="50" fill="url(#p)" stroke="#000000" stroke-width="3"/>
</svg>`

// goldenCase names one rendered fixture. Exactly one of path or svg is set:
// path names a testdata SVG to read from disk, svg holds an inline SVG
// document string.
type goldenCase struct {
	name    string // golden file basename, combined with @1x/@2x.png
	path    string // testdata path, or "" if svg is set
	svg     string // inline SVG source, used when path == ""
	feature string // human-readable note on what this case exercises
}

// goldenCases is the fixed set of golden-render fixtures. See the package
// doc comment above for the feature-coverage map.
var goldenCases = []goldenCase{
	{
		name:    "gradient_objectbbox",
		path:    "testdata/TestShapes6.svg",
		feature: "gradients: gradientUnits=objectBoundingBox (radial + linear), gradient used as stroke paint",
	},
	{
		name:    "gradient_userspace",
		path:    "testdata/testIcons/tagsremoved.svg",
		feature: "gradients: gradientUnits=userSpaceOnUse, xlink:href gradient stop inheritance, gradientTransform",
	},
	{
		name:    "pattern_fill",
		svg:     patternFillSVG,
		feature: "pattern fill (inline fixture: no testdata SVG contains <pattern>)",
	},
	{
		name:    "use_defs",
		path:    "testdata/testIcons/defs.svg",
		feature: "<use>/<defs> reuse of circle/rect/group/path definitions",
	},
	{
		name:    "multi_arc",
		path:    "testdata/landscapeIcons/village.svg",
		feature: "multi-arc / curve-heavy paths (dozens of elliptical-arc segments)",
	},
	{
		name:    "dashed_stroke",
		path:    "testdata/OpacityStrokeDashTest.svg",
		feature: "stroke-dasharray/-dashoffset combined with stroke/fill/group opacity",
	},
}

// loadIcon parses the fixture, from disk or from its inline svg string,
// using StrictErrorMode: every chosen fixture is known-good vector SVG (no
// unsupported elements), so a parse error here indicates a real regression,
// not an expected warning.
func (c goldenCase) loadIcon(t *testing.T) *SvgIcon {
	t.Helper()
	var (
		icon *SvgIcon
		err  error
	)
	if c.svg != "" {
		icon, err = ReadIconStream(strings.NewReader(c.svg), StrictErrorMode)
	} else {
		icon, err = ReadIcon(c.path, StrictErrorMode)
	}
	if err != nil {
		t.Fatalf("%s: failed to parse: %v", c.name, err)
	}
	return icon
}

// renderIcon rasterizes icon into a fresh w x h RGBA image using the
// standard NewScannerGV + NewDasher pipeline (matching the rest of this
// package's tests and benchmarks), sizing the icon to fill the image via
// SetTarget.
func renderIcon(icon *SvgIcon, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scanner := NewScannerGV(w, h, img, img.Bounds())
	raster := NewDasher(w, h, scanner)
	icon.SetTarget(0, 0, float64(w), float64(h))
	icon.Draw(raster, 1.0)
	return img
}

// nonTransparentFraction returns the fraction of pixels in img with a
// nonzero alpha channel.
func nonTransparentFraction(img *image.RGBA) float64 {
	b := img.Bounds()
	total := b.Dx() * b.Dy()
	if total == 0 {
		return 0
	}
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if img.RGBAAt(x, y).A != 0 {
				n++
			}
		}
	}
	return float64(n) / float64(total)
}

// assertNonBlank fails t if img has too few non-transparent pixels to be a
// plausible render, guarding against the all-blank-golden failure mode
// (e.g. a silently no-op draw call producing a fully transparent image that
// would otherwise happily "match" an equally blank golden).
func assertNonBlank(t *testing.T, label string, img *image.RGBA) {
	t.Helper()
	frac := nonTransparentFraction(img)
	if frac < minNonTransparentFraction {
		t.Fatalf("%s: only %.3f%% non-transparent pixels; rendering looks blank (want > %.1f%%)",
			label, frac*100, minNonTransparentFraction*100)
	}
}

// pixel8 returns the 8-bit alpha-premultiplied r,g,b,a for the pixel at
// (x,y), regardless of img's underlying concrete color model (image.RGBA
// for freshly rendered images, image.NRGBA for PNG-decoded ones). Routing
// both sides of a comparison through Color.RGBA() this way means the
// comparison tolerance only has to absorb genuine premultiplication
// round-trip rounding, not color-model bookkeeping differences.
func pixel8(img image.Image, x, y int) (r, g, b, a uint8) {
	cr, cg, cb, ca := img.At(x, y).RGBA()
	return uint8(cr >> 8), uint8(cg >> 8), uint8(cb >> 8), uint8(ca >> 8)
}

func absDiffU8(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}

func maxU8(vs ...uint8) uint8 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// goldenPath returns the checked-in golden path for a case name and size
// suffix ("1x" or "2x"): testdata/golden/<name>@<size>.png.
func goldenPath(name, size string) string {
	return filepath.Join(goldenDir, fmt.Sprintf("%s@%s.png", name, size))
}

// actualPath returns the scratch path a mismatched render is dumped to for
// inspection on failure: testdata/<label>-actual.png, where label is
// already of the form "<name>@<size>" (e.g. "dashed_stroke@1x"). This lives
// directly under testdata/ (not testdata/golden/), which .gitignore excludes
// wholesale via "/testdata/*.png" -- so failure dumps are never accidentally
// committed.
func actualPath(label string) string {
	return filepath.Join("testdata", fmt.Sprintf("%s-actual.png", label))
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}

// compareGolden compares got against the checked-in golden PNG at path,
// failing t if more than maxMismatchFraction of pixels have any channel
// differing from the golden by more than channelTolerance. On failure it
// writes got to testdata/ for visual inspection and reports diff stats.
func compareGolden(t *testing.T, label, path string, got *image.RGBA) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%s: open golden %s: %v (run `go test -run TestGoldenRender -update` to generate goldens)", label, path, err)
	}
	want, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatalf("%s: decode golden %s: %v", label, path, err)
	}

	wb, gb := want.Bounds(), got.Bounds()
	if wb.Dx() != gb.Dx() || wb.Dy() != gb.Dy() {
		t.Fatalf("%s: size mismatch: golden=%dx%d rendered=%dx%d", label, wb.Dx(), wb.Dy(), gb.Dx(), gb.Dy())
	}

	var (
		total, mismatched int
		maxDelta          uint8
	)
	for y := 0; y < gb.Dy(); y++ {
		for x := 0; x < gb.Dx(); x++ {
			wr, wg, wbl, wa := pixel8(want, wb.Min.X+x, wb.Min.Y+y)
			gr, gg, gbl, ga := pixel8(got, gb.Min.X+x, gb.Min.Y+y)
			total++
			d := maxU8(
				absDiffU8(wr, gr),
				absDiffU8(wg, gg),
				absDiffU8(wbl, gbl),
				absDiffU8(wa, ga),
			)
			if d > maxDelta {
				maxDelta = d
			}
			if d > channelTolerance {
				mismatched++
			}
		}
	}

	frac := float64(mismatched) / float64(total)
	if frac > maxMismatchFraction {
		dump := actualPath(label)
		writePNG(t, dump, got)
		t.Errorf("%s: golden mismatch: %d/%d pixels (%.3f%%) exceed %d/255 per-channel tolerance "+
			"(allowed %.2f%%); max channel delta observed=%d/255; actual image written to %s for inspection",
			label, mismatched, total, frac*100, channelTolerance, maxMismatchFraction*100, maxDelta, dump)
	}
}

// TestGoldenRender is the golden-image regression suite for T10. For each
// fixture in goldenCases it renders at native viewBox size and at 2x (via
// SetTarget), sanity-checks the render is not blank, then either writes new
// goldens (with -update) or compares against the checked-in ones.
func TestGoldenRender(t *testing.T) {
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", goldenDir, err)
		}
	}

	for _, tc := range goldenCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Log(tc.feature)

			icon := tc.loadIcon(t)
			w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
			if w <= 0 || h <= 0 {
				t.Fatalf("%s: invalid viewBox %dx%d", tc.name, w, h)
			}

			native := renderIcon(icon, w, h)
			assertNonBlank(t, tc.name+"@1x", native)

			doubled := renderIcon(icon, w*2, h*2)
			assertNonBlank(t, tc.name+"@2x", doubled)

			if *updateGolden {
				writePNG(t, goldenPath(tc.name, "1x"), native)
				writePNG(t, goldenPath(tc.name, "2x"), doubled)
				t.Logf("wrote goldens for %s", tc.name)
				return
			}

			compareGolden(t, tc.name+"@1x", goldenPath(tc.name, "1x"), native)
			compareGolden(t, tc.name+"@2x", goldenPath(tc.name, "2x"), doubled)
		})
	}
}
