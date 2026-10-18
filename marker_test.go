// Copyright 2026 The oksvg Authors. All rights reserved.
//
// marker_test.go covers <marker> placement on path, line, polyline, and
// polygon elements.

package oksvg

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// arrowDefs is the arrowhead go-mermaid draws: a triangle whose tip is the
// reference point, sized in stroke widths.
const arrowDefs = `<defs><marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5"
  markerWidth="5" markerHeight="5" orient="%s" markerUnits="%s">
  <path d="M0,0 L10,5 L0,10 z" fill="#ff0000"/></marker></defs>`

func arrowSVG(orient, units, shapes string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` +
		fmt.Sprintf(arrowDefs, orient, units) + shapes + `</svg>`
}

func markerRed(t *testing.T, svg string, x, y int) bool {
	t.Helper()
	r, g, b, a := rgba8(renderSVG(t, svg, 100, 100, StrictErrorMode), x, y)
	return r > 200 && g < 60 && b < 60 && a > 200
}

func pt(x, y float64) fixed.Point26_6 {
	return fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestMarkerVerticesAndAngles(t *testing.T) {
	var p rasterx.Path
	p.Start(pt(0, 0))
	p.Line(pt(10, 0))
	p.Line(pt(10, 10))
	vs := markerVertices(p)
	if len(vs) != 3 {
		t.Fatalf("vertices = %d, want 3", len(vs))
	}
	if !near(vs[0].autoAngle(), 0) || !near(vs[2].autoAngle(), math.Pi/2) {
		t.Errorf("end angles = %v, %v; want 0, π/2", vs[0].autoAngle(), vs[2].autoAngle())
	}
	if !near(vs[1].autoAngle(), math.Pi/4) {
		t.Errorf("corner angle = %v, want the bisector π/4", vs[1].autoAngle())
	}

	// A cubic's end tangent comes from its last control point.
	var q rasterx.Path
	q.Start(pt(0, 0))
	q.CubeBezier(pt(0, 10), pt(10, 10), pt(10, 0))
	vs = markerVertices(q)
	if !near(vs[0].autoAngle(), math.Pi/2) || !near(vs[1].autoAngle(), -math.Pi/2) {
		t.Errorf("cubic angles = %v, %v; want π/2, -π/2", vs[0].autoAngle(), vs[1].autoAngle())
	}

	// A closed subpath joins its ends at its first vertex.
	var c rasterx.Path
	c.Start(pt(0, 0))
	c.Line(pt(10, 0))
	c.Line(pt(10, 10))
	c.Stop(true)
	vs = markerVertices(c)
	if len(vs) != 4 || vs[3].x != 0 || vs[3].y != 0 {
		t.Fatalf("closed vertices = %+v, want the start repeated last", vs)
	}
	if !vs[0].hasIn || !vs[3].hasOut {
		t.Error("a closed subpath's ends should carry both directions")
	}
}

func TestMarkerEndDrawsArrowAtTip(t *testing.T) {
	line := `<line x1="10" y1="50" x2="60" y2="50" stroke="#000000" stroke-width="2" marker-end="url(#arrow)"/>`
	svg := arrowSVG("auto", "strokeWidth", line)
	// Five stroke widths of 2 make a 10-unit arrow ending at the line's end.
	// Points are inside the triangle, clear of its antialiased edges.
	for _, p := range [][2]int{{52, 47}, {55, 50}, {52, 52}} {
		if !markerRed(t, svg, p[0], p[1]) {
			t.Errorf("pixel %v should be inside the arrowhead", p)
		}
	}
	for _, p := range [][2]int{{62, 50}, {48, 46}, {30, 47}} {
		if markerRed(t, svg, p[0], p[1]) {
			t.Errorf("pixel %v should be outside the arrowhead", p)
		}
	}
	plain := arrowSVG("auto", "strokeWidth", strings.Replace(line, ` marker-end="url(#arrow)"`, "", 1))
	if markerRed(t, plain, 52, 47) {
		t.Error("without marker-end there should be no arrowhead")
	}
}

func TestMarkerFollowsDirection(t *testing.T) {
	svg := arrowSVG("auto", "strokeWidth", `<path d="M50,10 L50,60" stroke="#000" stroke-width="2" marker-end="url(#arrow)"/>`)
	if !markerRed(t, svg, 50, 55) || !markerRed(t, svg, 51, 52) {
		t.Error("a downward path should get a downward arrowhead")
	}
	if markerRed(t, svg, 52, 47) {
		t.Error("the arrowhead should not lie across the path")
	}
}

func TestMarkerAutoStartReverse(t *testing.T) {
	line := `<line x1="10" y1="50" x2="60" y2="50" stroke="#000" stroke-width="2" marker-start="url(#arrow)"/>`
	if !markerRed(t, arrowSVG("auto-start-reverse", "strokeWidth", line), 18, 47) {
		t.Error("auto-start-reverse should point the start arrow back along the line")
	}
	if markerRed(t, arrowSVG("auto", "strokeWidth", line), 18, 47) {
		t.Error("auto should point the start arrow along the line, off its start")
	}
}

func TestMarkerUnitsAndFixedAngle(t *testing.T) {
	line := `<line x1="10" y1="50" x2="60" y2="50" stroke="#000" stroke-width="2" marker-end="url(#arrow)"/>`
	svg := arrowSVG("auto", "userSpaceOnUse", line)
	if !markerRed(t, svg, 56, 50) || markerRed(t, svg, 52, 47) {
		t.Error("userSpaceOnUse should ignore the stroke width: a 5-unit arrow")
	}
	// A fixed 90° orientation points down whatever the path does: the arrow
	// lies above its tip at the line's end.
	for _, orient := range []string{"90", "90deg", "0.25turn"} {
		svg := arrowSVG(orient, "strokeWidth", line)
		if !markerRed(t, svg, 60, 45) || markerRed(t, svg, 55, 50) {
			t.Errorf("orient=%s should point the arrow down", orient)
		}
	}
}

func TestMarkerMidOnPolyline(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
  <defs><marker id="dot" viewBox="0 0 10 10" refX="5" refY="5" markerWidth="4" markerHeight="4">
    <circle cx="5" cy="5" r="5" fill="#ff0000"/></marker></defs>
  <polyline points="10,10 50,10 50,50 90,50" fill="none" stroke="#000" marker-mid="url(#dot)"/>
</svg>`
	for _, p := range [][2]int{{50, 10}, {50, 50}} {
		if !markerRed(t, svg, p[0], p[1]) {
			t.Errorf("interior vertex %v should have a dot", p)
		}
	}
	for _, p := range [][2]int{{10, 10}, {90, 50}} {
		if markerRed(t, svg, p[0], p[1]) {
			t.Errorf("end vertex %v should not have a mid marker", p)
		}
	}
}

