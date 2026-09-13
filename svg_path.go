// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// svg_path.go binds a drawing style to a compiled rasterx path and draws it.

package oksvg

import (
	"image/color"
	"math"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// SvgPath binds a style to a path.
type SvgPath struct {
	PathStyle
	Path  rasterx.Path
	order int // draw-order stamp; see SvgIcon.Draw
}

// Draw the compiled SvgPath into the Dasher.
func (svgp *SvgPath) Draw(r *rasterx.Dasher, opacity float64) {
	svgp.DrawTransformed(r, opacity, rasterx.Identity)
}

// DrawTransformed draws the compiled SvgPath into the Dasher while applying transform t.
func (svgp *SvgPath) DrawTransformed(r *rasterx.Dasher, opacity float64, t rasterx.Matrix2D) {
	svgp.drawTransformedInternal(r, opacity, t, nil)
}

// drawTransformedInternal is the recursion-aware core of DrawTransformed. The
// active set threads through nested *Pattern fills so pattern cycles are
// detected per call-graph instead of via a shared mutable flag. active is nil
// on the common (non-pattern) path and is allocated lazily only when a Pattern
// paint is actually encountered.
//
// It never mutates svgp: the combined matrix lives in a local MatrixAdder, so
// concurrent draws of the same SvgPath into separate targets are safe.
func (svgp *SvgPath) drawTransformedInternal(r *rasterx.Dasher, opacity float64, t rasterx.Matrix2D, active map[*Pattern]bool) {
	// Local combined transform; never mutate svgp.mAdder (data race + retained
	// Adder pointer).
	madder := rasterx.MatrixAdder{M: t.Mult(svgp.mAdder.M)}

	// User-space (pre-transform) geometry bounds, computed once. Gradients and
	// patterns that need the object bounding box use this so their coordinate
	// space is consistent under any draw transform.
	userBounds, hasBounds := pathBounds(svgp.Path)

	if svgp.fillerColor != nil {
		r.Clear()
		rf := &r.Filler
		rf.SetWinding(svgp.UseNonZeroWinding)
		madder.Adder = rf // This allows transformations to be applied
		svgp.Path.AddTo(&madder)

		fillOpacity := clamp01(svgp.FillOpacity * svgp.groupOpacity * opacity)
		switch fillerColor := svgp.fillerColor.(type) {
		case color.Color:
			rf.SetColor(rasterx.ApplyOpacity(fillerColor, fillOpacity))
			rf.Draw()
		case rasterx.Gradient:
			if hasBounds {
				rf.SetColor(gradientColorFunc(fillerColor, fillOpacity, madder.M, userBounds, rf.Scanner.GetPathExtent()))
				rf.Draw()
			}
		case *Pattern:
			if hasBounds {
				if active == nil {
					active = map[*Pattern]bool{}
				}
				rf.SetColor(fillerColor.getColorFunction(fillOpacity, userBounds, madder.M, active))
				rf.Draw()
			}
		}
		// default is true
		rf.SetWinding(true)
	}
	if svgp.linerColor != nil {
		r.Clear()
		madder.Adder = r
		lineGap := svgp.LineGap
		if lineGap == nil {
			lineGap = DefaultStyle.LineGap
		}
		lineCap := svgp.LineCap
		if lineCap == nil {
			lineCap = DefaultStyle.LineCap
		}
		leadLineCap := lineCap
		if svgp.LeadLineCap != nil {
			leadLineCap = svgp.LeadLineCap
		}
		r.SetStroke(fixed.Int26_6(svgp.LineWidth*64),
			fixed.Int26_6(svgp.MiterLimit*64), leadLineCap, lineCap,
			lineGap, svgp.LineJoin, svgp.Dash, svgp.DashOffset)
		svgp.Path.AddTo(&madder)

		lineOpacity := clamp01(svgp.LineOpacity * svgp.groupOpacity * opacity)
		switch linerColor := svgp.linerColor.(type) {
		case color.Color:
			r.SetColor(rasterx.ApplyOpacity(linerColor, lineOpacity))
			r.Draw()
		case rasterx.Gradient:
			if hasBounds {
				r.SetColor(gradientColorFunc(linerColor, lineOpacity, madder.M, userBounds, r.Scanner.GetPathExtent()))
				r.Draw()
			}
		case *Pattern:
			if hasBounds {
				if active == nil {
					active = map[*Pattern]bool{}
				}
				r.SetColor(linerColor.getColorFunction(lineOpacity, userBounds, madder.M, active))
				r.Draw()
			}
		}
	}
}

// pathBounds computes the tight axis-aligned bounding box of p in its own
// (pre-transform) coordinate space: the SVG object bounding box. Curve extents
// are exact (evaluated at the roots of each segment's derivative), so Bézier
// control points that lie outside the curve do not inflate the box; an
// objectBoundingBox gradient or pattern would otherwise be positioned over the
// control hull instead of the geometry (visibly wrong for arc-built ellipses
// and for stroked curves with distant handles). ok is false for empty paths
// (no vertices), which callers use to skip paint that requires a bounding box.
func pathBounds(p rasterx.Path) (b ObjectBounds, ok bool) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	add := func(x, y float64) {
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
		ok = true
	}
	pt := func(i int) (float64, float64) { return float64(p[i]) / 64, float64(p[i+1]) / 64 }
	var curX, curY, startX, startY float64
	for i := 0; i < len(p); {
		switch rasterx.PathCommand(p[i]) {
		case rasterx.PathMoveTo:
			if i+2 >= len(p) {
				return b, ok
			}
			curX, curY = pt(i + 1)
			startX, startY = curX, curY
			add(curX, curY)
			i += 3
		case rasterx.PathLineTo:
			if i+2 >= len(p) {
				return b, ok
			}
			curX, curY = pt(i + 1)
			add(curX, curY)
			i += 3
		case rasterx.PathQuadTo:
			if i+4 >= len(p) {
				return b, ok
			}
			cx, cy := pt(i + 1)
			ex, ey := pt(i + 3)
			for _, t := range quadExtremaT(curX, cx, ex) {
				add(quadAt(curX, cx, ex, t), quadAt(curY, cy, ey, t))
			}
			for _, t := range quadExtremaT(curY, cy, ey) {
				add(quadAt(curX, cx, ex, t), quadAt(curY, cy, ey, t))
			}
			curX, curY = ex, ey
			add(curX, curY)
			i += 5
		case rasterx.PathCubicTo:
			if i+6 >= len(p) {
				return b, ok
			}
			c1x, c1y := pt(i + 1)
			c2x, c2y := pt(i + 3)
			ex, ey := pt(i + 5)
			for _, t := range cubicExtremaT(curX, c1x, c2x, ex) {
				add(cubicAt(curX, c1x, c2x, ex, t), cubicAt(curY, c1y, c2y, ey, t))
			}
			for _, t := range cubicExtremaT(curY, c1y, c2y, ey) {
				add(cubicAt(curX, c1x, c2x, ex, t), cubicAt(curY, c1y, c2y, ey, t))
			}
			curX, curY = ex, ey
			add(curX, curY)
			i += 7
		case rasterx.PathClose:
			// Z returns the current point to the subpath start; a curve that
			// follows without an explicit M begins there.
			curX, curY = startX, startY
			i++
		default:
			return b, ok
		}
	}
	if !ok {
		return b, false
	}
	return ObjectBounds{X: minX, Y: minY, W: maxX - minX, H: maxY - minY}, true
}

