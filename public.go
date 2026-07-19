// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// public.go implements the public SVG-reading entry points and SVG color
// parsing.

package oksvg

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image/color"
	"io"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/colornames"
	"golang.org/x/net/html/charset"
)

// ReadIconStream reads the Icon from the given io.Reader.
// This only supports a sub-set of SVG, but
// is enough to draw many icons. If errMode is provided,
// the first value determines if the icon ignores, errors out, or logs a warning
// if it does not handle an element found in the icon file. Ignore warnings is
// the default if no ErrorMode value is provided.
func ReadIconStream(stream io.Reader, errMode ...ErrorMode) (*SvgIcon, error) {
	icon := &SvgIcon{Defs: make(map[string][]definition), Grads: make(map[string]*rasterx.Gradient), Patterns: make(map[string]*Pattern), Transform: rasterx.Identity}
	cursor := &IconCursor{StyleStack: []PathStyle{DefaultStyle}, icon: icon}
	if len(errMode) > 0 {
		cursor.ErrorMode = errMode[0]
	}
	decoder := xml.NewDecoder(stream)
	decoder.CharsetReader = charset.NewReaderLabel
	for {
		t, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return icon, err
		}
		// Inspect the type of the XML token
		switch se := t.(type) {
		case xml.StartElement:
			// Reads all recognized style attributes from the start element
			// and places it on top of the styleStack
			err = cursor.PushStyle(se.Attr)
			if err != nil {
				return icon, err
			}
			err = cursor.readStartElement(se)
			if err != nil {
				return icon, err
			}
			if se.Name.Local == "style" && cursor.inDefs {
				cursor.inDefsStyle = true
			}
		case xml.EndElement:
			if err = cursor.readEndElement(se); err != nil {
				return icon, err
			}
		case xml.CharData:
			// Route character data exclusively so <title>/<desc> inside <text>
			// win and never leak into the rendered text run.
			switch {
			case cursor.inTitleText:
				icon.Titles[len(icon.Titles)-1] += string(se)
			case cursor.inDescText:
				icon.Descriptions[len(icon.Descriptions)-1] += string(se)
			case cursor.inDefsStyle:
				cursor.classInfo += string(se) // += so CDATA chunking is preserved
			case cursor.inText:
				cursor.appendTextChunk(se)
			}
		}
	}
	if err := cursor.finalize(); err != nil {
		return icon, err
	}
	return icon, nil
}

// ReadReplacingCurrentColor replaces currentColor value with specified value and loads SvgIcon as ReadIconStream do.
// currentColor value should be valid hex, rgb or named color value.
func ReadReplacingCurrentColor(stream io.Reader, currentColor string, errMode ...ErrorMode) (icon *SvgIcon, err error) {
	var (
		data []byte
	)

	if data, err = io.ReadAll(stream); err != nil {
		return nil, fmt.Errorf("read data: %w", err)
	}

	if currentColor != "" && strings.Contains(string(data), "currentColor") {
		data = []byte(strings.ReplaceAll(string(data), "currentColor", currentColor))
	}

	if icon, err = ReadIconStream(bytes.NewBuffer(data), errMode...); err != nil {
		return nil, fmt.Errorf("load: %w", err)
	}

	return icon, nil
}

// ReadIcon reads the Icon from the named file.
// This only supports a sub-set of SVG, but is enough to draw many icons.
// If errMode is provided, the first value determines if the icon ignores, errors out, or logs a warning
// if it does not handle an element found in the icon file.
// Ignore warnings is the default if no ErrorMode value is provided.
func ReadIcon(iconFile string, errMode ...ErrorMode) (*SvgIcon, error) {
	fin, errf := os.Open(iconFile)
	if errf != nil {
		return nil, errf
	}
	defer fin.Close()
	return ReadIconStream(fin, errMode...)
}

// ParseSVGColorNum reads the SVG color string e.g. #FBD9BD
func ParseSVGColorNum(colorStr string) (r, g, b uint8, err error) {
	colorStr = strings.TrimPrefix(colorStr, "#")
	var t uint64
	if len(colorStr) != 6 {
		if len(colorStr) != 3 {
			err = fmt.Errorf("color string %s is not length 3 or 6 as required by SVG specification",
				colorStr)
			return
		}
		// SVG specs say duplicate characters in case of 3 digit hex number
		colorStr = string([]byte{colorStr[0], colorStr[0],
			colorStr[1], colorStr[1], colorStr[2], colorStr[2]})
	}
	for _, v := range []struct {
		c *uint8
		s string
	}{
		{&r, colorStr[0:2]},
		{&g, colorStr[2:4]},
		{&b, colorStr[4:6]}} {
		t, err = strconv.ParseUint(v.s, 16, 8)
		if err != nil {
			return
		}
		*v.c = uint8(t)
	}
	return
}

