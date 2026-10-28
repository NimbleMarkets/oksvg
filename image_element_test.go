package oksvg

import (
	"fmt"
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
		{"presentation overridden", `display="none" style="display:inline"`, "", image.Pt(5, 5), 65535},
		{"last display wins", `style="display:none;display:inline"`, "", image.Pt(5, 5), 65535},
		{"last display hides", `style="display:inline;display:none"`, "", image.Pt(5, 5), 0},
		{"inline cannot override ancestor", `style="display:inline"`, `display="none"`, image.Pt(5, 5), 0},
		{"visibility inherited", `visibility="inherit"`, `visibility="hidden"`, image.Pt(5, 5), 0},
		{"visibility inherit overrides local visible", `visibility="visible" style="visibility:inherit"`, `visibility="hidden"`, image.Pt(5, 5), 0},
		{"visibility inherit restores visible", `style="visibility:hidden;visibility:inherit"`, "", image.Pt(5, 5), 65535},
		{"visibility visible overrides ancestor", `visibility="visible"`, `visibility="hidden"`, image.Pt(5, 5), 65535},
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

func TestImageElementDimensions(t *testing.T) {
	for _, dims := range []struct {
		name, width, height string
		invalid             bool
	}{
		{"negative width", "-1", "10", true},
		{"negative height", "10", "-1", true},
		{"negative percent", "-10%", "10", true},
		{"zero width", "0", "10", false},
		{"zero height", "10", "0", false},
	} {
		for _, mode := range []ErrorMode{StrictErrorMode, WarnErrorMode, IgnoreErrorMode} {
			for _, supplied := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%d/supplied=%t", dims.name, mode, supplied), func(t *testing.T) {
					images := map[string]image.Image{}
					if supplied {
						images["asset"] = image.NewRGBA(image.Rect(0, 0, 2, 2))
					}
					markup := `<svg width="20" height="20"><image href="asset" width="` + dims.width + `" height="` + dims.height + `"/></svg>`
					icon, err := ReadIconStreamWithImages(strings.NewReader(markup), images, mode)
					if wantErr := dims.invalid && mode == StrictErrorMode; (err != nil) != wantErr {
						t.Fatalf("error = %v, want error %t", err, wantErr)
					}
					if len(icon.SVGImages) != 0 {
						t.Fatal("non-positive image dimensions produced drawable content")
					}
				})
			}
		}
	}
}

func TestImageElementUnsupportedPaintServers(t *testing.T) {
	imageSVG := `<image href="asset" width="10" height="10"/>`
	for _, tc := range []struct{ name, markup string }{
		{"pattern", `<svg width="20" height="20"><defs><pattern id="p" width="10" height="10" patternUnits="userSpaceOnUse"><rect width="10" height="10"/>` + imageSVG + `</pattern></defs><rect width="20" height="20" fill="url(#p)"/></svg>`},
		{"marker", `<svg width="20" height="20"><defs><marker id="m"><rect width="2" height="2"/>` + imageSVG + `</marker></defs><path d="M0 10 L10 10" marker-end="url(#m)"/></svg>`},
	} {
		for _, mode := range []ErrorMode{StrictErrorMode, WarnErrorMode, IgnoreErrorMode} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, mode), func(t *testing.T) {
				icon, err := ReadIconStreamWithImages(strings.NewReader(tc.markup), map[string]image.Image{"asset": image.NewRGBA(image.Rect(0, 0, 2, 2))}, mode)
				if wantErr := mode == StrictErrorMode; (err != nil) != wantErr {
					t.Fatalf("error = %v, want error %t", err, wantErr)
				}
				if len(icon.SVGImages) != 0 {
					t.Fatal("unsupported image leaked into document")
				}
				if mode != StrictErrorMode {
					if len(icon.SVGPaths) == 0 {
						t.Fatal("supported document content was dropped")
					}
					if tc.name == "pattern" && (icon.Patterns["p"] == nil || len(icon.Patterns["p"].Paths) == 0) {
						t.Fatal("supported pattern content was dropped")
					}
					if tc.name == "marker" && len(icon.SVGPaths) < 2 {
						t.Fatal("supported marker content was dropped")
					}
				}
			})
		}
	}
}
