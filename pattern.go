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
	return p.getColorFunction(opacity, objBounds, parentTransform, map[*Pattern]bool{})
}

// getColorFunction is the recursion-aware implementation of GetColorFunction.
// active is the set of patterns currently being rasterized on the call graph;
// re-entering a pattern that is already active is a cycle and paints
// transparent. This replaces the old shared `rendering bool` flag, which was a
// data race under concurrent draws and produced blank tiles.
func (p *Pattern) getColorFunction(opacity float64, objBounds ObjectBounds, parentTransform rasterx.Matrix2D, active map[*Pattern]bool) rasterx.ColorFunc {
	transparent := func(xi, yi int) color.Color { return color.Transparent }

	if active == nil {
		active = map[*Pattern]bool{}
	}
	if active[p] {
		return transparent
	}
	active[p] = true
	// Remove p from the active set once its tile is rasterized so that a
	// sibling (non-cyclic) reference to the same pattern can still render.
	defer delete(active, p)

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

	if w <= 0 || h <= 0 {
		return transparent
	}

	// Compute combined scale factor to render pattern at target resolution
	combinedTransform := parentTransform.Mult(p.Transform)
	scaleX := math.Sqrt(combinedTransform.A*combinedTransform.A + combinedTransform.B*combinedTransform.B)
	scaleY := math.Sqrt(combinedTransform.C*combinedTransform.C + combinedTransform.D*combinedTransform.D)
	if scaleX <= 0 {
		scaleX = 1
	}
	if scaleY <= 0 {
		scaleY = 1
	}

	imgW := int(math.Ceil(w * scaleX))
	imgH := int(math.Ceil(h * scaleY))
	if imgW <= 0 {
		imgW = 1
	}
	if imgH <= 0 {
		imgH = 1
	}
	// Clamp tile dimensions and re-derive scaleX/scaleY so the rasterized tile
	// stays consistent with the actual image size after clamping.
	if imgW > maxPatternTileDim {
		imgW = maxPatternTileDim
		scaleX = float64(imgW) / w
	}
	if imgH > maxPatternTileDim {
		imgH = maxPatternTileDim
		scaleY = float64(imgH) / h
	}

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
		p.Paths[i].drawTransformedInternal(dasher, opacity, contentTransform, active)
	}

	// Guard against singular combined transforms (det == 0). Invert would
	// otherwise produce Inf/NaN entries and corrupt the tile lookup.
	det := combinedTransform.A*combinedTransform.D - combinedTransform.B*combinedTransform.C
	if math.Abs(det) < 1e-12 {
		return transparent
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
