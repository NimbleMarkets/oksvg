package oksvg

import (
	"encoding/xml"
	"fmt"
	"math"
	"strings"

	"github.com/srwiley/rasterx"
)

// imageF resolves raster content exclusively from the caller's allowlist.
func imageF(c *IconCursor, attrs []xml.Attr) error {
	// Pattern and marker compilers retain vector paths only. Report this
	// limitation before resolving a resource that would otherwise be discarded.
	if c.patternDepth > 0 || c.markerDepth > 0 {
		err := fmt.Errorf("SVG image elements inside patterns or markers are unsupported")
		if c.returnError(err.Error()) {
			return err
		}
		return nil
	}
	style := c.StyleStack[len(c.StyleStack)-1]
	if style.displayNone || style.visibilityHidden {
		return nil
	}
	var href, legacyHref, src string
	var x, y, w, h float64
	aspect := "xMidYMid meet"
	for _, a := range attrs {
		var err error
		switch a.Name.Local {
		case "href":
			if a.Name.Space == "" {
				href = a.Value
			} else if a.Name.Space == "http://www.w3.org/1999/xlink" || a.Name.Space == "xlink" {
				legacyHref = a.Value
			}
		case "src":
			src = a.Value
		case "x":
			x, err = c.readLength(a.Value, horizontal)
		case "y":
			y, err = c.readLength(a.Value, vertical)
		case "width":
			w, err = c.readLength(a.Value, horizontal)
		case "height":
			h, err = c.readLength(a.Value, vertical)
		case "preserveAspectRatio":
			aspect = strings.TrimSpace(a.Value)
		}
		if err != nil {
			return err
		}
	}
	if href == "" {
		href = legacyHref
	}
	if href == "" {
		href = src
	}
	if w < 0 || h < 0 {
		return fmt.Errorf("SVG image width and height must be non-negative")
	}
	img := c.images[href]
	if img == nil || w == 0 || h == 0 {
		return nil
	}
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil
	}
	if sw > 4096 || sh > 4096 || sw > (4<<20)/sh {
		return fmt.Errorf("SVG image dimensions exceed limit")
	}
	for _, v := range []float64{x, y, w, h} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 32768 {
			return fmt.Errorf("SVG image coordinate exceeds limit")
		}
	}
	sx, sy := w/float64(sw), h/float64(sh)
	var viewport *imageViewport
	fields := strings.Fields(aspect)
	if len(fields) > 0 && fields[0] == "defer" {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		fields = []string{"xMidYMid"}
	}
	if fields[0] != "none" {
		align := fields[0]
		if len(align) != 8 || (align[:4] != "xMin" && align[:4] != "xMid" && align[:4] != "xMax") || (align[4:] != "YMin" && align[4:] != "YMid" && align[4:] != "YMax") {
			return fmt.Errorf("invalid SVG image preserveAspectRatio")
		}
		if len(fields) > 1 && fields[1] != "meet" && fields[1] != "slice" {
			return fmt.Errorf("SVG image preserveAspectRatio %s is unsupported", fields[1])
		}
		scale := math.Min(sx, sy)
		if len(fields) > 1 && fields[1] == "slice" {
			scale = math.Max(sx, sy)
			viewport = &imageViewport{x: x, y: y, w: w, h: h, transform: style.mAdder.M}
		}
		dx, dy := w-float64(sw)*scale, h-float64(sh)*scale
		switch align[:4] {
		case "xMid":
			x += dx / 2
		case "xMax":
			x += dx
		}
		switch align[4:] {
		case "YMid":
			y += dy / 2
		case "YMax":
			y += dy
		}
		sx, sy = scale, scale
	}
	transform := style.mAdder.M.Mult(rasterx.Identity.Translate(x, y).Scale(sx, sy).Translate(-float64(b.Min.X), -float64(b.Min.Y)))
	c.icon.SVGImages = append(c.icon.SVGImages, SvgImage{Image: img, Transform: transform, Opacity: style.groupOpacity, order: c.icon.nextOrder(), viewport: viewport})
	return nil
}
