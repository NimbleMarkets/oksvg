package oksvg

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"

	"github.com/srwiley/rasterx"
)

func TestImageElementAllowlist(t *testing.T) {
	src := image.NewRGBA(image.Rect(3, 5, 7, 7))
	draw.Draw(src, src.Bounds(), image.NewUniform(color.RGBA{R: 255, A: 255}), image.Point{}, draw.Src)
	for _, tc := range []struct {
		name, attrs, parent string
		point               image.Point
		alpha               uint32
	}{
		{"meet letterbox", "", "", image.Pt(5, 1), 0},
		{"meet center", "", "", image.Pt(5, 5), 65535},
		{"stretch", `preserveAspectRatio="none"`, "", image.Pt(5, 1), 65535},
		{"slice", `preserveAspectRatio="xMidYMid slice"`, "", image.Pt(5, 1), 65535},
		{"slice clip", `preserveAspectRatio="xMidYMid slice"`, "", image.Pt(12, 5), 0},
		{"hidden", `display="none"`, "", image.Pt(5, 5), 0},
		{"parent hidden", "", `style="display:none"`, image.Pt(5, 5), 0},
		{"opacity", `opacity="0.5"`, "", image.Pt(5, 5), 32896},
		{"transform", "", `transform="translate(10 10)"`, image.Pt(15, 15), 65535},
	} {
		t.Run(tc.name, func(t *testing.T) {
			markup := `<svg width="30" height="30"><g ` + tc.parent + `><image href="asset" width="10" height="10" ` + tc.attrs + `/></g></svg>`
			icon, err := ReadIconStreamWithImages(strings.NewReader(markup), map[string]image.Image{"asset": src}, StrictErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			dst := image.NewRGBA(image.Rect(0, 0, 30, 30))
			icon.SetTarget(0, 0, 30, 30)
			icon.DrawTarget = dst
			icon.Draw(rasterx.NewDasher(30, 30, rasterx.NewScannerGV(30, 30, dst, dst.Bounds())), 1)
			_, _, _, a := dst.At(tc.point.X, tc.point.Y).RGBA()
			if a != tc.alpha {
				t.Fatalf("alpha %d, want %d", a, tc.alpha)
			}
		})
	}
	icon, err := ReadIconStream(strings.NewReader(`<svg><image href="https://example.invalid/a.png" width="10" height="10"/></svg>`), StrictErrorMode)
	if err != nil || len(icon.SVGImages) != 0 {
		t.Fatalf("default image loading: %v %v", icon, err)
	}
}
