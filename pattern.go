// Copyright 2026 The oksvg Authors. All rights reserved.
//
// pattern.go implements SVG patterns as custom rasterx color functions.

package oksvg

import (
	"image"
	"image/color"
	"math"

	"github.com/srwiley/rasterx"
)

// maxPatternTileDim caps the per-axis dimension of a rasterized pattern tile
// so attacker-controlled width/height/scale in an SVG can't trigger an
// unbounded image.NewRGBA allocation.
const maxPatternTileDim = 4096

// Bound both long chains and repeated sibling references. The pixel budget is
// cumulative over one Draw, including tiles that have already been released.
const (
	maxPatternDepth  = 40
	maxPatternTiles  = 1024
	maxPatternPixels = 32 << 20
)

// patternRenderState belongs to a single draw, never to the shared icon.
type patternRenderState struct {
	active map[*Pattern]bool
	tiles  int
	pixels int
}

// ObjectBounds is the axis-aligned bounding box of the object being painted,
// expressed in user space (pre-transform). It positions objectBoundingBox
// gradients and patterns.
type ObjectBounds struct {
	X, Y, W, H float64
}

// Pattern holds the data of an SVG pattern element.
type Pattern struct {
	ID                  string
	X, Y, Width, Height float64
	Units               string // "userSpaceOnUse" or "objectBoundingBox"
	ContentUnits        string // "userSpaceOnUse" or "objectBoundingBox"
	Transform           rasterx.Matrix2D
	Paths               []SvgPath
}

// GetColorFunction returns a ColorFunc that implements the tiling logic for
// this pattern. objBounds is the user-space bounding box of the painted object
// and parentTransform is the accumulated draw transform (user space -> device).
func (p *Pattern) GetColorFunction(opacity float64, objBounds ObjectBounds, parentTransform rasterx.Matrix2D) rasterx.ColorFunc {
	return p.getColorFunction(opacity, objBounds, parentTransform, &patternRenderState{})
}

// getColorFunction is the recursion-aware implementation of GetColorFunction.
// state.active is the set of patterns currently being rasterized on the call graph;
// re-entering a pattern that is already active is a cycle and paints
// transparent. This replaces the old shared `rendering bool` flag, which was a
// data race under concurrent draws and produced blank tiles.
func (p *Pattern) getColorFunction(opacity float64, objBounds ObjectBounds, parentTransform rasterx.Matrix2D, state *patternRenderState) rasterx.ColorFunc {
	transparent := func(xi, yi int) color.Color { return color.Transparent }

	if state == nil {
		state = &patternRenderState{}
	}
	if state.active[p] || len(state.active) >= maxPatternDepth || state.tiles >= maxPatternTiles {
		return transparent
	}
	if state.active == nil {
		state.active = map[*Pattern]bool{}
	}
	state.active[p] = true
	// Remove p from the active set once its tile is rasterized so that a
	// sibling (non-cyclic) reference to the same pattern can still render.
	defer delete(state.active, p)

	var x, y, w, h float64
	if p.Units == "objectBoundingBox" {
		x = objBounds.X + objBounds.W*p.X
		y = objBounds.Y + objBounds.H*p.Y
		w = objBounds.W * p.Width
		h = objBounds.H * p.Height
	} else {
		x = p.X
		y = p.Y
		w = p.Width
		h = p.Height
	}

	if w <= 0 || h <= 0 || math.IsNaN(w) || math.IsNaN(h) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return transparent
	}

	// Compute combined scale factor to render pattern at target resolution
	combinedTransform := parentTransform.Mult(p.Transform)
	det := combinedTransform.A*combinedTransform.D - combinedTransform.B*combinedTransform.C
	if math.IsNaN(det) || math.IsInf(det, 0) || math.Abs(det) < 1e-12 {
		return transparent
	}
	scaleX := math.Sqrt(combinedTransform.A*combinedTransform.A + combinedTransform.B*combinedTransform.B)
	scaleY := math.Sqrt(combinedTransform.C*combinedTransform.C + combinedTransform.D*combinedTransform.D)
	if scaleX <= 0 {
		scaleX = 1
	}
	if scaleY <= 0 {
		scaleY = 1
	}

	// Clamp before converting to int, including finite dimensions whose scaled
	// product overflows. Recompute the scale to match the allocated tile.
	imgW := int(math.Min(math.Ceil(w*scaleX), maxPatternTileDim))
	imgH := int(math.Min(math.Ceil(h*scaleY), maxPatternTileDim))
	if imgW <= 0 {
		imgW = 1
	}
	if imgH <= 0 {
		imgH = 1
	}
	scaleX = float64(imgW) / w
	scaleY = float64(imgH) / h
	if imgW*imgH > maxPatternPixels-state.pixels {
		return transparent
	}
	state.tiles++
	state.pixels += imgW * imgH

	img := image.NewRGBA(image.Rect(0, 0, imgW, imgH))
	scanner := rasterx.NewScannerGV(imgW, imgH, img, img.Bounds())
	dasher := rasterx.NewDasher(imgW, imgH, scanner)

	var contentTransform rasterx.Matrix2D
	if p.ContentUnits == "objectBoundingBox" {
		contentTransform = rasterx.Identity.Scale(scaleX*w, scaleY*h)
	} else {
		contentTransform = rasterx.Identity.Scale(scaleX, scaleY)
	}

	// Draw pattern sub-paths onto tile image. Route through the internal draw
	// so nested *Pattern fills share the active set and cycles are caught.
	for i := range p.Paths {
		p.Paths[i].drawTransformedInternal(dasher, opacity, contentTransform, state)
	}

	invCombined := combinedTransform.Invert()

	return func(xi, yi int) color.Color {
		xVal, yVal := invCombined.Transform(float64(xi)+0.5, float64(yi)+0.5)
		px := xVal - x
		py := yVal - y

		tx := math.Mod(px, w)
		if tx < 0 {
			tx += w
		}
		ty := math.Mod(py, h)
		if ty < 0 {
			ty += h
		}

		ix := int(tx * scaleX)
		iy := int(ty * scaleY)

		if ix < 0 {
			ix = 0
		}
		if ix >= imgW {
			ix = imgW - 1
		}
		if iy < 0 {
			iy = 0
		}
		if iy >= imgH {
			iy = imgH - 1
		}

		return img.At(ix, iy)
	}
}
