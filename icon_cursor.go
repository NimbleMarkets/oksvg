// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// utils.go implements translation of an SVG2.0 path into a rasterx Path.

package oksvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// maxFontSize caps font-size at parse time so fixed.Int26_6 (int32) ppem
// arithmetic can't overflow from attacker-controlled SVG.
const maxFontSize = 1000.0

// textFragment holds a piece of text with its associated style and coordinates.
type textFragment struct {
	text       string
	style      PathStyle
	x, y       float64
	dx, dy     float64
	hasX, hasY bool
}

// fontRegistry maps font-family names to parsed sfnt.Fonts. Protected by
// fontRegistryMu so RegisterFont and SVG parsing can run concurrently.
var (
	fontRegistryMu    sync.RWMutex
	fontRegistry      = map[string]*sfnt.Font{}
	fontBytesRegistry = map[string][]byte{}
	fontIndexRegistry = map[string]int{}
)

// RegisterFont registers a font under the given family name.
func RegisterFont(family string, fontBytes []byte) error {
	f, err := sfnt.Parse(fontBytes)
	if err != nil {
		return err
	}
	fontRegistryMu.Lock()
	name := strings.ToLower(family)
	fontRegistry[name] = f
	fontBytesRegistry[name] = fontBytes
	fontIndexRegistry[name] = 0
	fontRegistryMu.Unlock()
	return nil
}

// RegisterFontCollection registers all fonts inside a TTC/collection.
func RegisterFontCollection(family string, collectionBytes []byte) error {
	coll, err := sfnt.ParseCollection(collectionBytes)
	if err != nil {
		return err
	}
	fontRegistryMu.Lock()
	defer fontRegistryMu.Unlock()
	for i := 0; i < coll.NumFonts(); i++ {
		f, err := coll.Font(i)
		if err == nil {
			name := strings.ToLower(family)
			nameIdx := name + "-" + strconv.Itoa(i)
			fontRegistry[name] = f
			fontBytesRegistry[name] = collectionBytes
			fontIndexRegistry[name] = i

			fontRegistry[nameIdx] = f
			fontBytesRegistry[nameIdx] = collectionBytes
			fontIndexRegistry[nameIdx] = i
		}
	}
	return nil
}

func resolveFallbackGlyph(r rune) (*sfnt.Font, sfnt.GlyphIndex) {
	fontRegistryMu.RLock()
	defer fontRegistryMu.RUnlock()

	var fontBuf sfnt.Buffer
	for _, name := range []string{"emoji", "emoji-0", "emoji-1", "symbols", "default"} {
		if f, ok := fontRegistry[name]; ok {
			if idx, err := f.GlyphIndex(&fontBuf, r); err == nil && idx != 0 {
				return f, idx
			}
		}
	}
	return nil, 0
}

func isAppleColorEmoji(f *sfnt.Font, buf *sfnt.Buffer) bool {
	name, err := f.Name(buf, sfnt.NameID(1))
	return err == nil && strings.Contains(name, "Apple Color Emoji")
}

func isBold(weight string) bool {
	weight = strings.ToLower(strings.TrimSpace(weight))
	if weight == "bold" || weight == "bolder" {
		return true
	}
	if val, err := strconv.Atoi(weight); err == nil && val >= 600 {
		return true
	}
	return false
}

func lookupFont(family, weight string) *sfnt.Font {
	fontRegistryMu.RLock()
	defer fontRegistryMu.RUnlock()

	// Parse fallback list, e.g. "'Arial Black', 'Impact', sans-serif"
	families := strings.Split(family, ",")
	for _, fam := range families {
		fam = strings.TrimSpace(fam)
		fam = strings.Trim(fam, `'"`) // Remove quotes
		fam = strings.ToLower(fam)
		if fam == "" {
			continue
		}

		if isBold(weight) {
			if f, ok := fontRegistry[fam+"-bold"]; ok {
				return f
			}
		}
		if f, ok := fontRegistry[fam]; ok {
			return f
		}
	}

	if isBold(weight) {
		if f, ok := fontRegistry["default-bold"]; ok {
			return f
		}
	}
	return fontRegistry["default"]
}

