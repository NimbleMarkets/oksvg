package oksvg

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
)

// SvgImage represents a parsed bitmap glyph/image and its transform/opacity properties.
type SvgImage struct {
	Image     image.Image
	Transform rasterx.Matrix2D
	Opacity   float64
}

// DrawTransformed draws the SvgImage onto the destination image, applying scaling, translation, and opacity.
func (svgi *SvgImage) DrawTransformed(dst draw.Image, opacity float64, t rasterx.Matrix2D) {
	if dst == nil || svgi.Image == nil {
		return
	}

	// Combine parent transform matrix t with image matrix
	m := t.Mult(svgi.Transform)

	// Extract scaling and translation from combined matrix:
	// A = sx (X-scaling), D = sy (Y-scaling), E = tx (X-translation), F = ty (Y-translation)
	sx := m.A
	sy := m.D
	tx := m.E
	ty := m.F

	srcBounds := svgi.Image.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()

	dstW := int(float64(srcW) * sx)
	dstH := int(float64(srcH) * sy)

	if dstW <= 0 || dstH <= 0 {
		return
	}

	// Allocate temporary image for scaling
	scaledImg := image.NewRGBA(image.Rect(0, 0, dstW, dstH))

	// Scale the image using Bilinear interpolation from golang.org/x/image/draw
	xdraw.BiLinear.Scale(scaledImg, scaledImg.Bounds(), svgi.Image, srcBounds, draw.Over, nil)

	// Compute drawing offset rect
	targetRect := scaledImg.Bounds().Add(image.Pt(int(tx), int(ty)))

	// Composite onto destination image, applying opacity if less than 1.0
	finalOpacity := svgi.Opacity * opacity
	if finalOpacity < 0.99 {
		mask := image.NewUniform(color.Alpha{A: uint8(finalOpacity * 255)})
		draw.DrawMask(dst, targetRect, scaledImg, image.Point{}, mask, image.Point{}, draw.Over)
	} else {
		draw.Draw(dst, targetRect, scaledImg, image.Point{}, draw.Over)
	}
}
