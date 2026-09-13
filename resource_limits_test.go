package oksvg

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
)

func patternChainSVG(depth int) string {
	var b strings.Builder
	b.WriteString(`<svg><defs>`)
	for i := 0; i < depth; i++ {
		fmt.Fprintf(&b, `<pattern id="p%d" width="1" height="1"><rect width="1" height="1" fill="url(#p%d)"/></pattern>`, i, i+1)
	}
	b.WriteString(`</defs><rect width="1" height="1" fill="url(#p0)"/></svg>`)
	return b.String()
}

func TestPatternCompileDepthBounded(t *testing.T) {
	if _, err := ReadIconStream(strings.NewReader(patternChainSVG(80)), StrictErrorMode); err == nil {
		t.Fatal("deep pattern reference chain accepted without a limit")
	}
	if _, err := ReadIconStream(strings.NewReader(patternChainSVG(80)), IgnoreErrorMode); err != nil {
		t.Fatalf("permissive parse failed: %v", err)
	}
}

type countedPaint struct{ calls *int }

func (c countedPaint) RGBA() (uint32, uint32, uint32, uint32) {
	(*c.calls)++
	return 65535, 0, 0, 65535
}

func TestPatternFanoutBounded(t *testing.T) {
	var c PathCursor
	if err := c.CompilePath("M0 0H1V1H0Z"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	leaf := SvgPath{Path: c.Path, PathStyle: DefaultStyle}
	leaf.fillerColor = countedPaint{&calls}
	p := &Pattern{Width: 1, Height: 1, Transform: rasterx.Identity, Paths: []SvgPath{leaf}}
	for i := 0; i < 12; i++ {
		child := SvgPath{Path: c.Path, PathStyle: DefaultStyle}
		child.fillerColor = p
		p = &Pattern{Width: 1, Height: 1, Transform: rasterx.Identity, Paths: []SvgPath{child, child}}
	}
	p.GetColorFunction(1, ObjectBounds{W: 1, H: 1}, rasterx.Identity)(0, 0)
	if calls > 1024 {
		t.Fatalf("pattern DAG expanded to %d leaf paints", calls)
	}
}

func TestPatternBudgetSharedAcrossPathsAndResetBetweenDraws(t *testing.T) {
	var c PathCursor
	if err := c.CompilePath("M0 0H1V1H0Z"); err != nil {
		t.Fatal(err)
	}
	calls := 0
	leaf := SvgPath{Path: c.Path, PathStyle: DefaultStyle}
	leaf.fillerColor = countedPaint{&calls}
	p := &Pattern{Width: 1, Height: 1, Transform: rasterx.Identity, Paths: []SvgPath{leaf}}
	path := SvgPath{Path: c.Path, PathStyle: DefaultStyle}
	path.fillerColor = p
	icon := &SvgIcon{Transform: rasterx.Identity}
	for i := 0; i < maxPatternTiles+10; i++ {
		icon.SVGPaths = append(icon.SVGPaths, path)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	r := rasterx.NewDasher(1, 1, rasterx.NewScannerGV(1, 1, img, img.Bounds()))
	for i := 0; i < 2; i++ {
		calls = 0
		icon.Draw(r, 1)
		if calls != maxPatternTiles {
			t.Fatalf("draw %d: got %d leaf paints, want %d", i, calls, maxPatternTiles)
		}
	}
}

func TestPatternPixelBudgetAndDrawDepth(t *testing.T) {
	p := &Pattern{Width: 8, Height: 8, Transform: rasterx.Identity}
	state := &patternRenderState{pixels: maxPatternPixels - 64}
	p.getColorFunction(1, ObjectBounds{}, rasterx.Identity, state)
	if state.pixels != maxPatternPixels || state.tiles != 1 {
		t.Fatalf("at-limit tile refused: %+v", state)
	}
	p.getColorFunction(1, ObjectBounds{}, rasterx.Identity, state)
	if state.pixels != maxPatternPixels || state.tiles != 1 {
		t.Fatalf("over-limit tile allocated: %+v", state)
	}

	var c PathCursor
	if err := c.CompilePath("M0 0H1V1H0Z"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxPatternDepth+10; i++ {
		child := SvgPath{Path: c.Path, PathStyle: DefaultStyle}
		child.fillerColor = p
		p = &Pattern{Width: 1, Height: 1, Transform: rasterx.Identity, Paths: []SvgPath{child}}
	}
	state = &patternRenderState{}
	p.getColorFunction(1, ObjectBounds{}, rasterx.Identity, state)
	if state.tiles != maxPatternDepth || len(state.active) != 0 {
		t.Fatalf("draw depth not bounded or active entries leaked: %+v", state)
	}
}

func TestNestedDefinitionsStorageBounded(t *testing.T) {
	// 320 levels exhaust the budget while adding closing markers; 500 levels
	// exhaust it while opening collectors. Exercise both accounting paths.
	for _, depth := range []int{320, 500} {
		t.Run(fmt.Sprint(depth), func(t *testing.T) {
			var b strings.Builder
			b.WriteString(`<svg><defs><rect id="keep" width="1" height="1"/>`)
			for i := 0; i < depth; i++ {
				fmt.Fprintf(&b, `<g id="g%d">`, i)
			}
			b.WriteString(`<rect width="1" height="1"/>`)
			b.WriteString(strings.Repeat(`</g>`, depth))
			b.WriteString(`</defs><defs><rect id="late" width="1" height="1"/></defs><rect width="1" height="1" fill="red"/></svg>`)
			svg := b.String()
			if _, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode); err == nil {
				t.Error("strict mode accepted quadratic definition expansion")
			}
			icon, err := ReadIconStream(strings.NewReader(svg), IgnoreErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			entries := 0
			for _, defs := range icon.Defs {
				entries += len(defs)
			}
			if entries > 100000 {
				t.Errorf("%d-byte SVG expanded to %d stored definition entries", len(svg), entries)
			}
			if len(icon.SVGPaths) != 1 || icon.SVGPaths[0].GetFillColor() != (color.NRGBA{R: 255, A: 255}) {
				t.Error("definition limit disrupted the following document shape")
			}
			if len(icon.Defs["keep"]) != 1 || len(icon.Defs["late"]) != 0 {
				t.Error("budget did not retain completed definitions or persisted incorrectly across defs blocks")
			}
		})
	}
}