// ParseSVGColor parses an SVG color string in all forms
// including all SVG1.1 names, obtained from the image.colornames package
func ParseSVGColor(colorStr string) (color.Color, error) {
	v := strings.ToLower(strings.TrimSpace(colorStr))
	if strings.HasPrefix(v, "url") { // We are not handling urls
		// and gradients and stuff at this point
		return color.NRGBA{0, 0, 0, 255}, nil
	}
	switch v {
	case "none", "":
		// nil signals that the function (fill or stroke) is off;
		// not the same as black
		return nil, nil
	case "transparent":
		return color.NRGBA{0, 0, 0, 0}, nil
	default:
		cn, ok := colornames.Map[v]
		if ok {
			r, g, b, a := cn.RGBA()
			return color.NRGBA{uint8(r), uint8(g), uint8(b), uint8(a)}, nil
		}
	}

	if cStr := strings.TrimPrefix(v, "rgba("); cStr != v {
		vals := strings.Split(strings.TrimSuffix(cStr, ")"), ",")
		if len(vals) != 4 {
			return color.NRGBA{}, errParamMismatch
		}
		var cvals [3]uint8
		for i := 0; i < 3; i++ {
			if strings.TrimSpace(vals[i]) == "" {
				return nil, errParamMismatch
			}
			cv, err := parseColorValue(vals[i])
			if err != nil {
				return nil, err
			}
			cvals[i] = cv
		}
		a, err := parseAlphaValue(vals[3])
		if err != nil {
			return nil, err
		}
		return color.NRGBA{cvals[0], cvals[1], cvals[2], a}, nil
	}

	if cStr := strings.TrimPrefix(v, "rgb("); cStr != v {
		vals := strings.Split(strings.TrimSuffix(cStr, ")"), ",")
		if len(vals) != 3 {
			return color.NRGBA{}, errParamMismatch
		}
		var cvals [3]uint8
		for i := range cvals {
			// Guard against empty components (e.g. "rgb(1,,1)") before
			// parseColorValue indexes them.
			if strings.TrimSpace(vals[i]) == "" {
				return nil, errParamMismatch
			}
			cv, err := parseColorValue(vals[i])
			if err != nil {
				return nil, err
			}
			cvals[i] = cv
		}
		return color.NRGBA{cvals[0], cvals[1], cvals[2], 0xFF}, nil
	}

	if cStr := strings.TrimPrefix(v, "hsla("); cStr != v {
		vals := strings.Split(strings.TrimSuffix(cStr, ")"), ",")
		if len(vals) != 4 {
			return color.NRGBA{}, errParamMismatch
		}
		r, g, b, err := hslToNRGB(vals[0], vals[1], vals[2])
		if err != nil {
			return color.NRGBA{}, err
		}
		a, err := parseAlphaValue(vals[3])
		if err != nil {
			return nil, err
		}
		return color.NRGBA{r, g, b, a}, nil
	}

	if cStr := strings.TrimPrefix(v, "hsl("); cStr != v {
		vals := strings.Split(strings.TrimSuffix(cStr, ")"), ",")
		if len(vals) != 3 {
			return color.NRGBA{}, errParamMismatch
		}
		r, g, b, err := hslToNRGB(vals[0], vals[1], vals[2])
		if err != nil {
			return color.NRGBA{}, err
		}
		return color.NRGBA{r, g, b, 0xFF}, nil
	}

	// Use the trimmed/lower-cased value (v), like every other branch above, so a
	// hex color with surrounding whitespace (e.g. " #ff0000") still parses.
	if len(v) > 0 && v[0] == '#' {
		r, g, b, err := ParseSVGColorNum(v)
		if err != nil {
			return nil, err
		}
		return color.NRGBA{r, g, b, 0xFF}, nil
	}
	return nil, errParamMismatch
}

// hslToNRGB converts hsl() string components (hue, saturation%, lightness%) to
// 8-bit RGB. The hue is parsed as a float and wrapped into [0,360); saturation
// and lightness require a % suffix, validated before use so malformed input
// (e.g. "hsl(1,,1)") errors instead of panicking.
func hslToNRGB(hStr, sStr, lStr string) (r, g, b uint8, err error) {
	Hf, err := strconv.ParseFloat(strings.TrimSpace(hStr), 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid hue in hsl: '%s' (%s)", hStr, err)
	}
	Hf = math.Mod(Hf, 360)
	if Hf < 0 {
		Hf += 360
	}
	S, err := parseHSLPercent(sStr)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid saturation in hsl: '%s' (%s)", sStr, err)
	}
	L, err := parseHSLPercent(lStr)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid lightness in hsl: '%s' (%s)", lStr, err)
	}

	C := (1 - math.Abs((2*L)-1)) * S
	X := C * (1 - math.Abs(math.Mod(Hf/60, 2)-1))
	m := L - C/2

	var rp, gp, bp float64
	switch {
	case Hf < 60:
		rp, gp, bp = C, X, 0
	case Hf < 120:
		rp, gp, bp = X, C, 0
	case Hf < 180:
		rp, gp, bp = 0, C, X
	case Hf < 240:
		rp, gp, bp = 0, X, C
	case Hf < 300:
		rp, gp, bp = X, 0, C
	default:
		rp, gp, bp = C, 0, X
	}
	return clamp255((rp + m) * 255), clamp255((gp + m) * 255), clamp255((bp + m) * 255), nil
}

// parseHSLPercent parses a required-percent hsl component, e.g. "47%" -> 0.47.
func parseHSLPercent(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "%") {
		return 0, fmt.Errorf("missing %% suffix in %q", s)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, "%")), 64)
	if err != nil {
		return 0, err
	}
	return f / 100, nil
}

// parseAlphaValue parses an alpha channel expressed as a float in [0,1] or a
// percentage, clamped and scaled to an 8-bit value.
func parseAlphaValue(v string) (uint8, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, errParamMismatch
	}
	var f float64
	var err error
	if strings.HasSuffix(v, "%") {
		f, err = strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, "%")), 64)
		f /= 100
	} else {
		f, err = strconv.ParseFloat(v, 64)
	}
	if err != nil {
		return 0, err
	}
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return uint8(math.Round(f * 255)), nil
}

// clamp255 rounds and clamps a float channel value to the [0,255] uint8 range.
func clamp255(f float64) uint8 {
	f = math.Round(f)
	if f < 0 {
		f = 0
	}
	if f > 255 {
		f = 255
	}
	return uint8(f)
}
