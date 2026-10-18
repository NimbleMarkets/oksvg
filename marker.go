// Copyright 2026 The oksvg Authors. All rights reserved.
//
// marker.go places <marker> content at the vertices of path, line, polyline,
// and polygon elements (marker-start, marker-mid, marker-end).

package oksvg

import (
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// maxMarkerPaths caps the paths that marker placement adds to one document.
// A marker-mid on a long polyline multiplies the marker's content by the
// vertex count, so the source size alone does not bound the output.
const maxMarkerPaths = 100000

// markerShapes are the elements SVG draws markers on.
var markerShapes = map[string]bool{"path": true, "line": true, "polyline": true, "polygon": true}

// markerGeometryAttrs are attributes of <marker> itself rather than styles
// its content inherits.
var markerGeometryAttrs = map[string]bool{
	"id": true, "viewBox": true, "refX": true, "refY": true, "markerWidth": true,
	"markerHeight": true, "markerUnits": true, "orient": true, "preserveAspectRatio": true,
	"transform": true,
}

type markerOrient int

const (
	orientAngle markerOrient = iota
	orientAuto
	orientAutoStartReverse
)

// compiledMarker is a <marker>'s geometry and its content, compiled once.
type compiledMarker struct {
	paths         []SvgPath
	refX, refY    float64
	width, height float64
	vbW, vbH      float64 // zero when the marker has no viewBox
	strokeUnits   bool    // markerUnits="strokeWidth", the default
	orient        markerOrient
	angle         float64 // radians, for orientAngle
	stretch       bool    // preserveAspectRatio="none"
}

// markerVertex is a point where a marker may be drawn, with the directions
// of the segments arriving at and leaving it.
type markerVertex struct {
	x, y                 float64
	inX, inY, outX, outY float64
	hasIn, hasOut        bool
}

// autoAngle is the orientation orient="auto" gives a marker at v: the
// direction of travel, bisecting the turn at an interior vertex.
func (v markerVertex) autoAngle() float64 {
	switch {
	case v.hasIn && v.hasOut:
		a, b := math.Atan2(v.inY, v.inX), math.Atan2(v.outY, v.outX)
		d := math.Mod(b-a+3*math.Pi, 2*math.Pi) - math.Pi
		return a + d/2
	case v.hasIn:
		return math.Atan2(v.inY, v.inX)
	case v.hasOut:
		return math.Atan2(v.outY, v.outX)
	}
	return 0
}

// markerVertices lists the vertices of a compiled path in user space.
func markerVertices(p rasterx.Path) []markerVertex {
	var vs []markerVertex
	f := func(v fixed.Int26_6) float64 { return float64(v) / 64 }
	var cx, cy float64
	start := -1 // index of the current subpath's first vertex
	// segment ends the current subpath segment at the last of pts, whose
	// earlier entries are control points. Tangents skip coincident points.
	segment := func(pts ...float64) {
		ex, ey := pts[len(pts)-2], pts[len(pts)-1]
		var outX, outY, inX, inY float64
		for i := 0; i < len(pts); i += 2 {
			if pts[i] != cx || pts[i+1] != cy {
				outX, outY = pts[i]-cx, pts[i+1]-cy
				break
			}
		}
		prev := append([]float64{cx, cy}, pts[:len(pts)-2]...)
		for i := len(prev) - 2; i >= 0; i -= 2 {
			if prev[i] != ex || prev[i+1] != ey {
				inX, inY = ex-prev[i], ey-prev[i+1]
				break
			}
		}
		if len(vs) > 0 && (outX != 0 || outY != 0) {
			last := &vs[len(vs)-1]
			last.outX, last.outY, last.hasOut = outX, outY, true
		}
		vs = append(vs, markerVertex{x: ex, y: ey, inX: inX, inY: inY, hasIn: inX != 0 || inY != 0})
		cx, cy = ex, ey
	}
	for i := 0; i < len(p); {
		switch rasterx.PathCommand(p[i]) {
		case rasterx.PathMoveTo:
			if i+2 >= len(p) {
				return vs
			}
			cx, cy = f(p[i+1]), f(p[i+2])
			vs = append(vs, markerVertex{x: cx, y: cy})
			start = len(vs) - 1
			i += 3
		case rasterx.PathLineTo:
			if i+2 >= len(p) {
				return vs
			}
			segment(f(p[i+1]), f(p[i+2]))
			i += 3
		case rasterx.PathQuadTo:
			if i+4 >= len(p) {
				return vs
			}
			segment(f(p[i+1]), f(p[i+2]), f(p[i+3]), f(p[i+4]))
			i += 5
		case rasterx.PathCubicTo:
			if i+6 >= len(p) {
				return vs
			}
			segment(f(p[i+1]), f(p[i+2]), f(p[i+3]), f(p[i+4]), f(p[i+5]), f(p[i+6]))
			i += 7
		case rasterx.PathClose:
			if start >= 0 {
				first := vs[start]
				if cx != first.x || cy != first.y {
					segment(first.x, first.y)
				}
				// A closed subpath joins its ends: the closing vertex leaves
				// along the first segment, and the first vertex arrives along
				// the closing one.
				last := &vs[len(vs)-1]
				if first.hasOut && len(vs)-1 != start {
					last.outX, last.outY, last.hasOut = first.outX, first.outY, true
				}
				if last.hasIn {
					vs[start].inX, vs[start].inY, vs[start].hasIn = last.inX, last.inY, true
				}
				cx, cy = first.x, first.y
			}
			i++
		default:
			return vs
		}
	}
	return vs
}

// readMarkerRef parses a marker property: none, or url(#id).
func readMarkerRef(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "none" {
		return "", nil
	}
	if id, ok := parseURLID(v); ok {
		return id, nil
	}
	return "", errors.New("invalid marker reference: " + v)
}

// readMarkerAngle parses an orient angle: a number of degrees, or a number
// with a deg, rad, grad, or turn unit.
func readMarkerAngle(v string) (float64, error) {
	units := []struct {
		suffix string
		scale  float64
	}{{"grad", math.Pi / 200}, {"deg", math.Pi / 180}, {"rad", 1}, {"turn", 2 * math.Pi}}
	for _, u := range units {
		if strings.HasSuffix(v, u.suffix) {
			f, err := parseFloat(strings.TrimSuffix(v, u.suffix), 64)
			return f * u.scale, err
		}
	}
	f, err := parseFloat(v, 64)
	return f * math.Pi / 180, err
}

// marker returns the compiled marker with the given id, or nil when there is
// none. Markers are compiled once per document; a marker referenced before
// its definition is read is not drawn.
func (c *IconCursor) marker(id string) (*compiledMarker, error) {
	if m, ok := c.markers[id]; ok {
		return m, nil
	}
	defs, ok := c.icon.Defs[id]
	if !ok || len(defs) == 0 || defs[0].Tag != "marker" {
		return nil, nil
	}
	if c.markers == nil {
		c.markers = map[string]*compiledMarker{}
	}
	c.markers[id] = nil // Compiled at most once, even if it fails.
	m := &compiledMarker{width: 3, height: 3, strokeUnits: true}
	var style []xml.Attr
	for _, attr := range defs[0].Attrs {
		var err error
		v := strings.TrimSpace(attr.Value)
		switch attr.Name.Local {
		case "viewBox":
			parts := splitOnCommaOrSpace(v)
			if len(parts) != 4 {
				err = errParamMismatch
				break
			}
			var vb [4]float64
			for i, s := range parts {
				if vb[i], err = parseFloat(s, 64); err != nil {
					break
				}
			}
			m.vbW, m.vbH = vb[2], vb[3]
		case "refX":
			m.refX, err = parseFloat(v, 64)
		case "refY":
			m.refY, err = parseFloat(v, 64)
		case "markerWidth":
			m.width, err = parseFloat(v, 64)
		case "markerHeight":
			m.height, err = parseFloat(v, 64)
		case "markerUnits":
			m.strokeUnits = v != "userSpaceOnUse"
		case "preserveAspectRatio":
			m.stretch = strings.HasPrefix(v, "none")
		case "orient":
			switch v {
			case "auto":
				m.orient = orientAuto
			case "auto-start-reverse":
				m.orient = orientAutoStartReverse
			default:
				m.angle, err = readMarkerAngle(v)
			}
		}
		if err != nil {
			e := fmt.Sprintf("marker %s: %s: %s", id, attr.Name.Local, err)
			if c.returnError(e) {
				return nil, errors.New(e)
			}
			return nil, nil
		}
		if !markerGeometryAttrs[attr.Name.Local] {
			style = append(style, attr)
		}
	}
	if m.width <= 0 || m.height <= 0 {
		return nil, nil // Markers with no area are not drawn.
	}
	// The marker's own presentation attributes style its content, as a group
	// around it would. Nested markers in the content are definitions, not
	// content; compileDefs skips them.
	content := make([]definition, 0, len(defs)+1)
	content = append(content, definition{Tag: "g", Attrs: style})
	content = append(content, defs[1:]...)
	content = append(content, definition{Tag: "endg"})
	c.markerDepth++
	paths, err := c.compileDefs(content)
	c.markerDepth--
	if err != nil {
		return nil, err
	}
	m.paths = paths
	c.markers[id] = m
	return m, nil
}

// placeMarkers adds the content of style's markers at the vertices of path.
func (c *IconCursor) placeMarkers(style PathStyle, path rasterx.Path) error {
	vs := markerVertices(path)
	if len(vs) == 0 {
		return nil
	}
	place := func(id string, v markerVertex, start bool) error {
		if id == "" {
			return nil
		}
		m, err := c.marker(id)
		if m == nil || err != nil {
			return err
		}
		angle := m.angle
		if m.orient != orientAngle {
			angle = v.autoAngle()
			if start && m.orient == orientAutoStartReverse {
				angle += math.Pi
			}
		}
		sx, sy := 1.0, 1.0
		if m.vbW > 0 && m.vbH > 0 {
			sx, sy = m.width/m.vbW, m.height/m.vbH
			if !m.stretch {
				s := math.Min(sx, sy)
				sx, sy = s, s
			}
		}
		k := 1.0
		if m.strokeUnits {
			k = style.LineWidth
		}
		if len(m.paths) > maxMarkerPaths-c.markerPaths {
			msg := fmt.Sprintf("marker content exceeds budget of %d paths", maxMarkerPaths)
			if !c.markerBudgetSpent {
				c.markerBudgetSpent = true
				if c.ErrorMode == WarnErrorMode {
					log.Println(msg)
				}
			}
			if c.ErrorMode == StrictErrorMode {
				return errors.New(msg)
			}
			return nil
		}
		c.markerPaths += len(m.paths)
		at := style.mAdder.M.Translate(v.x, v.y).Rotate(angle).Scale(k*sx, k*sy).Translate(-m.refX, -m.refY)
		for _, p := range m.paths {
			p.mAdder.M = at.Mult(p.mAdder.M)
			p.groupOpacity *= style.groupOpacity
			p.order = c.icon.nextOrder()
			c.icon.SVGPaths = append(c.icon.SVGPaths, p)
		}
		return nil
	}
	if err := place(style.markerStart, vs[0], true); err != nil {
		return err
	}
	for i := 1; i < len(vs)-1; i++ {
		if err := place(style.markerMid, vs[i], false); err != nil {
			return err
		}
	}
	if len(vs) > 1 {
		return place(style.markerEnd, vs[len(vs)-1], false)
	}
	return nil
}

// emitPath turns the geometry the cursor compiled for an element into an
// SvgPath, followed by any markers the element draws.
func (c *IconCursor) emitPath(tag string) error {
	if len(c.Path) == 0 {
		return nil
	}
	path := make(rasterx.Path, len(c.Path))
	copy(path, c.Path)
	c.Path = c.Path[:0]
	style := c.StyleStack[len(c.StyleStack)-1]
	c.icon.SVGPaths = append(c.icon.SVGPaths, SvgPath{PathStyle: style, Path: path, order: c.icon.nextOrder()})
	// Markers inside marker content are not drawn, which also keeps a marker
	// that refers to itself from recursing.
	if !markerShapes[tag] || c.markerDepth > 0 ||
		(style.markerStart == "" && style.markerMid == "" && style.markerEnd == "") {
		return nil
	}
	return c.placeMarkers(style, path)
}

// skipSpan returns the index of the end tag matching defs[i] (an open tag),
// tracking nesting; the caller's loop increment then steps past it.
func skipSpan(defs []definition, i int, open, end string) int {
	depth := 1
	for i++; i < len(defs); i++ {
		switch defs[i].Tag {
		case open:
			depth++
		case end:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(defs) - 1
}