// quadAt evaluates a 1-D quadratic Bézier at t.
func quadAt(p0, p1, p2, t float64) float64 {
	mt := 1 - t
	return mt*mt*p0 + 2*mt*t*p1 + t*t*p2
}

// quadExtremaT returns the parameter (if any, in the open interval (0,1)) at
// which a 1-D quadratic Bézier has zero derivative.
func quadExtremaT(p0, p1, p2 float64) []float64 {
	den := p0 - 2*p1 + p2
	if den == 0 {
		return nil
	}
	if t := (p0 - p1) / den; t > 0 && t < 1 {
		return []float64{t}
	}
	return nil
}

// cubicAt evaluates a 1-D cubic Bézier at t.
func cubicAt(p0, p1, p2, p3, t float64) float64 {
	mt := 1 - t
	return mt*mt*mt*p0 + 3*mt*mt*t*p1 + 3*mt*t*t*p2 + t*t*t*p3
}

// cubicExtremaT returns the parameters in the open interval (0,1) at which a
// 1-D cubic Bézier has zero derivative (the roots of the quadratic derivative).
func cubicExtremaT(p0, p1, p2, p3 float64) []float64 {
	a := -p0 + 3*p1 - 3*p2 + p3
	bq := 2 * (p0 - 2*p1 + p2)
	c := p1 - p0
	var ts []float64
	keep := func(t float64) {
		if t > 0 && t < 1 {
			ts = append(ts, t)
		}
	}
	if math.Abs(a) < 1e-12 {
		if bq != 0 {
			keep(-c / bq)
		}
		return ts
	}
	disc := bq*bq - 4*a*c
	if disc < 0 {
		return ts
	}
	sq := math.Sqrt(disc)
	keep((-bq + sq) / (2 * a))
	keep((-bq - sq) / (2 * a))
	return ts
}

