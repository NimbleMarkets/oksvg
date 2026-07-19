// Copyright 2018 The oksvg Authors. All rights reserved.
package oksvg_test

import (
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	. "github.com/NimbleMarkets/oksvg"
	. "github.com/srwiley/rasterx"
)

// newQuadImage returns a 2x2 RGBA image with a distinct opaque color in
// each corner, useful for asserting exactly where pixels end up after a
// geometric transform: (0,0)=red, (1,0)=green, (0,1)=blue, (1,1)=white.
func newQuadImage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(1, 0, color.RGBA{G: 255, A: 255})
	img.Set(0, 1, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	return img
}

// Rotating 90 degrees about the image's own origin and then translating by
// (3,3) should move each source pixel to a predictable destination cell
// (verified against golang.org/x/image/draw's own affine convention), not
// leave the image unrotated/unmoved or, worse, silently mis-scaled.
func TestDrawTransformed_Rotate90MovesPixels(t *testing.T) {
	src := newQuadImage()
	svgi := &SvgImage{
		Image:     src,
		Transform: Identity.Translate(3, 3).Rotate(math.Pi / 2),
		Opacity:   1,
	}
	dst := image.NewRGBA(image.Rect(0, 0, 8, 8))
	svgi.DrawTransformed(dst, 1, Identity)

	cases := []struct {
		x, y int
		want color.RGBA
	}{
		{2, 3, color.RGBA{R: 255, A: 255}},                 // src(0,0) red
		{2, 4, color.RGBA{G: 255, A: 255}},                 // src(1,0) green
		{1, 3, color.RGBA{B: 255, A: 255}},                 // src(0,1) blue
		{1, 4, color.RGBA{R: 255, G: 255, B: 255, A: 255}}, // src(1,1) white
	}
	for _, c := range cases {
		if got := dst.RGBAAt(c.x, c.y); got != c.want {
			t.Errorf("pixel (%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if a := dst.RGBAAt(0, 0).A; a != 0 {
		t.Errorf("expected corner (0,0) to remain untouched/transparent, got alpha %d", a)
	}
}

// A pure X-mirror (scale(-1,1)) must still draw the image -- not vanish, as
// the previous int(sx)/int(sy) based sizing did for a negative scale -- and
// must actually flip it left-right.
func TestDrawTransformed_MirrorScaleStillDraws(t *testing.T) {
	src := newQuadImage()
	svgi := &SvgImage{
		Image:     src,
		Transform: Identity.Translate(2, 0).Scale(-1, 1),
		Opacity:   1,
	}
	dst := image.NewRGBA(image.Rect(0, 0, 4, 4))
	svgi.DrawTransformed(dst, 1, Identity)

	cases := []struct {
		x, y int
		want color.RGBA
	}{
		{1, 0, color.RGBA{R: 255, A: 255}},                 // src(0,0) red, mirrored to the right
		{0, 0, color.RGBA{G: 255, A: 255}},                 // src(1,0) green, mirrored to the left
		{1, 1, color.RGBA{B: 255, A: 255}},                 // src(0,1) blue
		{0, 1, color.RGBA{R: 255, G: 255, B: 255, A: 255}}, // src(1,1) white
	}
	for _, c := range cases {
		if got := dst.RGBAAt(c.x, c.y); got != c.want {
			t.Errorf("pixel (%d,%d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

// A huge scale factor must not attempt to allocate an intermediate image
// proportional to the scale (the old implementation allocated a
// srcW*scale x srcH*scale image.RGBA, which is a multi-terabyte allocation
// at scale(1e6)); drawing should stay bounded by dst's own size and finish
// quickly, touching only dst's bounds.
func TestDrawTransformed_HugeScaleBoundedAndFast(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			src.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	svgi := &SvgImage{Image: src, Transform: Identity.Scale(1e6, 1e6), Opacity: 1}
	dst := image.NewRGBA(image.Rect(0, 0, 8, 8))

	start := time.Now()
	svgi.DrawTransformed(dst, 1, Identity)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("DrawTransformed with scale(1e6) took %v, want well under 2s (suspected unbounded intermediate allocation)", elapsed)
	}

	want := color.RGBA{R: 255, A: 255}
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			if got := dst.RGBAAt(x, y); got != want {
				t.Errorf("pixel (%d,%d) = %v, want %v (dst should be fully covered by the huge scaled source)", x, y, got, want)
			}
		}
	}
}

// Opacity 0.5 should halve the composited alpha (and, since the source is
// fully opaque, the premultiplied color channels along with it) -- not get
// clamped to full opacity by a 0.99 cutoff as the previous implementation
// did.
func TestDrawTransformed_OpacityHalvesAlpha(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			src.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	svgi := &SvgImage{Image: src, Transform: Identity, Opacity: 1}
	dst := image.NewRGBA(image.Rect(0, 0, 2, 2))
	svgi.DrawTransformed(dst, 0.5, Identity)

	want := color.RGBA{R: 128, A: 128} // premultiplied: opaque red at 50% coverage
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			if got := dst.RGBAAt(x, y); got != want {
				t.Errorf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

// Nil dst or nil source image must be a no-op, not a panic.
func TestDrawTransformed_NilInputsNoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("DrawTransformed panicked on nil input: %v", r)
		}
	}()
	svgi := &SvgImage{Image: newQuadImage(), Transform: Identity, Opacity: 1}
	svgi.DrawTransformed(nil, 1, Identity)

	empty := &SvgImage{Transform: Identity, Opacity: 1}
	dst := image.NewRGBA(image.Rect(0, 0, 2, 2))
	empty.DrawTransformed(dst, 1, Identity)
}
