// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// icon_cursor.go implements the SVG parsing cursor: element and style handling,
// the depth-aware <defs> collection model, color/gradient resolution, and text
// layout.

package oksvg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
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

const (
	regionalIndicatorBase = 0x1F1E6 // '🇦' (Regional Indicator Symbol Letter A)
	regionalIndicatorEnd  = 0x1F1FF // '🇿' (Regional Indicator Symbol Letter Z)
)

// textFragment holds a piece of text with its associated style and coordinates.
type textFragment struct {
	text       string
	style      PathStyle
	x, y       float64
	dx, dy     float64
	hasX, hasY bool
}

// systemFontInfo names a single-face system font file to try to load. The
// platform files (icon_cursor_{darwin,linux,windows,other}.go) declare the
// per-GOOS systemFonts table using this type.
type systemFontInfo struct{ name, path string }

// emojiFontInfo names an emoji/symbol font file. coll marks a TrueType
// collection (.ttc), which is registered with RegisterFontCollection instead of
// RegisterFont. The platform files declare the per-GOOS emojiFonts table.
type emojiFontInfo struct {
	name, path string
	coll       bool
}

// fontRegistry maps font-family names to parsed sfnt.Fonts. Protected by
// fontRegistryMu so RegisterFont and SVG parsing can run concurrently.
var (
	fontRegistryMu    sync.RWMutex
	fontRegistry      = map[string]*sfnt.Font{}
	fontBytesRegistry = map[string][]byte{}
	fontIndexRegistry = map[string]int{}
)

// systemFontsOnce guards loadSystemFonts so the (potentially large, e.g. the
// ~180 MB Apple Color Emoji TTC on macOS) system/emoji font files are read from
// disk at most once, and only when text is first laid out rather than at import.
var systemFontsOnce sync.Once

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

// registerFontIfAbsent parses fontBytes and registers it under family only if no
// font is already registered under that (lower-cased) name. It lets the lazy
// system-font loader fill in defaults without clobbering a font the caller
// registered explicitly via RegisterFont. The presence check and the insert
// happen under the same write lock to avoid a TOCTOU race with a concurrent
// RegisterFont.
func registerFontIfAbsent(family string, fontBytes []byte) error {
	f, err := sfnt.Parse(fontBytes)
	if err != nil {
		return err
	}
	fontRegistryMu.Lock()
	defer fontRegistryMu.Unlock()
	name := strings.ToLower(family)
	if _, exists := fontRegistry[name]; exists {
		return nil
	}
	fontRegistry[name] = f
	fontBytesRegistry[name] = fontBytes
	fontIndexRegistry[name] = 0
	return nil
}