func TestMarkerStyleInheritsAndShorthand(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` + fmt.Sprintf(arrowDefs, "auto", "strokeWidth") +
		`<g style="marker: url(#arrow)"><line x1="10" y1="50" x2="60" y2="50" stroke="#000" stroke-width="2"/></g></svg>`
	// The start arrow points along the line, so it sits before x=10.
	if !markerRed(t, svg, 55, 50) || !markerRed(t, svg, 5, 50) {
		t.Error("marker inherited from a group should draw at both ends")
	}
}

func TestMarkerOutsideDefsIsADefinition(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
  <marker id="arrow" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="5" markerHeight="5" orient="auto">
    <path d="M0,0 L10,5 L0,10 z" fill="#ff0000"/></marker>
  <line x1="10" y1="50" x2="60" y2="50" stroke="#000" stroke-width="2" marker-end="url(#arrow)"/>
</svg>`
	if !markerRed(t, svg, 55, 50) {
		t.Error("a marker outside <defs> should still be usable")
	}
	if markerRed(t, svg, 2, 5) {
		t.Error("a marker's content should not be drawn where the marker appears")
	}
}

func TestUseSkipsMarkerContent(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
  <defs><g id="group"><marker id="m" markerWidth="10" markerHeight="10">
    <rect width="10" height="10" fill="#ff0000"/></marker>
    <rect x="80" y="80" width="10" height="10" fill="#0000ff"/></g></defs>
  <use href="#group"/>
</svg>`
	img := renderSVG(t, svg, 100, 100, StrictErrorMode)
	if _, _, b, _ := rgba8(img, 85, 85); b != 255 {
		t.Error("the group's own content should be drawn")
	}
	if markerRed(t, svg, 2, 2) {
		t.Error("a marker inside a used group should not be drawn as content")
	}
}

func TestMarkerSelfReferenceAndMissingAreHarmless(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
  <line x1="0" y1="0" x2="10" y2="0" stroke="#000" marker-end="url(#later)"/>
  <defs><marker id="loop" markerWidth="10" markerHeight="10">
    <line x1="0" y1="0" x2="10" y2="0" stroke="#000" marker-end="url(#loop)"/></marker>
  <marker id="later" markerWidth="10" markerHeight="10"><rect width="5" height="5"/></marker></defs>
  <line x1="10" y1="50" x2="60" y2="50" stroke="#000" marker-end="url(#loop)"/>
  <line x1="10" y1="60" x2="60" y2="60" stroke="#000" marker-end="url(#nowhere)"/>
</svg>`
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	// Three lines, plus the loop marker's one line, drawn once.
	if got := len(icon.SVGPaths); got != 4 {
		t.Fatalf("paths = %d, want 4", got)
	}
}

func TestMarkerBudget(t *testing.T) {
	var points strings.Builder
	for i := 0; i < maxMarkerPaths+10; i++ {
		fmt.Fprintf(&points, "%d,%d ", i%100, i%97)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">
  <defs><marker id="m" markerWidth="1" markerHeight="1"><rect width="1" height="1"/></marker></defs>
  <polyline points="` + points.String() + `" marker-mid="url(#m)"/></svg>`
	if _, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("strict mode should report the marker budget, got %v", err)
	}
	icon, err := ReadIconStream(strings.NewReader(svg), IgnoreErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(icon.SVGPaths); got != maxMarkerPaths+1 {
		t.Fatalf("paths = %d, want the polyline plus %d markers", got, maxMarkerPaths)
	}
}

func TestInvalidMarkerReference(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><line x2="5" stroke="#000" marker-end="#arrow"/></svg>`
	if _, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode); err == nil {
		t.Error("a marker reference that is not url(#id) should fail strict parsing")
	}
	if _, err := ReadIconStream(strings.NewReader(svg), IgnoreErrorMode); err != nil {
		t.Errorf("ignore mode should skip the bad property: %v", err)
	}
}