func init() {
	f, err := sfnt.Parse(goregular.TTF)
	if err == nil {
		fontRegistryMu.Lock()
		fontRegistry["sans-serif"] = f
		fontRegistry["default"] = f
		fontRegistry["serif"] = f
		fontRegistry["monospace"] = f
		fontRegistryMu.Unlock()
	}
	fb, errb := sfnt.Parse(gobold.TTF)
	if errb == nil {
		fontRegistryMu.Lock()
		fontRegistry["sans-serif-bold"] = fb
		fontRegistry["default-bold"] = fb
		fontRegistry["serif-bold"] = fb
		fontRegistry["monospace-bold"] = fb
		fontRegistryMu.Unlock()
	}

	// Try loading some common system fonts
	for _, info := range []struct {
		name string
		path string
	}{
		// macOS
		{"arial", "/System/Library/Fonts/Supplemental/Arial.ttf"},
		{"arial-bold", "/System/Library/Fonts/Supplemental/Arial Bold.ttf"},
		{"arial black", "/System/Library/Fonts/Supplemental/Arial Black.ttf"},
		{"arial black-bold", "/System/Library/Fonts/Supplemental/Arial Black.ttf"},
		{"impact", "/System/Library/Fonts/Supplemental/Impact.ttf"},
		{"impact-bold", "/System/Library/Fonts/Supplemental/Impact.ttf"},

		// Windows
		{"arial", "C:\\Windows\\Fonts\\arial.ttf"},
		{"arial-bold", "C:\\Windows\\Fonts\\arialbd.ttf"},
		{"arial black", "C:\\Windows\\Fonts\\ariblk.ttf"},
		{"arial black-bold", "C:\\Windows\\Fonts\\ariblk.ttf"},
		{"impact", "C:\\Windows\\Fonts\\impact.ttf"},
		{"impact-bold", "C:\\Windows\\Fonts\\impact.ttf"},

		// Linux (msttcorefonts)
		{"arial", "/usr/share/fonts/truetype/msttcorefonts/Arial.ttf"},
		{"arial-bold", "/usr/share/fonts/truetype/msttcorefonts/Arial_Bold.ttf"},
		{"arial black", "/usr/share/fonts/truetype/msttcorefonts/Arial_Black.ttf"},
		{"arial black-bold", "/usr/share/fonts/truetype/msttcorefonts/Arial_Black.ttf"},
		{"impact", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf"},
		{"impact-bold", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf"},
	} {
		if data, err := os.ReadFile(info.path); err == nil {
			_ = RegisterFont(info.name, data)
		}
	}

	// Try loading some emoji/symbol fonts
	for _, info := range []struct {
		name string
		path string
		coll bool
	}{
		{"emoji", "/System/Library/Fonts/Apple Color Emoji.ttc", true},
		{"symbols", "/System/Library/Fonts/Apple Symbols.ttf", false},
		{"emoji", "C:\\Windows\\Fonts\\seguiemj.ttf", false},
		{"emoji", "/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf", false},
	} {
		if data, err := os.ReadFile(info.path); err == nil {
			if info.coll {
				_ = RegisterFontCollection(info.name, data)
			} else {
				_ = RegisterFont(info.name, data)
			}
		}
	}
}

// IconCursor is used while parsing SVG files.
type IconCursor struct {
	PathCursor
	icon                                                 *SvgIcon
	StyleStack                                           []PathStyle
	grad                                                 *rasterx.Gradient
	inTitleText, inDescText, inGrad, inDefs, inDefsStyle bool
	currentDef                                           []definition
	inText                                               bool
	textFragments                                        []textFragment
	textX, textY, textDx, textDy                         float64
	hasTextX, hasTextY                                   bool
}

// ReadGradURL reads an SVG format gradient url
// Since the context of the gradient can affect the colors
// the current fill or line color is passed in and used in
// the case of a nil stopClor value
func (c *IconCursor) ReadGradURL(v string, defaultColor interface{}) (grad rasterx.Gradient, ok bool) {
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		urlStr := strings.TrimSpace(v[4 : len(v)-1])
		if strings.HasPrefix(urlStr, "#") {
			var g *rasterx.Gradient
			g, ok = c.icon.Grads[urlStr[1:]]
			if ok {
				grad = localizeGradIfStopClrNil(g, defaultColor)
			}
		}
	}
	return
}

// ReadGradAttr reads an SVG gradient attribute
func (c *IconCursor) ReadGradAttr(attr xml.Attr) (err error) {
	switch attr.Name.Local {
	case "gradientTransform":
		c.grad.Matrix, err = c.parseTransform(attr.Value)
	case "gradientUnits":
		switch strings.TrimSpace(attr.Value) {
		case "userSpaceOnUse":
			c.grad.Units = rasterx.UserSpaceOnUse
		case "objectBoundingBox":
			c.grad.Units = rasterx.ObjectBoundingBox
		}
	case "spreadMethod":
		switch strings.TrimSpace(attr.Value) {
		case "pad":
			c.grad.Spread = rasterx.PadSpread
		case "reflect":
			c.grad.Spread = rasterx.ReflectSpread
		case "repeat":
			c.grad.Spread = rasterx.RepeatSpread
		}
	}
	return
}

// PushStyle parses the style element, and push it on the style stack. Only color and opacity are supported
// for fill. Note that this parses both the contents of a style attribute plus
// direct fill and opacity attributes.
func (c *IconCursor) PushStyle(attrs []xml.Attr) error {
	var pairs []string
	className := ""
	for _, attr := range attrs {
		switch strings.ToLower(attr.Name.Local) {
		case "style":
			pairs = append(pairs, strings.Split(attr.Value, ";")...)
		case "class":
			className = attr.Value
		default:
			pairs = append(pairs, attr.Name.Local+":"+attr.Value)
		}
	}
	// Make a copy of the top style
	curStyle := c.StyleStack[len(c.StyleStack)-1]
	for _, pair := range pairs {
		kv := strings.Split(pair, ":")
		if len(kv) >= 2 {
			k := strings.ToLower(kv[0])
			k = strings.TrimSpace(k)
			v := strings.TrimSpace(kv[1])
			err := c.readStyleAttr(&curStyle, k, v)
			if err != nil {
				return err
			}
		}
	}
	c.adaptClasses(&curStyle, className)
	c.StyleStack = append(c.StyleStack, curStyle) // Push style onto stack
	return nil
}

func (c *IconCursor) readTransformAttr(m1 rasterx.Matrix2D, k string) (rasterx.Matrix2D, error) {
	ln := len(c.points)
	switch k {
	case "rotate":
		if ln == 1 {
			m1 = m1.Rotate(c.points[0] * math.Pi / 180)
		} else if ln == 3 {
			m1 = m1.Translate(c.points[1], c.points[2]).
				Rotate(c.points[0]*math.Pi/180).
				Translate(-c.points[1], -c.points[2])
		} else {
			return m1, errParamMismatch
		}
	case "translate":
		if ln == 1 {
			m1 = m1.Translate(c.points[0], 0)
		} else if ln == 2 {
			m1 = m1.Translate(c.points[0], c.points[1])
		} else {
			return m1, errParamMismatch
		}
	case "skewx":
		if ln == 1 {
			m1 = m1.SkewX(c.points[0] * math.Pi / 180)
		} else {
			return m1, errParamMismatch
		}
	case "skewy":
		if ln == 1 {
			m1 = m1.SkewY(c.points[0] * math.Pi / 180)
		} else {
			return m1, errParamMismatch
		}
	case "scale":
		if ln == 1 {
			m1 = m1.Scale(c.points[0], c.points[0])
		} else if ln == 2 {
			m1 = m1.Scale(c.points[0], c.points[1])
		} else {
			return m1, errParamMismatch
		}
	case "matrix":
		if ln == 6 {
			m1 = m1.Mult(rasterx.Matrix2D{
				A: c.points[0],
				B: c.points[1],
				C: c.points[2],
				D: c.points[3],
				E: c.points[4],
				F: c.points[5]})
		} else {
			return m1, errParamMismatch
		}
	default:
		return m1, errParamMismatch
	}
	return m1, nil
}

func (c *IconCursor) parseTransform(v string) (rasterx.Matrix2D, error) {
	return c.parseTransformFrom(v, c.StyleStack[len(c.StyleStack)-1].mAdder.M)
}

// parseTransformFrom parses an SVG transform list, composing each primitive
// onto the supplied base matrix. parseTransform seeds from the current style
// stack; callers that need an isolated transform (e.g. patternTransform, where
// the result must not include the referencing element's transform) pass
// rasterx.Identity explicitly.
func (c *IconCursor) parseTransformFrom(v string, m1 rasterx.Matrix2D) (rasterx.Matrix2D, error) {
	ts := strings.Split(v, ")")
	for _, t := range ts {
		t = strings.TrimSpace(t)
		if len(t) == 0 {
			continue
		}
		d := strings.Split(t, "(")
		if len(d) != 2 || len(d[1]) < 1 {
			return m1, errParamMismatch // badly formed transformation
		}
		err := c.GetPoints(d[1])
		if err != nil {
			return m1, err
		}
		m1, err = c.readTransformAttr(m1, strings.ToLower(strings.TrimSpace(d[0])))
		if err != nil {
			return m1, err
		}
	}
	return m1, nil
}

func (c *IconCursor) readStyleAttr(curStyle *PathStyle, k, v string) error {
	switch k {
	case "fill":
		gradient, ok := c.ReadGradURL(v, curStyle.fillerColor)
		if ok {
			curStyle.fillerColor = gradient
			break
		}
		pattern, ok := c.ReadPatternURL(v)
		if ok {
			curStyle.fillerColor = pattern
			break
		}
		var err error
		curStyle.fillerColor, err = ParseSVGColor(v)
		return err
	case "stroke":
		gradient, ok := c.ReadGradURL(v, curStyle.linerColor)
		if ok {
			curStyle.linerColor = gradient
			break
		}
		pattern, ok := c.ReadPatternURL(v)
		if ok {
			curStyle.linerColor = pattern
			break
		}
		col, errc := ParseSVGColor(v)
		if errc != nil {
			return errc
		}
		if col != nil {
			curStyle.linerColor = col.(color.NRGBA)
		} else {
			curStyle.linerColor = nil
		}
	case "stroke-linegap":
		switch v {
		case "flat":
			curStyle.LineGap = rasterx.FlatGap
		case "round":
			curStyle.LineGap = rasterx.RoundGap
		case "cubic":
			curStyle.LineGap = rasterx.CubicGap
		case "quadratic":
			curStyle.LineGap = rasterx.QuadraticGap
		}
	case "stroke-leadlinecap":
		switch v {
		case "butt":
			curStyle.LeadLineCap = rasterx.ButtCap
		case "round":
			curStyle.LeadLineCap = rasterx.RoundCap
		case "square":
			curStyle.LeadLineCap = rasterx.SquareCap
		case "cubic":
			curStyle.LeadLineCap = rasterx.CubicCap
		case "quadratic":
			curStyle.LeadLineCap = rasterx.QuadraticCap
		}
	case "stroke-linecap":
		switch v {
		case "butt":
			curStyle.LineCap = rasterx.ButtCap
		case "round":
			curStyle.LineCap = rasterx.RoundCap
		case "square":
			curStyle.LineCap = rasterx.SquareCap
		case "cubic":
			curStyle.LineCap = rasterx.CubicCap
		case "quadratic":
			curStyle.LineCap = rasterx.QuadraticCap
		}
	case "stroke-linejoin":
		switch v {
		case "miter":
			curStyle.LineJoin = rasterx.Miter
		case "miter-clip":
			curStyle.LineJoin = rasterx.MiterClip
		case "arc-clip":
			curStyle.LineJoin = rasterx.ArcClip
		case "round":
			curStyle.LineJoin = rasterx.Round
		case "arc":
			curStyle.LineJoin = rasterx.Arc
		case "bevel":
			curStyle.LineJoin = rasterx.Bevel
		}
	case "stroke-miterlimit":
		mLimit, err := parseFloat(v, 64)
		if err != nil {
			return err
		}
		curStyle.MiterLimit = mLimit
	case "stroke-width":
		width, err := parseFloat(v, 64)
		if err != nil {
			return err
		}
		curStyle.LineWidth = width
	case "stroke-dashoffset":
		dashOffset, err := parseFloat(v, 64)
		if err != nil {
			return err
		}
		curStyle.DashOffset = dashOffset
	case "stroke-dasharray":
		if v != "none" {
			dashes := splitOnCommaOrSpace(v)
			dList := make([]float64, len(dashes))
			for i, dstr := range dashes {
				d, err := parseFloat(strings.TrimSpace(dstr), 64)
				if err != nil {
					return err
				}
				dList[i] = d
			}
			curStyle.Dash = dList
			break
		}
	case "opacity", "stroke-opacity", "fill-opacity":
		op, err := parseFloat(v, 64)
		if err != nil {
			return err
		}
		if k != "stroke-opacity" {
			curStyle.FillOpacity *= op
		}
		if k != "fill-opacity" {
			curStyle.LineOpacity *= op
		}
	case "transform":
		m, err := c.parseTransform(v)
		if err != nil {
			return err
		}
		curStyle.mAdder.M = m
	case "font-family":
		curStyle.FontFamily = v
	case "font-size":
		v = strings.TrimSuffix(v, "px")
		v = strings.TrimSuffix(v, "pt")
		val, err := parseFloat(v, 64)
		if err != nil {
			return err
		}
		if val > maxFontSize {
			val = maxFontSize
		}
		curStyle.FontSize = val
	case "text-anchor":
		curStyle.TextAnchor = v
	case "font-weight":
		curStyle.FontWeight = v
	}
	return nil
}

func (c *IconCursor) readStartElement(se xml.StartElement) (err error) {
	var skipDef bool
	if se.Name.Local == "radialGradient" || se.Name.Local == "linearGradient" || c.inGrad {
		skipDef = true
	}
	if c.inDefs && !skipDef {
		ID := ""
		for _, attr := range se.Attr {
			if attr.Name.Local == "id" {
				ID = attr.Value
			}
		}
		if ID != "" && len(c.currentDef) > 0 {
			c.icon.Defs[c.currentDef[0].ID] = c.currentDef
			c.currentDef = make([]definition, 0)
		}
		c.currentDef = append(c.currentDef, definition{
			ID:    ID,
			Tag:   se.Name.Local,
			Attrs: se.Attr,
		})
		return nil
	}
	df, ok := drawFuncs[se.Name.Local]
	if !ok {
		errStr := "Cannot process svg element " + se.Name.Local
		if c.returnError(errStr) {
			return errors.New(errStr)
		}
		return nil
	}
	err = df(c, se.Attr)
	if err != nil {
		e := fmt.Sprintf("error during processing svg element %s: %s", se.Name.Local, err.Error())
		if c.returnError(e) {
			err = errors.New(e)
		}
		err = nil
	}

	if len(c.Path) > 0 {
		//The cursor parsed a path from the xml element
		pathCopy := make(rasterx.Path, len(c.Path))
		copy(pathCopy, c.Path)
		c.icon.SVGPaths = append(c.icon.SVGPaths,
			SvgPath{c.StyleStack[len(c.StyleStack)-1], pathCopy})
		c.Path = c.Path[:0]
	}
	return
}

func (c *IconCursor) adaptClasses(pathStyle *PathStyle, className string) {
	if className == "" || len(c.icon.classes) == 0 {
		return
	}
	for k, v := range c.icon.classes[className] {
		c.readStyleAttr(pathStyle, k, v)
	}
}

func (c *IconCursor) returnError(errMsg string) bool {
	if c.ErrorMode == StrictErrorMode {
		return true
	}
	if c.ErrorMode == WarnErrorMode {
		log.Println(errMsg)
	}

	return false
}

// compileDefs compiles a slice of definition elements into SvgPath structs using a temporary dummy icon.
func (c *IconCursor) compileDefs(defs []definition) ([]SvgPath, error) {
	origIcon := c.icon
	dummyIcon := &SvgIcon{
		Defs:      origIcon.Defs,
		Grads:     origIcon.Grads,
		Patterns:  origIcon.Patterns,
		ViewBox:   origIcon.ViewBox,
		Transform: rasterx.Identity,
	}
	c.icon = dummyIcon

	origStyleStack := c.StyleStack
	c.StyleStack = []PathStyle{DefaultStyle}

	for _, def := range defs {
		if def.Tag == "pattern" || def.Tag == "endpattern" {
			continue
		}
		if def.Tag == "endg" {
			if len(c.StyleStack) > 1 {
				c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			}
			continue
		}

		if err := c.PushStyle(def.Attrs); err != nil {
			c.icon = origIcon
			c.StyleStack = origStyleStack
			return nil, err
		}

		df, ok := drawFuncs[def.Tag]
		if ok {
			if err := df(c, def.Attrs); err != nil {
				c.icon = origIcon
				c.StyleStack = origStyleStack
				return nil, err
			}
		}

		if len(c.Path) > 0 {
			pathCopy := make(rasterx.Path, len(c.Path))
			copy(pathCopy, c.Path)
			c.icon.SVGPaths = append(c.icon.SVGPaths, SvgPath{c.StyleStack[len(c.StyleStack)-1], pathCopy})
			c.Path = c.Path[:0]
		}

		if def.Tag != "g" {
			if len(c.StyleStack) > 1 {
				c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			}
		}
	}

	paths := c.icon.SVGPaths
	c.icon = origIcon
	c.StyleStack = origStyleStack
	return paths, nil
}

// compilePattern parses a pattern definition slice into a Pattern struct. The
// (partially populated) Pattern is registered in c.icon.Patterns before its
// children are compiled, so cyclic references (P1 -> P1 or P1 -> P2 -> P1)
// resolve to the in-progress pointer instead of recursing indefinitely.
func (c *IconCursor) compilePattern(defs []definition) (*Pattern, error) {
	p := &Pattern{
		Units:        "userSpaceOnUse",
		ContentUnits: "userSpaceOnUse",
		Transform:    rasterx.Identity,
	}

	first := defs[0]
	p.ID = first.ID

	var err error
	for _, attr := range first.Attrs {
		switch attr.Name.Local {
		case "x":
			p.X, err = parseFloat(attr.Value, 64)
		case "y":
			p.Y, err = parseFloat(attr.Value, 64)
		case "width":
			p.Width, err = parseFloat(attr.Value, 64)
		case "height":
			p.Height, err = parseFloat(attr.Value, 64)
		case "patternUnits":
			p.Units = strings.TrimSpace(attr.Value)
		case "patternContentUnits":
			p.ContentUnits = strings.TrimSpace(attr.Value)
		case "patternTransform":
			// patternTransform must be parsed in isolation; using the current
			// style stack as the base would fold the referencing element's
			// transform into p.Transform and then double-apply it in
			// GetColorFunction.
			p.Transform, err = c.parseTransformFrom(attr.Value, rasterx.Identity)
		}
		if err != nil {
			return nil, err
		}
	}

	if p.ID != "" {
		c.icon.Patterns[p.ID] = p
	}
	p.Paths, err = c.compileDefs(defs)
	if err != nil {
		if p.ID != "" {
			delete(c.icon.Patterns, p.ID)
		}
		return nil, err
	}

	return p, nil
}

// ReadPatternURL parses an SVG pattern url reference.
func (c *IconCursor) ReadPatternURL(v string) (pattern *Pattern, ok bool) {
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		urlStr := strings.TrimSpace(v[4 : len(v)-1])
		if strings.HasPrefix(urlStr, "#") {
			id := urlStr[1:]
			pattern, ok = c.icon.Patterns[id]
			if ok {
				return
			}
			// If not in Patterns but in Defs, compile it. compilePattern
			// registers the pattern in c.icon.Patterns before recursing into
			// its children, so cyclic references resolve safely.
			defs, hasDefs := c.icon.Defs[id]
			if hasDefs && len(defs) > 0 && defs[0].Tag == "pattern" {
				p, err := c.compilePattern(defs)
				if err == nil {
					return p, true
				}
			}
		}
	}
	return nil, false
}