// registerFontCollectionIfAbsent registers the faces of a TTC/collection without
// overwriting existing registrations: the bare family name binds to face 0 only
// when it is not already present, and each indexed "name-i" key is filled only
// when absent. Used by the lazy system-font loader so it never clobbers caller
// registrations. The whole update runs under the write lock (TOCTOU safety).
func registerFontCollectionIfAbsent(family string, collectionBytes []byte) error {
	coll, err := sfnt.ParseCollection(collectionBytes)
	if err != nil {
		return err
	}
	fontRegistryMu.Lock()
	defer fontRegistryMu.Unlock()
	name := strings.ToLower(family)
	for i := 0; i < coll.NumFonts(); i++ {
		f, err := coll.Font(i)
		if err != nil {
			continue
		}
		if i == 0 {
			if _, exists := fontRegistry[name]; !exists {
				fontRegistry[name] = f
				fontBytesRegistry[name] = collectionBytes
				fontIndexRegistry[name] = i
			}
		}
		nameIdx := name + "-" + strconv.Itoa(i)
		if _, exists := fontRegistry[nameIdx]; !exists {
			fontRegistry[nameIdx] = f
			fontBytesRegistry[nameIdx] = collectionBytes
			fontIndexRegistry[nameIdx] = i
		}
	}
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
	name := strings.ToLower(family)
	for i := 0; i < coll.NumFonts(); i++ {
		f, err := coll.Font(i)
		if err != nil {
			continue
		}
		// The bare family name binds to the first face only. Previously every
		// face overwrote it, so the last member of the collection silently won;
		// the indexed "name-i" keys still expose the other faces.
		if i == 0 {
			fontRegistry[name] = f
			fontBytesRegistry[name] = collectionBytes
			fontIndexRegistry[name] = i
		}
		nameIdx := name + "-" + strconv.Itoa(i)
		fontRegistry[nameIdx] = f
		fontBytesRegistry[nameIdx] = collectionBytes
		fontIndexRegistry[nameIdx] = i
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
	// NOTE: system/emoji fonts are intentionally NOT loaded here. On macOS the
	// Apple Color Emoji TTC alone is ~180 MB of heap, which every importer of
	// this package would pay at init even when no text is ever rendered. They
	// are loaded lazily by LoadSystemFonts on the first <text> layout instead.
}

// loadSystemFonts reads the per-GOOS system and emoji/symbol font files from
// disk and registers any that are present. Missing files are ignored. It is
// invoked exactly once, via LoadSystemFonts.
func loadSystemFonts() {
	// Try loading some common system fonts. Register non-destructively so a font
	// the caller registered explicitly (via RegisterFont) is never clobbered.
	for _, info := range systemFonts {
		if data, err := os.ReadFile(info.path); err == nil {
			_ = registerFontIfAbsent(info.name, data)
		}
	}

	// Try loading some emoji/symbol fonts (likewise non-destructive).
	for _, info := range emojiFonts {
		if data, err := os.ReadFile(info.path); err == nil {
			if info.coll {
				_ = registerFontCollectionIfAbsent(info.name, data)
			} else {
				_ = registerFontIfAbsent(info.name, data)
			}
		}
	}
}

// LoadSystemFonts loads the platform's system and emoji/symbol fonts into the
// font registry. It is safe to call concurrently and does the work at most once
// (subsequent calls are no-ops). oksvg calls it automatically the first time a
// <text> element is laid out, so the (large) emoji fonts are read from disk only
// when text is actually rendered rather than at package import. Callers that
// register fonts before rendering, or that want the load cost paid up front, may
// call it explicitly.
func LoadSystemFonts() { systemFontsOnce.Do(loadSystemFonts) }

// gradHrefLink records a gradient that inherits its stops from another gradient
// via xlink:href/href. It is resolved after the whole document is parsed so
// forward references work regardless of element order.
type gradHrefLink struct {
	grad   *rasterx.Gradient
	target string
}

// IconCursor is used while parsing SVG files.
type IconCursor struct {
	PathCursor
	icon                                                 *SvgIcon
	StyleStack                                           []PathStyle
	grad                                                 *rasterx.Gradient
	inTitleText, inDescText, inGrad, inDefs, inDefsStyle bool
	inText                                               bool
	textFragments                                        []textFragment
	textX, textY, textDx, textDy                         float64
	hasTextX, hasTextY                                   bool
	// defDepth is the current nesting depth within <defs>; openDefs holds the
	// collectors for every ID'd element currently open.
	defDepth int
	openDefs []*defCollector
	// defsNesting counts open <defs> elements. defs mode stays on until the
	// outermost </defs>, so a nested </defs> can't prematurely flush the outer
	// collection (which would render the outer defs' remaining children and drop
	// their ids).
	defsNesting int
	// classInfo accumulates the raw text of <style> elements for CSS parsing.
	classInfo string
	// pendingGradHrefs records gradient stop-inheritance links to resolve at EOF.
	pendingGradHrefs []gradHrefLink
	// skipShapeDepth tracks a subtree being skipped (e.g. a <pattern> that
	// appears outside <defs>, whose tile children must not be drawn as shapes).
	skipShapeDepth int
	// useActive is the set of href ids currently being replayed by useF, and
	// useDepth is the current <use> replay nesting depth. Together they detect a
	// cyclic <use> chain (self-reference #a->#a or mutual #a->#b->#a) and cap
	// pathological nesting, so a malicious/broken document is routed through the
	// error-mode policy instead of recursing until the goroutine stack overflows.
	useActive map[string]bool
	useDepth  int
	// useReplayed counts definition elements replayed through <use> over the
	// whole document (see maxUseReplay); useBudgetSpent records that the
	// budget was hit so warn mode logs it once rather than per frame.
	useReplayed    int
	useBudgetSpent bool
	// bitmapCache shares one decoded sbix image per (font, glyph) across the
	// document; bitmapPixels is the running total of pixels decoded for
	// distinct glyphs, charged against bitmapPixelLimit (0 means
	// maxBitmapGlyphPixels). See bitmapGlyph.
	bitmapCache      map[bitmapGlyphKey]image.Image
	bitmapPixels     int
	bitmapPixelLimit int
}

// bitmapGlyphKey identifies a decoded sbix glyph within one document.
type bitmapGlyphKey struct {
	font *sfnt.Font
	idx  sfnt.GlyphIndex
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
		// Seed from Identity: gradientTransform is absolute and must not fold in
		// the referencing/ancestor element transform (which would double-apply).
		c.grad.Matrix, err = c.parseTransformFrom(attr.Value, rasterx.Identity)
	case "href":
		// xlink:href and href both surface as Name.Local == "href". Record the
		// stop-inheritance link; it is resolved at EOF by finalize().
		c.pendingGradHrefs = append(c.pendingGradHrefs, gradHrefLink{
			grad:   c.grad,
			target: strings.TrimPrefix(strings.TrimSpace(attr.Value), "#"),
		})
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

// PushStyle parses the style attributes of an element and pushes the resulting
// style onto the style stack. Properties are applied in CSS precedence order:
// class selectors (lowest), then presentation attributes, then the inline
// style="" attribute (highest). Per-property parse errors are handled according
// to the error mode: StrictErrorMode aborts the parse; Warn logs and skips the
// property; Ignore skips silently. The style is always pushed (even when a
// property is skipped) so the stack stays balanced with EndElement pops.
func (c *IconCursor) PushStyle(attrs []xml.Attr) error {
	var attrPairs, stylePairs, classNames []string
	for _, attr := range attrs {
		switch strings.ToLower(attr.Name.Local) {
		case "style":
			stylePairs = append(stylePairs, strings.Split(attr.Value, ";")...)
		case "class":
			classNames = append(classNames, strings.Fields(attr.Value)...)
		default:
			attrPairs = append(attrPairs, attr.Name.Local+":"+attr.Value)
		}
	}
	// Make a copy of the top style.
	curStyle := c.StyleStack[len(c.StyleStack)-1]

	// Lowest precedence: class selectors, in listed order.
	if err := c.adaptClasses(&curStyle, classNames); err != nil {
		return err
	}
	// Middle precedence: presentation attributes.
	if err := c.applyStylePairs(&curStyle, attrPairs); err != nil {
		return err
	}
	// Highest precedence: the inline style="" attribute.
	if err := c.applyStylePairs(&curStyle, stylePairs); err != nil {
		return err
	}

	c.StyleStack = append(c.StyleStack, curStyle) // Push style onto stack
	return nil
}

// applyStylePairs applies "key:value" style declarations to curStyle, routing
// per-property parse errors through the error-mode policy. It returns a non-nil
// error only in StrictErrorMode.
func (c *IconCursor) applyStylePairs(curStyle *PathStyle, pairs []string) error {
	for _, pair := range pairs {
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) < 2 {
			continue
		}
		k := strings.TrimSpace(strings.ToLower(kv[0]))
		v := strings.TrimSpace(kv[1])
		if k == "" {
			continue
		}
		if err := c.readStyleAttr(curStyle, k, v); err != nil {
			e := fmt.Sprintf("error parsing style property %q: %s", pair, err.Error())
			if c.returnError(e) {
				return errors.New(e)
			}
			// Non-strict: skip this property and keep the inherited value.
		}
	}
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
		// Transform lists may be separated by commas as well as whitespace,
		// e.g. "translate(1,1),rotate(45)". Strip any leading/trailing commas
		// (and surrounding space) left over from the ")" split.
		t = strings.TrimSpace(t)
		t = strings.Trim(t, ",")
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
		// Any explicit fill supersedes an inherited unresolved paint ref.
		curStyle.pendingFillURL = ""
		gradient, ok := c.ReadGradURL(v, curStyle.fillerColor)
		if ok {
			if len(gradient.Stops) == 0 {
				// The gradient exists but has no stops yet: it inherits them from
				// another gradient via href, which finalize() only copies in AFTER
				// the whole document is parsed. Defer instead of freezing a
				// zero-stop value copy (which renders black); finalize resolves
				// href inheritance first, then re-resolves this reference.
				if id, isURL := parseURLID(v); isURL {
					curStyle.fillerColor = nil
					curStyle.pendingFillURL = id
					break
				}
			}
			curStyle.fillerColor = gradient
			break
		}
		pattern, ok, perr := c.readPatternURL(v)
		if perr != nil {
			return perr
		}
		if ok {
			curStyle.fillerColor = pattern
			break
		}
		if id, isURL := parseURLID(v); isURL {
			// The gradient/pattern is not defined yet. Record the id as a
			// forward paint reference and leave fill none (nil) for now;
			// finalize() resolves it once the whole document is parsed. If it
			// still does not resolve at EOF, none is the correct SVG fallback.
			curStyle.fillerColor = nil
			curStyle.pendingFillURL = id
			break
		}
		var err error
		curStyle.fillerColor, err = ParseSVGColor(v)
		return err
	case "stroke":
		// Any explicit stroke supersedes an inherited unresolved paint ref.
		curStyle.pendingStrokeURL = ""
		gradient, ok := c.ReadGradURL(v, curStyle.linerColor)
		if ok {
			if len(gradient.Stops) == 0 {
				// Zero-stop gradient inheriting stops via href; defer so finalize
				// re-resolves it after copying the inherited stops. (See fill.)
				if id, isURL := parseURLID(v); isURL {
					curStyle.linerColor = nil
					curStyle.pendingStrokeURL = id
					break
				}
			}
			curStyle.linerColor = gradient
			break
		}
		pattern, ok, perr := c.readPatternURL(v)
		if perr != nil {
			return perr
		}
		if ok {
			curStyle.linerColor = pattern
			break
		}
		if id, isURL := parseURLID(v); isURL {
			// Forward paint reference: resolved later in finalize().
			curStyle.linerColor = nil
			curStyle.pendingStrokeURL = id
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
		if v == "none" {
			// Explicitly clear inherited dashes rather than leaving them intact.
			curStyle.Dash = nil
			break
		}
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
	case "opacity", "stroke-opacity", "fill-opacity":
		op, err := parseFloat(v, 64)
		if err != nil {
			return err
		}
		op = clamp01(op)
		switch k {
		case "fill-opacity":
			// fill-opacity/stroke-opacity SET (replace) the per-element paint
			// opacity; they do not inherit-multiply.
			curStyle.FillOpacity = op
		case "stroke-opacity":
			curStyle.LineOpacity = op
		default: // "opacity"
			// Plain opacity is group opacity. True group compositing would
			// flatten the subtree and apply opacity once to the result; as an
			// approximation we accumulate it into groupOpacity, which multiplies
			// each descendant's fill/line opacity at draw time.
			curStyle.groupOpacity *= op
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
		val, err := parseFontSize(v, curStyle.FontSize)
		if err != nil {
			return err
		}
		// A non-positive resolved size keeps the inherited value.
		if val > 0 {
			if val > maxFontSize {
				val = maxFontSize
			}
			curStyle.FontSize = val
		}
	case "text-anchor":
		curStyle.TextAnchor = v
	case "font-weight":
		curStyle.FontWeight = v
	}
	return nil
}

func (c *IconCursor) readStartElement(se xml.StartElement) (err error) {
	name := se.Name.Local

	// If we are inside a skipped subtree (e.g. a top-level <pattern>), swallow
	// every descendant. The matching EndElement unwinds the depth counter.
	// Exception: gradient definitions (and their stops) inside the subtree are
	// paint servers that may be referenced elsewhere in the document, so keep
	// dispatching them into icon.Grads instead of dropping them. They are not
	// counted against skipShapeDepth (readEndElement mirrors this).
	if c.skipShapeDepth > 0 {
		if name == "radialGradient" || name == "linearGradient" || c.inGrad {
			if df, ok := drawFuncs[name]; ok {
				if err := df(c, se.Attr); err != nil {
					e := fmt.Sprintf("error during processing svg element %s: %s", name, err.Error())
					if c.returnError(e) {
						return errors.New(e)
					}
				}
			}
			return nil
		}
		c.skipShapeDepth++
		return nil
	}

	// <defs> is a container, not a collected definition. Track nesting so a
	// nested </defs> doesn't prematurely end the outer collection. Both the
	// outermost and any nested <defs> just bump the counter and ensure defs mode
	// is on; they are never added to the flat def lists or the depth model.
	if name == "defs" {
		c.defsNesting++
		c.inDefs = true
		return nil
	}

	// Gradient elements (and their stops) are not part of the flat defs model;
	// they are compiled directly into icon.Grads by their draw funcs.
	skipDef := name == "radialGradient" || name == "linearGradient" || c.inGrad

	if c.inDefs && !skipDef {
		ID := ""
		for _, attr := range se.Attr {
			if attr.Name.Local == "id" {
				ID = attr.Value
			}
		}
		c.defDepth++
		def := definition{ID: ID, Tag: name, Attrs: se.Attr}
		// Every open collector contains this element's def (nested elements
		// belong to their ancestors' replay lists as well as their own).
		for _, col := range c.openDefs {
			col.defs = append(col.defs, def)
		}
		if ID != "" {
			c.openDefs = append(c.openDefs, &defCollector{
				id:        ID,
				openDepth: c.defDepth,
				defs:      []definition{def},
			})
		}
		return nil
	}

	// A <pattern> outside <defs> is a paint server, not a shape; painting its
	// tile children as ordinary shapes is wrong. Skip the whole subtree.
	if name == "pattern" && !c.inDefs {
		c.skipShapeDepth = 1
		if c.ErrorMode == WarnErrorMode {
			log.Println("pattern outside defs is not rendered")
		}
		return nil
	}

	df, ok := drawFuncs[name]
	if !ok {
		errStr := "Cannot process svg element " + name
		if c.returnError(errStr) {
			return errors.New(errStr)
		}
		return nil
	}
	err = df(c, se.Attr)
	if err != nil {
		e := fmt.Sprintf("error during processing svg element %s: %s", name, err.Error())
		if c.returnError(e) {
			return errors.New(e)
		}
		err = nil
	}

	if len(c.Path) > 0 {
		//The cursor parsed a path from the xml element
		pathCopy := make(rasterx.Path, len(c.Path))
		copy(pathCopy, c.Path)
		c.icon.SVGPaths = append(c.icon.SVGPaths,
			SvgPath{
				PathStyle: c.StyleStack[len(c.StyleStack)-1],
				Path:      pathCopy,
				order:     c.icon.nextOrder(),
			})
		c.Path = c.Path[:0]
	}
	return
}

// readEndElement mirrors readStartElement: it pops the style pushed for the
// element (guarded against stack underflow on malformed input), unwinds the
// defs collection / skip-subtree state, and dispatches the per-tag end actions.
func (c *IconCursor) readEndElement(se xml.EndElement) error {
	// Pop the style pushed for this element.
	if len(c.StyleStack) > 1 {
		c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
	}

	// Unwind a skipped subtree (top-level pattern, etc.). Gradient ends (and
	// stops) inside the subtree were dispatched, not counted, in
	// readStartElement, so their ends unwind gradient state without touching
	// skipShapeDepth. Everything else decrements the depth counter as before.
	if c.skipShapeDepth > 0 {
		name := se.Name.Local
		switch {
		case name == "radialGradient" || name == "linearGradient":
			c.inGrad = false
		case c.inGrad:
			// A stop's end inside a gradient; nothing to unwind.
		default:
			c.skipShapeDepth--
		}
		return nil
	}

	name := se.Name.Local

	// Depth-aware defs collection: close/flush collectors as elements end.
	// Gradient starts never incremented defDepth, so their ends must not
	// decrement it.
	if c.inDefs && name != "defs" && name != "radialGradient" && name != "linearGradient" && !c.inGrad {
		c.endDefElement(name)
	}

	switch name {
	case "text":
		c.inText = false
		return c.compileText()
	case "title":
		c.inTitleText = false
	case "desc":
		c.inDescText = false
	case "defs":
		// Only the outermost </defs> flushes the collection and leaves defs mode.
		if c.defsNesting > 0 {
			c.defsNesting--
		}
		if c.defsNesting == 0 {
			c.closeAllDefs()
			c.inDefs = false
		}
	case "radialGradient", "linearGradient":
		c.inGrad = false
	case "style":
		if c.inDefsStyle {
			classes, err := parseClasses(c.classInfo)
			if err != nil {
				// Route malformed CSS through the error-mode policy like every
				// other style error: strict aborts; warn logs; ignore continues.
				e := fmt.Sprintf("error parsing <style> classes: %s", err.Error())
				if c.returnError(e) {
					return errors.New(e)
				}
				// Non-strict: keep whatever classes parsed before the error.
			}
			c.icon.classes = classes
			c.inDefsStyle = false
		}
	}
	return nil
}

// endDefElement handles the EndElement of an element that started inside
// <defs>. For a g/pattern end it appends a balancing endg/endpattern marker to
// every still-open collector (each of which contains that start), then flushes
// every collector opened at the current depth.
func (c *IconCursor) endDefElement(tag string) {
	if c.defDepth == 0 {
		return
	}
	if tag == "g" || tag == "pattern" {
		marker := "endg"
		if tag == "pattern" {
			marker = "endpattern"
		}
		for _, col := range c.openDefs {
			if col.openDepth <= c.defDepth {
				col.defs = append(col.defs, definition{Tag: marker})
			}
		}
	}
	kept := c.openDefs[:0]
	for _, col := range c.openDefs {
		if col.openDepth == c.defDepth {
			c.icon.Defs[col.id] = col.defs
		} else {
			kept = append(kept, col)
		}
	}
	c.openDefs = kept
	c.defDepth--
}

// closeAllDefs flushes any collectors still open when </defs> is reached and
// resets the defs collection state.
func (c *IconCursor) closeAllDefs() {
	for _, col := range c.openDefs {
		c.icon.Defs[col.id] = col.defs
	}
	c.openDefs = nil
	c.defDepth = 0
}

// parseURLID extracts the fragment id from a url(#id) reference, tolerating
// surrounding whitespace and quotes. It reports whether v is a url() reference.
func parseURLID(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "url(") || !strings.HasSuffix(v, ")") {
		return "", false
	}
	inner := strings.TrimSpace(v[4 : len(v)-1])
	inner = strings.Trim(inner, `'"`)
	inner = strings.TrimSpace(inner)
	if !strings.HasPrefix(inner, "#") {
		return "", false
	}
	return strings.TrimSpace(inner[1:]), true
}

// parseFontSize resolves an SVG font-size value against the inherited size.
// px and unitless values are taken as-is; pt is scaled by 4/3; the absolute
// units mm/cm/in/pc convert via the CSS px-per-unit factors (96px/in); em/rem
// scale the inherited size; % is inherited*v/100. Keywords and unparsable values
// return an error (handled per the error-mode policy by the caller).
func parseFontSize(v string, inherited float64) (float64, error) {
	v = strings.TrimSpace(v)
	num := v
	factor := 1.0
	switch {
	case strings.HasSuffix(v, "px"):
		num = strings.TrimSuffix(v, "px")
	case strings.HasSuffix(v, "pt"):
		num = strings.TrimSuffix(v, "pt")
		factor = 4.0 / 3.0
	case strings.HasSuffix(v, "mm"):
		num = strings.TrimSuffix(v, "mm")
		factor = 96.0 / 25.4 // CSS px per millimeter
	case strings.HasSuffix(v, "cm"):
		num = strings.TrimSuffix(v, "cm")
		factor = 96.0 / 2.54 // CSS px per centimeter
	case strings.HasSuffix(v, "in"):
		num = strings.TrimSuffix(v, "in")
		factor = 96.0 // CSS px per inch
	case strings.HasSuffix(v, "pc"):
		num = strings.TrimSuffix(v, "pc")
		factor = 16.0 // CSS px per pica (1pc = 12pt = 16px)
	case strings.HasSuffix(v, "rem"): // must precede the "em" check
		num = strings.TrimSuffix(v, "rem")
		factor = inherited
	case strings.HasSuffix(v, "em"):
		num = strings.TrimSuffix(v, "em")
		factor = inherited
	case strings.HasSuffix(v, "%"):
		num = strings.TrimSuffix(v, "%")
		factor = inherited / 100
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid font-size %q: %w", v, err)
	}
	return n * factor, nil
}

// appendTextChunk normalizes an SVG text CharData chunk (default xml:space
// handling: drop CR/LF, turn tabs into spaces, collapse space runs) and appends
// it as a text fragment. Empty results are dropped. Cross-fragment trimming is
// handled later in compileText.
func (c *IconCursor) appendTextChunk(data []byte) {
	var b strings.Builder
	b.Grow(len(data))
	prevSpace := false
	for _, r := range string(data) {
		switch r {
		case '\n', '\r':
			continue
		case '\t', ' ':
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	text := b.String()
	if text == "" {
		return
	}
	c.textFragments = append(c.textFragments, textFragment{
		text:  text,
		style: c.StyleStack[len(c.StyleStack)-1],
		x:     c.textX,
		y:     c.textY,
		dx:    c.textDx,
		dy:    c.textDy,
		hasX:  c.hasTextX,
		hasY:  c.hasTextY,
	})
	c.textDx = 0
	c.textDy = 0
	c.hasTextX = false
	c.hasTextY = false
}

// finalize resolves deferred parse state at end-of-document: first it copies
// gradient stops along recorded xlink:href/href inheritance chains, then it
// resolves forward paint references (fill/stroke url(#id) that pointed at a
// gradient or pattern declared later in the document).
func (c *IconCursor) finalize() error {
	for _, link := range c.pendingGradHrefs {
		if link.grad == nil || len(link.grad.Stops) > 0 {
			continue
		}
		if stops := c.resolveGradStops(link.target, map[string]bool{}, 0); stops != nil {
			// Copy so the inheriting gradient does not alias the source's slice.
			cp := make([]rasterx.GradStop, len(stops))
			copy(cp, stops)
			link.grad.Stops = cp
		}
	}

	// Forward paint references. Every gradient/pattern is known now, so resolve
	// any fill/stroke that referenced one before it was declared. Unresolvable
	// references keep the SVG "none" fallback (nil paint).
	if err := c.resolvePendingPaints(c.icon.SVGPaths); err != nil {
		return err
	}

	// Pattern tile children can carry the same forward references (a tile shape
	// filling url(#g) with g declared later). Resolving icon.SVGPaths alone left
	// those nil -> transparent tiles. Snapshot the pattern pointers first: a
	// resolve can compile a forward-referenced pattern and mutate the map, which
	// would otherwise panic mid-iteration. Any pattern compiled now (at EOF) sees
	// every gradient already, so it produces no new pending paints of its own.
	pats := make([]*Pattern, 0, len(c.icon.Patterns))
	for _, p := range c.icon.Patterns {
		pats = append(pats, p)
	}
	for _, p := range pats {
		if err := c.resolvePendingPaints(p.Paths); err != nil {
			return err
		}
	}
	return nil
}

// resolvePendingPaints resolves every deferred fill/stroke url(#id) reference in
// paths (see resolvePendingPaint). Unresolvable references fall back to none
// (nil paint), logged in WarnErrorMode. A pattern that fails to compile is
// routed through the error-mode policy exactly as a backward reference is in
// readStyleAttr: strict returns the error, warn logs it, and both non-strict
// modes fall back to none. It mutates paths in place.
func (c *IconCursor) resolvePendingPaints(paths []SvgPath) error {
	for i := range paths {
		sp := &paths[i]
		if sp.pendingFillURL != "" {
			paint, ok, err := c.resolvePendingPaint(sp.pendingFillURL, sp.fillerColor)
			if err != nil {
				if c.returnError("fill url(#" + sp.pendingFillURL + "): " + err.Error()) {
					return err
				}
			} else if ok {
				sp.fillerColor = paint
			} else if c.ErrorMode == WarnErrorMode {
				log.Println("fill url(#" + sp.pendingFillURL + ") never resolved to a gradient or pattern; falling back to none")
			}
			sp.pendingFillURL = ""
		}
		if sp.pendingStrokeURL != "" {
			paint, ok, err := c.resolvePendingPaint(sp.pendingStrokeURL, sp.linerColor)
			if err != nil {
				if c.returnError("stroke url(#" + sp.pendingStrokeURL + "): " + err.Error()) {
					return err
				}
			} else if ok {
				sp.linerColor = paint
			} else if c.ErrorMode == WarnErrorMode {
				log.Println("stroke url(#" + sp.pendingStrokeURL + ") never resolved to a gradient or pattern; falling back to none")
			}
			sp.pendingStrokeURL = ""
		}
	}
	return nil
}

// resolvePendingPaint resolves a deferred url(#id) paint reference to the final
// gradient (localized against defaultColor, as ReadGradURL does) or pattern.
// It reports whether the id resolved; a non-nil error means the id named a
// pattern whose children failed to compile.
func (c *IconCursor) resolvePendingPaint(id string, defaultColor interface{}) (interface{}, bool, error) {
	url := "url(#" + id + ")"
	if grad, ok := c.ReadGradURL(url, defaultColor); ok {
		return grad, true, nil
	}
	pat, ok, err := c.readPatternURL(url)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return pat, true, nil
	}
	return nil, false, nil
}

// resolveGradStops follows an xlink:href chain (cycle-guarded, depth <= 8) to
// the first gradient that defines stops, returning them.
func (c *IconCursor) resolveGradStops(target string, seen map[string]bool, depth int) []rasterx.GradStop {
	if depth > 8 || target == "" || seen[target] {
		return nil
	}
	seen[target] = true
	g, ok := c.icon.Grads[target]
	if !ok {
		return nil
	}
	if len(g.Stops) > 0 {
		return g.Stops
	}
	// The target itself may inherit its stops from another gradient.
	for _, link := range c.pendingGradHrefs {
		if link.grad == g {
			return c.resolveGradStops(link.target, seen, depth+1)
		}
	}
	return nil
}

// adaptClasses applies the named class selectors to pathStyle, in order. Per
// property errors are routed through the error-mode policy (StrictErrorMode
// aborts; Warn logs and skips; Ignore skips silently).
func (c *IconCursor) adaptClasses(pathStyle *PathStyle, classNames []string) error {
	if len(classNames) == 0 || len(c.icon.classes) == 0 {
		return nil
	}
	for _, className := range classNames {
		attrMap, ok := c.icon.classes[className]
		if !ok {
			continue
		}
		for k, v := range attrMap {
			if err := c.readStyleAttr(pathStyle, k, v); err != nil {
				e := fmt.Sprintf("error parsing class %q property %s:%s: %s", className, k, v, err.Error())
				if c.returnError(e) {
					return errors.New(e)
				}
			}
		}
	}
	return nil
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
		// Share the CSS class table so pattern tile children can resolve class
		// selectors (adaptClasses reads c.icon.classes).
		classes: origIcon.classes,
	}
	c.icon = dummyIcon

	origStyleStack := c.StyleStack
	c.StyleStack = []PathStyle{DefaultStyle}

	for i := 0; i < len(defs); i++ {
		def := defs[i]
		if def.Tag == "pattern" {
			if i == 0 {
				// defs[0] is this pattern's own marker (compilePattern already
				// consumed its attrs); its direct children follow.
				continue
			}
			// A nested pattern: its children belong to that pattern's own tile
			// (compiled independently via ReadPatternURL), not this one. Skip the
			// whole span to its matching endpattern, tracking depth for further
			// nesting (mirrors useF's span-skip).
			depth := 1
			for i++; i < len(defs) && depth > 0; i++ {
				switch defs[i].Tag {
				case "pattern":
					depth++
				case "endpattern":
					depth--
				}
			}
			i-- // outer loop's i++ steps past the matched endpattern
			continue
		}
		if def.Tag == "endpattern" {
			// This pattern's own closing marker (or a stray one); ignore.
			continue
		}
		if def.Tag == "endg" {
			if len(c.StyleStack) > 1 {
				c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
			}
			continue
		}
		if statefulDefTags[def.Tag] {
			// Never run titleF/descF & co. against the temporary icon: their
			// mode flags would outlive the icon swap (see statefulDefTags).
			continue
		}

		if err := c.PushStyle(def.Attrs); err != nil {
			c.icon = origIcon
			c.StyleStack = origStyleStack
			// Clear any partial geometry a failing property (e.g. a transform, or
			// a nested pattern compile) left in the cursor, so it cannot leak into
			// the next element compiled with this cursor.
			c.Path = c.Path[:0]
			return nil, err
		}

		df, ok := drawFuncs[def.Tag]
		if ok {
			if err := df(c, def.Attrs); err != nil {
				c.icon = origIcon
				c.StyleStack = origStyleStack
				// A drawFunc can populate c.Path before erroring (e.g. a path with
				// a bad trailing segment). Clear it so the partial geometry does
				// not leak into the next element's SvgPath.
				c.Path = c.Path[:0]
				return nil, err
			}
		}

		if len(c.Path) > 0 {
			pathCopy := make(rasterx.Path, len(c.Path))
			copy(pathCopy, c.Path)
			c.icon.SVGPaths = append(c.icon.SVGPaths, SvgPath{
				PathStyle: c.StyleStack[len(c.StyleStack)-1],
				Path:      pathCopy,
				order:     c.icon.nextOrder(),
			})
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

// readPatternURL is the internal form of ReadPatternURL that also returns any
// pattern-child compile error, so readStyleAttr can route it through the
// error-mode policy (strict surfaces it) instead of silently dropping it.
func (c *IconCursor) readPatternURL(v string) (pattern *Pattern, ok bool, err error) {
	if strings.HasPrefix(v, "url(") && strings.HasSuffix(v, ")") {
		urlStr := strings.TrimSpace(v[4 : len(v)-1])
		if strings.HasPrefix(urlStr, "#") {
			id := urlStr[1:]
			if pattern, ok = c.icon.Patterns[id]; ok {
				return pattern, true, nil
			}
			// If not in Patterns but in Defs, compile it. compilePattern
			// registers the pattern in c.icon.Patterns before recursing into
			// its children, so cyclic references resolve safely.
			defs, hasDefs := c.icon.Defs[id]
			if hasDefs && len(defs) > 0 && defs[0].Tag == "pattern" {
				p, cerr := c.compilePattern(defs)
				if cerr != nil {
					return nil, false, cerr
				}
				return p, true, nil
			}
		}
	}
	return nil, false, nil
}

// ReadPatternURL parses an SVG pattern url reference. It is a thin wrapper over
// readPatternURL that preserves the historical (pattern, ok) signature; any
// compile error is dropped (ok == false).
func (c *IconCursor) ReadPatternURL(v string) (pattern *Pattern, ok bool) {
	pattern, ok, _ = c.readPatternURL(v)
	return
}

// maxBitmapGlyphPixels bounds the total pixels decoded for distinct sbix
// glyphs in one document (32 Mpx ≈ 128 MB of RGBA). Repeated glyphs are served
// from the per-document cache and cost nothing further, so this is a limit on
// distinct bitmaps; once spent, remaining glyphs fall back to their vector
// outlines instead of allocating more.
const maxBitmapGlyphPixels = 32 << 20

// maxBitmapGlyphSide bounds the width and height of an sbix bitmap glyph.
// Real color-emoji strikes top out at a few hundred pixels; the cap exists
// because the PNG comes from the registered font bytes, so its declared IHDR
// size is attacker-controlled and png.Decode allocates for it up front.
const maxBitmapGlyphSide = 1024

// decodeBitmapGlyph decodes an sbix PNG after checking its declared dimensions
// against maxBitmapGlyphSide, so a font cannot make png.Decode allocate an
// arbitrarily large image (the later drawing is allocation-free and the ppem
// clamp only bounds the transform, neither of which protects this decode).
func decodeBitmapGlyph(pngBytes []byte) (image.Image, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 ||
		cfg.Width > maxBitmapGlyphSide || cfg.Height > maxBitmapGlyphSide {
		return nil, fmt.Errorf("bitmap glyph %dx%d exceeds %d px limit", cfg.Width, cfg.Height, maxBitmapGlyphSide)
	}
	return png.Decode(bytes.NewReader(pngBytes))
}

// bitmapGlyph returns the decoded sbix image for (font, idx), decoding
// pngBytes at most once per document and charging its pixels against the
// document budget. A cached glyph is always returned; a new one is refused
// (error) when it would exceed the budget or the per-glyph size cap, and the
// caller then draws the vector outline instead.
func (c *IconCursor) bitmapGlyph(fnt *sfnt.Font, idx sfnt.GlyphIndex, pngBytes []byte) (image.Image, error) {
	key := bitmapGlyphKey{font: fnt, idx: idx}
	if img, ok := c.bitmapCache[key]; ok {
		return img, nil
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, err
	}
	limit := c.bitmapPixelLimit
	if limit == 0 {
		limit = maxBitmapGlyphPixels
	}
	if px := cfg.Width * cfg.Height; px < 0 || c.bitmapPixels+px > limit {
		return nil, fmt.Errorf("bitmap glyph %dx%d exceeds the document budget of %d px", cfg.Width, cfg.Height, limit)
	}
	img, err := decodeBitmapGlyph(pngBytes)
	if err != nil {
		return nil, err
	}
	c.bitmapPixels += cfg.Width * cfg.Height
	if c.bitmapCache == nil {
		c.bitmapCache = map[bitmapGlyphKey]image.Image{}
	}
	c.bitmapCache[key] = img
	return img, nil
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

// glyphVisit is one resolved glyph produced by walkTextGlyphs. penX/penY is the
// pen position (in user units) at which the glyph is placed: horizontal kerning
// has already been applied but the glyph's own advance has not. font/idx/ppem
// identify the glyph and the size to load or measure it at. idx == 0 is the
// .notdef glyph (used when neither the fragment's own font nor any fallback has
// the rune) and is drawn/advanced like any other glyph rather than skipped.
type glyphVisit struct {
	frag       *textFragment
	font       *sfnt.Font
	idx        sfnt.GlyphIndex
	ppem       fixed.Int26_6
	penX, penY float64
}

// registryFont returns the font registered under name, or nil, holding the
// registry read lock for the lookup.
func registryFont(name string) *sfnt.Font {
	fontRegistryMu.RLock()
	defer fontRegistryMu.RUnlock()
	return fontRegistry[name]
}

// walkTextGlyphs is the single glyph iterator shared by compileText's
// measurement and layout passes, so the two can never disagree on advances,
// kerning, flag-ligature pairing, or fallback. It seeds the pen at
// (startX+shiftX, startY), then for each fragment applies its x/y/dx/dy (dx/dy
// exactly once — fixing the historical double-application of the first
// fragment's dx/dy), resolves each rune to a glyph (regional-indicator flag
// ligature -> per-rune fallback font -> .notdef), applies kerning, calls visit
// with the pen position BEFORE the glyph's advance, then advances the pen. The
// advance always comes from GlyphAdvance — including for bitmap/sbix glyphs —
// so a measurement walk (no-op visit) and a layout walk stay in lockstep. It
// returns the pen position after the last glyph so callers can thread the
// baseline (y) and running x across chunks.
func (c *IconCursor) walkTextGlyphs(frags []textFragment, startX, startY, shiftX float64,
	buf *sfnt.Buffer, visit func(glyphVisit)) (endX, endY float64) {

	penX := startX + shiftX
	penY := startY
	var prevIdx sfnt.GlyphIndex
	var prevFont *sfnt.Font

	for fi := range frags {
		frag := &frags[fi]
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

		runes := []rune(frag.text)
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			useFont := fontObj
			var idx sfnt.GlyphIndex

			isFlag := false
			if i+1 < len(runes) &&
				r >= regionalIndicatorBase && r <= regionalIndicatorEnd &&
				runes[i+1] >= regionalIndicatorBase && runes[i+1] <= regionalIndicatorEnd {
				code := string(rune(r-regionalIndicatorBase+'A')) + string(rune(runes[i+1]-regionalIndicatorBase+'A'))
				if emojiFont := registryFont("emoji"); emojiFont != nil {
					if gIdx, ok := resolveFlagGlyph(code, emojiFont, buf); ok {
						useFont = emojiFont
						idx = gIdx
						isFlag = true
						i++
					}
				}
			}

			if !isFlag {
				gi, err := useFont.GlyphIndex(buf, r)
				switch {
				case err == nil && gi != 0:
					idx = gi
				default:
					// Missing from the fragment's font: try the fallback chain,
					// else fall back to the fragment font's .notdef (idx 0).
					if f, fallbackIdx := resolveFallbackGlyph(r); f != nil {
						useFont = f
						idx = fallbackIdx
					} else {
						idx = 0
					}
				}
			}

			if useFont != prevFont {
				prevIdx = 0
				prevFont = useFont
			}

			// Kerning (skipped when either side is .notdef).
			if prevIdx != 0 && idx != 0 {
				if kern, err := useFont.Kern(buf, prevIdx, idx, ppem, font.HintingNone); err == nil {
					penX += float64(kern) / 64
				}
			}

			visit(glyphVisit{frag: frag, font: useFont, idx: idx, ppem: ppem, penX: penX, penY: penY})

			if adv, err := useFont.GlyphAdvance(buf, idx, ppem, font.HintingNone); err == nil {
				penX += float64(adv) / 64
			}
			prevIdx = idx
		}
	}
	return penX, penY
}

// textEmitter turns the glyphs of a chunk into SvgPaths (vector outlines,
// accumulated per fragment so each keeps its own style) and SvgImages (color
// bitmap / sbix glyphs). It is driven by walkTextGlyphs via emit; flush() must
// be called after the walk to append the final fragment's accumulated outline.
type textEmitter struct {
	c        *IconCursor
	buf      *sfnt.Buffer
	curFrag  *textFragment
	fragPath rasterx.Path
}

func (e *textEmitter) emit(g glyphVisit) {
	if e.curFrag != nil && g.frag != e.curFrag {
		e.flush()
	}
	e.curFrag = g.frag

	// Bitmap-first: place a color bitmap (sbix) glyph if the font has one.
	if bytesData, index := findFontBytesAndIndex(g.font); bytesData != nil {
		if pngBytes, originX, originY, maxPPEM, err := parseSBIX(bytesData, index, int(g.idx)); err == nil && pngBytes != nil && maxPPEM > 0 {
			if img, derr := e.c.bitmapGlyph(g.font, g.idx, pngBytes); derr == nil {
				// Scale from the strike's native design size (maxPPEM) to the
				// requested, already-clamped ppem — not the raw FontSize — so an
				// out-of-range font-size cannot blow up the transform.
				scale := (float64(g.ppem) / 64) / float64(maxPPEM)
				tx := g.penX + float64(originX)*scale
				ty := g.penY - (float64(originY)+float64(img.Bounds().Dy()))*scale
				e.c.icon.SVGImages = append(e.c.icon.SVGImages, SvgImage{
					Image:     img,
					Transform: g.frag.style.mAdder.M.Translate(tx, ty).Scale(scale, scale),
					Opacity:   g.frag.style.FillOpacity * g.frag.style.groupOpacity,
					order:     e.c.icon.nextOrder(),
				})
				return
			}
		}
	}

	// Vector outline.
	segs, err := g.font.LoadGlyph(e.buf, g.idx, g.ppem, nil)
	if err != nil {
		return
	}
	fixedPenX := fixed.Int26_6(g.penX * 64)
	fixedPenY := fixed.Int26_6(g.penY * 64)
	// Each MoveTo starts a new contour; close the previous one so multi-contour
	// glyphs (e.g. 'o', 'B') render correctly.
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
				e.fragPath.Stop(true)
			}
			e.fragPath.Start(args[0])
			contourOpen = true
		case sfnt.SegmentOpLineTo:
			e.fragPath.Line(args[0])
		case sfnt.SegmentOpQuadTo:
			e.fragPath.QuadBezier(args[0], args[1])
		case sfnt.SegmentOpCubeTo:
			e.fragPath.CubeBezier(args[0], args[1], args[2])
		}
	}
	if contourOpen {
		e.fragPath.Stop(true)
	}
}

// flush appends the current fragment's accumulated outline (if any) as one
// SvgPath carrying that fragment's style and a fresh draw-order stamp.
func (e *textEmitter) flush() {
	if len(e.fragPath) > 0 && e.curFrag != nil {
		e.c.icon.SVGPaths = append(e.c.icon.SVGPaths, SvgPath{
			PathStyle: e.curFrag.style,
			Path:      e.fragPath,
			order:     e.c.icon.nextOrder(),
		})
	}
	e.fragPath = nil
}

// normalizeBlockWhitespace applies SVG cross-fragment whitespace trimming to a
// text block (default xml:space): the leading spaces of the first non-empty
// fragment and the trailing spaces of the last are dropped, and a space that
// begins a fragment is dropped when the previous fragment already ended with one
// (so whitespace straddling a fragment boundary collapses to a single space).
// Per-chunk collapsing/newline removal already happened in appendTextChunk; this
// only fixes up the seams between fragments.
func normalizeBlockWhitespace(frags []textFragment) {
	first, last := -1, -1
	for i := range frags {
		if frags[i].text != "" {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		return
	}
	frags[first].text = strings.TrimLeft(frags[first].text, " ")
	frags[last].text = strings.TrimRight(frags[last].text, " ")

	prevEndsSpace := false
	for i := range frags {
		if frags[i].text == "" {
			continue
		}
		if prevEndsSpace && strings.HasPrefix(frags[i].text, " ") {
			frags[i].text = frags[i].text[1:]
		}
		if frags[i].text != "" {
			prevEndsSpace = strings.HasSuffix(frags[i].text, " ")
		}
	}
}

// compileText lays out the accumulated text fragments and appends the resulting
// glyph outlines (SvgPaths) and color bitmaps (SvgImages) to the icon.
//
// Fragments are split into chunks at each fragment that carries an explicit x;
// each chunk is measured and anchored (text-anchor) independently using its own
// first fragment's anchor, then laid out. A single walkTextGlyphs iterator backs
// both the measurement and layout passes so they cannot drift. The pen's x and
// baseline y thread across chunks so a later chunk without an explicit y (or
// with a cumulative dy) continues from the previous one.
func (c *IconCursor) compileText() error {
	if len(c.textFragments) == 0 {
		return nil
	}
	// System/emoji fonts load lazily on the first text layout (see
	// LoadSystemFonts) so importers that never render text don't pay the cost.
	LoadSystemFonts()

	frags := c.textFragments
	c.textFragments = nil

	normalizeBlockWhitespace(frags)

	var buf sfnt.Buffer

	// Seed the pen at the first fragment's x/y WITHOUT its dx/dy; walkTextGlyphs
	// applies dx/dy inside the per-fragment loop exactly once.
	penX := frags[0].x
	penY := frags[0].y

	for i := 0; i < len(frags); {
		j := i + 1
		for j < len(frags) && !frags[j].hasX {
			j++
		}
		chunk := frags[i:j]

		startX := penX
		if chunk[0].hasX {
			startX = chunk[0].x
		}

		// Measurement pass: chunk width is pen travel with no anchor shift.
		mEndX, _ := c.walkTextGlyphs(chunk, startX, penY, 0, &buf, func(glyphVisit) {})
		width := mEndX - startX

		var shiftX float64
		switch chunk[0].style.TextAnchor {
		case "middle":
			shiftX = -width / 2
		case "end":
			shiftX = -width
		}

		// Layout pass: emit glyphs at the anchored pen positions.
		em := textEmitter{c: c, buf: &buf}
		endX, endY := c.walkTextGlyphs(chunk, startX, penY, shiftX, &buf, em.emit)
		em.flush()

		penX, penY = endX, endY
		i = j
	}

	return nil
}
