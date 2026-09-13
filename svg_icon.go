// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// svg_icon.go holds the parsed SVG (SvgIcon) and draws it to a target.

package oksvg

import (
	"image/draw"

	"github.com/srwiley/rasterx"
)

// SvgIcon holds data from parsed SVGs.
type SvgIcon struct {
	ViewBox      struct{ X, Y, W, H float64 }
	Titles       []string // Title elements collect here
	Descriptions []string // Description elements collect here
	Grads        map[string]*rasterx.Gradient
	Patterns     map[string]*Pattern
	Defs         map[string][]definition
	SVGPaths     []SvgPath
	SVGImages    []SvgImage
	Transform    rasterx.Matrix2D
	DrawTarget   draw.Image
	classes      map[string]styleAttribute
	drawOrder    int // monotonically increasing draw-order stamp source
}

// nextOrder returns the next draw-order stamp and advances the counter. Vector
// paths and bitmap images are stamped from this shared sequence so Draw can
// interleave them in document order.
func (s *SvgIcon) nextOrder() int {
	o := s.drawOrder
	s.drawOrder++
	return o
}

// Draw rasterizes the icon. Vector shapes (SVGPaths) are painted into r, a
// rasterx.Dasher. Bitmap glyphs (SVGImages, e.g. color emoji) are composited
// into s.DrawTarget; if DrawTarget is nil they are skipped. Paths and images
// are interleaved by their draw-order stamp so document z-order is preserved.
// On equal or zero order, paths are drawn before images (legacy behavior).
func (s *SvgIcon) Draw(r *rasterx.Dasher, opacity float64) {
	patterns := &patternRenderState{}
	pi, ii := 0, 0
	for pi < len(s.SVGPaths) || ii < len(s.SVGImages) {
		drawPath := false
		switch {
		case ii >= len(s.SVGImages):
			drawPath = true
		case pi >= len(s.SVGPaths):
			drawPath = false
		default:
			// Paths first on equal/zero order (legacy behavior).
			drawPath = s.SVGPaths[pi].order <= s.SVGImages[ii].order
		}
		if drawPath {
			s.SVGPaths[pi].drawTransformedInternal(r, opacity, s.Transform, patterns)
			pi++
		} else {
			if s.DrawTarget != nil {
				s.SVGImages[ii].DrawTransformed(s.DrawTarget, opacity, s.Transform)
			}
			ii++
		}
	}
}

// SetTarget sets the Transform matrix to draw within the bounds of the
// rectangle arguments, accounting for a non-zero viewBox origin. Zero viewBox
// dimensions fall back to a scale of 1 on that axis to avoid Inf/NaN.
func (s *SvgIcon) SetTarget(x, y, w, h float64) {
	scaleW := 1.0
	if s.ViewBox.W != 0 {
		scaleW = w / s.ViewBox.W
	}
	scaleH := 1.0
	if s.ViewBox.H != 0 {
		scaleH = h / s.ViewBox.H
	}
	s.Transform = rasterx.Identity.Translate(x, y).Scale(scaleW, scaleH).Translate(-s.ViewBox.X, -s.ViewBox.Y)
}