// fragmentPPEM returns a clamped, font-size-derived ppem for a fragment. The
// upper clamp keeps fixed.Int26_6 (int32) arithmetic inside sfnt safe from
// overflow on attacker-controlled font-size values.
func fragmentPPEM(fontSize float64) fixed.Int26_6 {
	if fontSize <= 0 {
		fontSize = 12.0
	}
	if fontSize > maxFontSize {
		fontSize = maxFontSize
	}
	return fixed.Int26_6(fontSize * 64)
}

// compileText lays out text fragments, computes text-anchor alignments,
// extracts sfnt glyph vector outlines, and appends compound paths.
func (c *IconCursor) compileText() error {
	if len(c.textFragments) == 0 {
		return nil
	}

	var penX, penY float64
	var fontBuf sfnt.Buffer
	var prevIdx sfnt.GlyphIndex
	var prevFont *sfnt.Font

	// 1. Calculate total width of the text layout
	var totalWidth float64
	startX := c.textFragments[0].x + c.textFragments[0].dx
	penX = startX

	for _, frag := range c.textFragments {
		fontObj := lookupFont(frag.style.FontFamily, frag.style.FontWeight)
		if fontObj == nil {
			continue
		}

		if frag.hasX {
			penX = frag.x
		}
		if frag.hasY {
			penY = frag.y
		}
		penX += frag.dx
		penY += frag.dy

		ppem := fragmentPPEM(frag.style.FontSize)

		if fontObj != prevFont {
			prevIdx = 0
			prevFont = fontObj
		}

		runes := []rune(frag.text)
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			fontObjToUse := fontObj
			var idx sfnt.GlyphIndex
			var err error

			isFlag := false
			if i+1 < len(runes) && r >= 0x1F1E6 && r <= 0x1F1FF && runes[i+1] >= 0x1F1E6 && runes[i+1] <= 0x1F1FF {
				code := string(rune(r-0x1F1E6+'A')) + string(rune(runes[i+1]-0x1F1E6+'A'))
				fontRegistryMu.RLock()
				emojiFont := fontRegistry["emoji"]
				fontRegistryMu.RUnlock()
				if emojiFont != nil && isAppleColorEmoji(emojiFont, &fontBuf) {
					if gIdx, ok := emojiFlagGlyphs[code]; ok {
						fontObjToUse = emojiFont
						idx = sfnt.GlyphIndex(gIdx)
						isFlag = true
						i++
					}
				}
			}

			if !isFlag {
				idx, err = fontObjToUse.GlyphIndex(&fontBuf, r)
				if err != nil || idx == 0 {
					if f, fallbackIdx := resolveFallbackGlyph(r); f != nil {
						fontObjToUse = f
						idx = fallbackIdx
					}
				}
			}

			if fontObjToUse != prevFont {
				prevIdx = 0
				prevFont = fontObjToUse
			}

			// Kerning
			if prevIdx != 0 && idx != 0 {
				kern, err := fontObjToUse.Kern(&fontBuf, prevIdx, idx, ppem, font.HintingNone)
				if err == nil {
					penX += float64(kern) / 64
				}
			}

			if idx != 0 {
				adv, err := fontObjToUse.GlyphAdvance(&fontBuf, idx, ppem, font.HintingNone)
				if err == nil {
					penX += float64(adv) / 64
				}
				prevIdx = idx
			}
		}
	}
	totalWidth = penX - startX

	anchor := c.textFragments[0].style.TextAnchor
	var shiftX float64
	if anchor == "middle" {
		shiftX = -totalWidth / 2
	} else if anchor == "end" {
		shiftX = -totalWidth
	}

	// 2. Perform layout and build glyph paths
	penX = startX + shiftX
	penY = c.textFragments[0].y + c.textFragments[0].dy

	prevIdx = 0
	prevFont = nil

	for _, frag := range c.textFragments {
		fontObj := lookupFont(frag.style.FontFamily, frag.style.FontWeight)
		if fontObj == nil {
			continue
		}

		if frag.hasX {
			penX = frag.x + shiftX
		}
		if frag.hasY {
			penY = frag.y
		}
		penX += frag.dx
		penY += frag.dy

		ppem := fragmentPPEM(frag.style.FontSize)

		if fontObj != prevFont {
			prevIdx = 0
			prevFont = fontObj
		}

		var fragPath rasterx.Path

		runes := []rune(frag.text)
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			fontObjToUse := fontObj
			var idx sfnt.GlyphIndex
			var err error

			isFlag := false
			if i+1 < len(runes) && r >= 0x1F1E6 && r <= 0x1F1FF && runes[i+1] >= 0x1F1E6 && runes[i+1] <= 0x1F1FF {
				code := string(rune(r-0x1F1E6+'A')) + string(rune(runes[i+1]-0x1F1E6+'A'))
				fontRegistryMu.RLock()
				emojiFont := fontRegistry["emoji"]
				fontRegistryMu.RUnlock()
				if emojiFont != nil && isAppleColorEmoji(emojiFont, &fontBuf) {
					if gIdx, ok := emojiFlagGlyphs[code]; ok {
						fontObjToUse = emojiFont
						idx = sfnt.GlyphIndex(gIdx)
						isFlag = true
						i++
					}
				}
			}

			if !isFlag {
				idx, err = fontObjToUse.GlyphIndex(&fontBuf, r)
				if err != nil || idx == 0 {
					if f, fallbackIdx := resolveFallbackGlyph(r); f != nil {
						fontObjToUse = f
						idx = fallbackIdx
					}
				}
			}

			if fontObjToUse != prevFont {
				prevIdx = 0
				prevFont = fontObjToUse
			}

			// Kerning
			if prevIdx != 0 && idx != 0 {
				kern, err := fontObjToUse.Kern(&fontBuf, prevIdx, idx, ppem, font.HintingNone)
				if err == nil {
					penX += float64(kern) / 64
				}
			}

			if idx != 0 {
				// Try drawing as bitmap/PNG first
				bytesData, index := findFontBytesAndIndex(fontObjToUse)
				var pngBytes []byte
				var originX, originY, maxPPEM int
				var parseErr error
				if bytesData != nil {
					pngBytes, originX, originY, maxPPEM, parseErr = parseSBIX(bytesData, index, int(idx))
				}
				if pngBytes != nil && parseErr == nil {
					if img, decodeErr := png.Decode(bytes.NewReader(pngBytes)); decodeErr == nil {
						scale := frag.style.FontSize / float64(maxPPEM)
						tx := penX + float64(originX)*scale
						ty := penY - (float64(originY)+float64(img.Bounds().Dy()))*scale
						svgi := SvgImage{
							Image:     img,
							Transform: frag.style.mAdder.M.Translate(tx, ty).Scale(scale, scale),
							Opacity:   frag.style.FillOpacity,
						}
						c.icon.SVGImages = append(c.icon.SVGImages, svgi)

						// Advance penX
						adv, err := fontObjToUse.GlyphAdvance(&fontBuf, idx, ppem, font.HintingNone)
						if err == nil {
							penX += float64(adv) / 64
						}
						prevIdx = idx
						continue // Skip vector path drawing!
					}
				}

				segs, err := fontObjToUse.LoadGlyph(&fontBuf, idx, ppem, nil)
				if err == nil {
					fixedPenX := fixed.Int26_6(penX * 64)
					fixedPenY := fixed.Int26_6(penY * 64)
					// Each MoveTo starts a new contour; close the previous one
					// so multi-contour glyphs (e.g. 'o', 'B') render correctly.
					contourOpen := false
					for _, seg := range segs {
						args := seg.Args
						for i := range args {
							args[i].X += fixedPenX
							args[i].Y += fixedPenY
						}
						switch seg.Op {
						case sfnt.SegmentOpMoveTo:
							if contourOpen {
								fragPath.Stop(true)
							}
							fragPath.Start(args[0])
							contourOpen = true
						case sfnt.SegmentOpLineTo:
							fragPath.Line(args[0])
						case sfnt.SegmentOpQuadTo:
							fragPath.QuadBezier(args[0], args[1])
						case sfnt.SegmentOpCubeTo:
							fragPath.CubeBezier(args[0], args[1], args[2])
						}
					}
					if contourOpen {
						fragPath.Stop(true)
					}
				}

				adv, err := fontObjToUse.GlyphAdvance(&fontBuf, idx, ppem, font.HintingNone)
				if err == nil {
					penX += float64(adv) / 64
				}
				prevIdx = idx
			}
		}

		if len(fragPath) > 0 {
			c.icon.SVGPaths = append(c.icon.SVGPaths, SvgPath{
				PathStyle: frag.style,
				Path:      fragPath,
			})
		}
	}

	c.textFragments = nil
	return nil
}
