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
				rf.SetColor(gradientColorFunc(fillerColor, fillOpacity, madder.M, rf.Scanner.GetPathExtent()))
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
				r.SetColor(gradientColorFunc(linerColor, lineOpacity, madder.M, r.Scanner.GetPathExtent()))
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

// pathBounds computes a conservative axis-aligned bounding box of p in its own
// (pre-transform) coordinate space by walking the fixed-point path ops,
// including control points. ok is false for empty paths (no vertices), which
// callers use to skip paint that requires an object bounding box.
func pathBounds(p rasterx.Path) (b ObjectBounds, ok bool) {
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	add := func(xf, yf fixed.Int26_6) {
		x, y := float64(xf)/64, float64(yf)/64
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
	for i := 0; i < len(p); {
		switch rasterx.PathCommand(p[i]) {
		case rasterx.PathMoveTo:
			if i+2 >= len(p) {
				return b, ok
			}
			add(p[i+1], p[i+2])
			i += 3
		case rasterx.PathLineTo:
			if i+2 >= len(p) {
				return b, ok
			}
			add(p[i+1], p[i+2])
			i += 3
		case rasterx.PathQuadTo:
			if i+4 >= len(p) {
				return b, ok
			}
			add(p[i+1], p[i+2])
			add(p[i+3], p[i+4])
			i += 5
		case rasterx.PathCubicTo:
			if i+6 >= len(p) {
				return b, ok
			}
			add(p[i+1], p[i+2])
			add(p[i+3], p[i+4])
			add(p[i+5], p[i+6])
			i += 7
		case rasterx.PathClose:
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

// gradientColorFunc builds the color function for a gradient fill or stroke,
// respecting the draw transform for both coordinate systems.
//
//   - userSpaceOnUse: gradient coordinates are user space, so the draw
//     transform m is applied via GetColorFunctionUS's objMatrix (fixes the
//     confirmed bug where these ignored SetTarget scaling).
//   - objectBoundingBox: rasterx's GetColorFunctionUS ignores objMatrix for
//     this branch and samples in device space, so the gradient is positioned
//     over devExtent (the device-space extent of the already-rasterized path)
//     and objMatrix is Identity. This keeps objectBoundingBox gradients correct
//     under any transform.
//
// g is a value copy, but its Stops slice aliases the shared gradient; it is
// copied locally because GetColorFunctionUS sorts the stops in place, which
// would otherwise race on the shared slice under concurrent draws.
func gradientColorFunc(g rasterx.Gradient, opacity float64, m rasterx.Matrix2D, devExtent fixed.Rectangle26_6) interface{} {
	stops := make([]rasterx.GradStop, len(g.Stops))
	copy(stops, g.Stops)
	g.Stops = stops

	if g.Units == rasterx.ObjectBoundingBox {
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
