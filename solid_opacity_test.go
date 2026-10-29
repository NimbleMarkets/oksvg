package oksvg

import (
	"fmt"
	"image/color"
	"testing"
)

func TestSolidColorOpacityPreservesIntrinsicAlpha(t *testing.T) {
	for _, c := range []color.Color{color.NRGBA{R: 255, A: 128}, color.RGBA{R: 128, A: 128}, color.NRGBA64{R: 65535, A: 32896}} {
		got := solidColorOpacity(c, 0.5)
		if got != (color.NRGBA{R: 255, A: 64}) {
			t.Fatalf("%T: %+v", c, got)
		}
	}
	if got := solidColorOpacity(color.NRGBA{R: 255, A: 0}, 1); got.A != 0 {
		t.Fatal("transparent became opaque")
	}
}
func TestSolidRGBAFillAndStroke(t *testing.T) {
	for _, paint := range []string{"fill", "stroke"} {
		for _, opacity := range []float64{1, 0.5} {
			t.Run(fmt.Sprintf("%s/%g", paint, opacity), func(t *testing.T) {
				shape := fmt.Sprintf(`<rect x="5" y="5" width="10" height="10" fill="rgba(255,0,0,0.5)" fill-opacity="%g"/>`, opacity)
				if paint == "stroke" {
					shape = fmt.Sprintf(`<path d="M5 10H15" stroke="rgba(255,0,0,0.5)" stroke-opacity="%g" stroke-width="6"/>`, opacity)
				}
				img := renderSVG(t, `<svg width="20" height="20"><rect width="20" height="20" fill="white"/>`+shape+`</svg>`, 20, 20, StrictErrorMode)
				r, g, b, a := rgba8(img, 10, 10)
				want := 255 - int(128*opacity)
				if r != 255 || a != 255 || int(g) < want-1 || int(g) > want+1 || b != g {
					t.Fatalf("got %d,%d,%d,%d want red over white at alpha %g", r, g, b, a, 0.5*opacity)
				}
			})
		}
	}
}
