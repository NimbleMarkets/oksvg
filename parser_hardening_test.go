package oksvg

import (
	"image/color"
	"strings"
	"testing"
)

func TestInvalidFillKeepsInheritedPaint(t *testing.T) {
	for _, mode := range []ErrorMode{IgnoreErrorMode, WarnErrorMode} {
		for _, attrs := range []string{`fill="invalid"`, `style="fill:invalid"`, `class="bad"`} {
			icon, err := ReadIconStream(strings.NewReader(`<svg><defs><style>.bad{fill:invalid}</style></defs><g fill="red"><rect width="10" height="10" `+attrs+`/></g></svg>`), mode)
			if err != nil {
				t.Fatal(err)
			}
			if len(icon.SVGPaths) != 1 || icon.SVGPaths[0].GetFillColor() != (color.NRGBA{R: 255, A: 255}) {
				t.Errorf("mode %d: invalid fill replaced inherited red", mode)
			}
		}
	}
}

func TestPatternUnknownElementStrict(t *testing.T) {
	for _, forward := range []bool{false, true} {
		defs := `<defs><pattern id="p" width="1" height="1"><unsupported/></pattern></defs>`
		shape := `<rect width="10" height="10" fill="url(#p)"/>`
		body := defs + shape
		if forward {
			body = shape + defs
		}
		if _, err := ReadIconStream(strings.NewReader(`<svg>`+body+`</svg>`), StrictErrorMode); err == nil {
			t.Errorf("forward=%v: unknown pattern child accepted in strict mode", forward)
		}
	}
}

func TestPatternSkippedGradientMetadata(t *testing.T) {
	for _, tag := range []string{"title", "desc"} {
		t.Run(tag, func(t *testing.T) {
			svg := `<svg><pattern><linearGradient><` + tag + `>metadata</` + tag + `></linearGradient></pattern>` + " \n" +
				`<rect width="1" height="1"/></svg>`
			icon, err := ReadIconStream(strings.NewReader(svg), IgnoreErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			metadata := icon.Titles
			if tag == "desc" {
				metadata = icon.Descriptions
			}
			if len(metadata) != 1 || metadata[0] != "metadata" {
				t.Fatalf("character data after metadata end leaked into %s: %q", tag, metadata)
			}
		})
	}
}
