package oksvg

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// SvgImage represents a parsed bitmap glyph/image and its transform/opacity properties.
type SvgImage struct {
	Image     image.Image
	Transform rasterx.Matrix2D
	Opacity   float64
	// order records the draw-order stamp assigned when the image was
	// appended alongside path elements, so callers that interleave
	// SvgImages with SvgPaths can render them back in document order.
	// order == 0 means legacy/unstamped.
	order int
}

// DrawTransformed draws the SvgImage onto the destination image, applying
// the full affine transform (translation, scaling, rotation, and skew) and
// opacity in a single pass via golang.org/x/image/draw. There is no
// intermediate allocation: output is written directly into dst and is
// naturally clipped to dst's own bounds, regardless of how large the
// transform's scale factors are.
func (svgi *SvgImage) DrawTransformed(dst draw.Image, opacity float64, t rasterx.Matrix2D) {
	if dst == nil || svgi.Image == nil {
		return
	}

	// Combine parent transform matrix t with the image's own matrix.
	m := t.Mult(svgi.Transform)
	aff := f64.Aff3{m.A, m.C, m.E, m.B, m.D, m.F}

	op := svgi.Opacity * opacity
	if op < 0 {
		op = 0
	}
	if op > 1 {
		op = 1
	}
	var opts *xdraw.Options
	if op < 1 {
		opts = &xdraw.Options{SrcMask: image.NewUniform(color.Alpha{A: uint8(op*255 + 0.5)}), SrcMaskP: image.Point{}}
	}
	xdraw.BiLinear.Transform(dst, aff, svgi.Image, svgi.Image.Bounds(), xdraw.Over, opts)
}