// gradientColorFunc builds the color function for a gradient fill or stroke,
// respecting the draw transform m for both coordinate systems.
//
//   - userSpaceOnUse: gradient coordinates are user space, so m is applied via
//     GetColorFunctionUS's objMatrix (fixes the confirmed bug where these
//     ignored SetTarget scaling).
//   - objectBoundingBox: the gradient lives in the shape's own (pre-transform)
//     bounding box userBounds, so it must rotate/skew with the shape. rasterx's
//     GetColorFunctionUS ignores objMatrix for this branch: it samples device
//     pixels through gradT = B·Matrix⁻¹·B⁻¹ (B maps the unit square onto
//     Bounds) and compares against endpoints laid out in Bounds space. Folding
//     the device→user transform m⁻¹ into that sample path by setting
//     Matrix = B⁻¹·m·B·G (G is the SVG gradientTransform) gives
//     gradT = B·G⁻¹·B⁻¹·m⁻¹, i.e. each device pixel is mapped back to user
//     space and then through the inverse gradient transform, exactly the
//     objectBoundingBox semantics for any m. Bounds is the user-space bbox.
//     A degenerate bbox or singular m falls back to positioning the gradient
//     over devExtent (the device-space extent of the rasterized path), which
//     is exact for axis-aligned transforms.
//
// g is a value copy, but its Stops slice aliases the shared gradient; it is
// copied locally because GetColorFunctionUS sorts the stops in place, which
// would otherwise race on the shared slice under concurrent draws.
func gradientColorFunc(g rasterx.Gradient, opacity float64, m rasterx.Matrix2D, userBounds ObjectBounds, devExtent fixed.Rectangle26_6) interface{} {
	stops := make([]rasterx.GradStop, len(g.Stops))
	copy(stops, g.Stops)
	g.Stops = stops

	if g.Units == rasterx.ObjectBoundingBox {
		det := m.A*m.D - m.B*m.C
		if userBounds.W > 0 && userBounds.H > 0 && det != 0 {
			bbox := rasterx.Identity.Translate(userBounds.X, userBounds.Y).Scale(userBounds.W, userBounds.H)
			g.Bounds.X, g.Bounds.Y = userBounds.X, userBounds.Y
			g.Bounds.W, g.Bounds.H = userBounds.W, userBounds.H
			g.Matrix = bbox.Invert().Mult(m).Mult(bbox).Mult(g.Matrix)
			return g.GetColorFunctionUS(opacity, rasterx.Identity)
		}
		mnx, mny := float64(devExtent.Min.X)/64, float64(devExtent.Min.Y)/64
		mxx, mxy := float64(devExtent.Max.X)/64, float64(devExtent.Max.Y)/64
		g.Bounds.X, g.Bounds.Y = mnx, mny
		g.Bounds.W, g.Bounds.H = mxx-mnx, mxy-mny
		return g.GetColorFunctionUS(opacity, rasterx.Identity)
	}
	return g.GetColorFunctionUS(opacity, m)
}

// clamp01 clamps v to the closed interval [0,1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// GetFillColor returns the fill color of the SvgPath if one is defined and otherwise returns colornames.Black
func (svgp *SvgPath) GetFillColor() color.Color {
	return getColor(svgp.fillerColor)
}

// GetLineColor returns the stroke color of the SvgPath if one is defined and otherwise returns colornames.Black
func (svgp *SvgPath) GetLineColor() color.Color {
	return getColor(svgp.linerColor)
}

// SetFillColor sets the fill color of the SvgPath
func (svgp *SvgPath) SetFillColor(clr color.Color) {
	svgp.fillerColor = clr
}

// SetLineColor sets the line color of the SvgPath
func (svgp *SvgPath) SetLineColor(clr color.Color) {
	svgp.linerColor = clr
}
