// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// utils.go implements translation of an SVG2.0 path into a rasterx Path.

package oksvg

import (
	"errors"
	"log"
	"math"
	"strings"
	"unicode"

	"github.com/srwiley/rasterx"

	"golang.org/x/image/math/fixed"
)

type (
	// ErrorMode is the for setting how the parser reacts to unparsed elements
	ErrorMode uint8
	// PathCursor is used to parse SVG format path strings into a rasterx Path
	PathCursor struct {
		rasterx.Path
		placeX, placeY         float64
		cntlPtX, cntlPtY       float64
		pathStartX, pathStartY float64
		points                 []float64
		lastKey                uint8
		ErrorMode              ErrorMode
		inPath                 bool
	}
)

const (
	// IgnoreErrorMode skips un-parsed SVG elements.
	IgnoreErrorMode ErrorMode = iota

	// WarnErrorMode outputs a warning when an un-parsed SVG element is found.
	WarnErrorMode

	// StrictErrorMode causes an error when an un-parsed SVG element is found.
	StrictErrorMode
)

var (
	errParamMismatch  = errors.New("param mismatch")
	errCommandUnknown = errors.New("unknown command")
	errZeroLengthID   = errors.New("zero length id")
)

// ReadFloat reads a floating point value and adds it to the cursor's points slice.
func (c *PathCursor) ReadFloat(numStr string) error {
	last := 0
	isFirst := true
	for i, n := range numStr {
		if n == '.' {
			if isFirst {
				isFirst = false
				continue
			}
			f, err := parseFloat(numStr[last:i], 64)
			if err != nil {
				return err
			}
			c.points = append(c.points, f)
			last = i
		}
	}
	f, err := parseFloat(numStr[last:], 64)
	if err != nil {
		return err
	}
	c.points = append(c.points, f)
	return nil
}

// GetPoints reads a set of floating point values from the SVG format number string,
// and add them to the cursor's points slice.
//
// It tokenizes dataPoints as a run of SVG numbers: a number may start with a
// sign (+/-), contains digits and (via ReadFloat) one or more decimal
// points, and may end with an exponent marker ('e' or 'E') followed by an
// optional sign and digits. A '+' or '-' that is not part of an exponent
// starts a new number, matching the SVG grammar for lists of numbers packed
// together without whitespace (e.g. "1-2" is two numbers, "1e-2" is one).
func (c *PathCursor) GetPoints(dataPoints string) error {
	c.points = c.points[0:0]
	lastIndex := -1
	sawExp := false // just consumed an exponent marker; a following sign belongs to it
	for i, r := range dataPoints {
		switch {
		case unicode.IsDigit(r) || r == '.':
			if lastIndex == -1 {
				lastIndex = i
			}
			sawExp = false
		case r == 'e' || r == 'E':
			if lastIndex == -1 {
				lastIndex = i
			}
			sawExp = true
		case r == '+' || r == '-':
			if sawExp {
				// Sign belongs to the exponent; still part of the current number.
				sawExp = false
				continue
			}
			if lastIndex != -1 {
				if err := c.ReadFloat(dataPoints[lastIndex:i]); err != nil {
					return err
				}
			}
			lastIndex = i
			sawExp = false
		default:
			if lastIndex != -1 {
				if err := c.ReadFloat(dataPoints[lastIndex:i]); err != nil {
					return err
				}
			}
			lastIndex = -1
			sawExp = false
		}
	}
	if lastIndex != -1 {
		if err := c.ReadFloat(dataPoints[lastIndex:]); err != nil {
			return err
		}
	}
	return nil
}

// getPointsArc reads one or more sets of arc parameters (rx, ry,
// x-axis-rotation, large-arc-flag, sweep-flag, x, y) from an "a"/"A" path
// segment's data string and fills the cursor's points slice, 7 values per
// arc. Per the SVG grammar, the two flag parameters are single digits ('0'
// or '1') that may run directly into neighboring numbers without a
// separator, e.g. "26.8 26.8 0 01-2.5-12.8" (flags "0" and "1" immediately
// followed by the signed number "-2.5"). Every other parameter is parsed
// with the same number grammar as GetPoints.
func (c *PathCursor) getPointsArc(s string) error {
	c.points = c.points[0:0]
	pos := 0
	for {
		pos = skipArcDelims(s, pos)
		if pos >= len(s) {
			break
		}
		for param := 0; param < 7; param++ {
			pos = skipArcDelims(s, pos)
			if pos >= len(s) {
				return errParamMismatch
			}
			if param == 3 || param == 4 {
				// large-arc-flag / sweep-flag. Per the SVG grammar these are
				// single digits (0 or 1) that may run directly into the next
				// number without a separator (compact "01"). Some producers emit
				// them in full-number form ("1.0"/"0.0"); when a '.' follows the
				// digit, consume the whole number token and accept iff its value
				// is exactly 0 or 1.
				if s[pos] != '0' && s[pos] != '1' {
					return errParamMismatch
				}
				if pos+1 < len(s) && s[pos+1] == '.' {
					tok, next, err := scanArcNumber(s, pos)
					if err != nil {
						return err
					}
					f, err := parseFloat(tok, 64)
					if err != nil {
						return err
					}
					if f != 0 && f != 1 {
						return errParamMismatch
					}
					c.points = append(c.points, f)
					pos = next
					continue
				}
				if s[pos] == '0' {
					c.points = append(c.points, 0)
				} else {
					c.points = append(c.points, 1)
				}
				pos++
				continue
			}
			tok, next, err := scanArcNumber(s, pos)
			if err != nil {
				return err
			}
			f, err := parseFloat(tok, 64)
			if err != nil {
				return err
			}
			c.points = append(c.points, f)
			pos = next
		}
	}
	return nil
}

// skipArcDelims advances pos past whitespace and comma delimiters.
func skipArcDelims(s string, pos int) int {
	for pos < len(s) {
		switch s[pos] {
		case ' ', '\t', '\n', '\r', ',':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// scanArcNumber scans a single SVG number (optional sign, digits, optional
// single decimal point and digits, optional exponent with optional sign and
// digits) starting at s[pos], which must not be a delimiter. It returns the
// number's substring and the position immediately following it.
func scanArcNumber(s string, pos int) (string, int, error) {
	start := pos
	n := len(s)
	if pos < n && (s[pos] == '+' || s[pos] == '-') {
		pos++
	}
	digits := 0
	for pos < n && s[pos] >= '0' && s[pos] <= '9' {
		pos++
		digits++
	}
	if pos < n && s[pos] == '.' {
		pos++
		for pos < n && s[pos] >= '0' && s[pos] <= '9' {
			pos++
			digits++
		}
	}
	if digits == 0 {
		return "", pos, errParamMismatch
	}
	if pos < n && (s[pos] == 'e' || s[pos] == 'E') {
		expStart := pos
		pos++
		if pos < n && (s[pos] == '+' || s[pos] == '-') {
			pos++
		}
		expDigits := 0
		for pos < n && s[pos] >= '0' && s[pos] <= '9' {
			pos++
			expDigits++
		}
		if expDigits == 0 {
			// Not actually an exponent (e.g. a trailing bare 'e'); back out.
			pos = expStart
		}
	}
	return s[start:pos], pos, nil
}

// EllipseAt adds a path of an elipse centered at cx, cy of radius rx and ry
// to the PathCursor
func (c *PathCursor) EllipseAt(cx, cy, rx, ry float64) {
	c.placeX, c.placeY = cx+rx, cy
	c.points = c.points[0:0]
	c.points = append(c.points, rx, ry, 0.0, 1.0, 0.0, c.placeX, c.placeY)
	c.Path.Start(fixed.Point26_6{
		X: fixed.Int26_6(c.placeX * 64),
		Y: fixed.Int26_6(c.placeY * 64)})
	c.placeX, c.placeY = rasterx.AddArc(c.points, cx, cy, c.placeX, c.placeY, &c.Path)
	c.Path.Stop(true)
}

// AddArcFromA adds a path of an arc element to the cursor path to the PathCursor
func (c *PathCursor) AddArcFromA(points []float64) {
	cx, cy := rasterx.FindEllipseCenter(&points[0], &points[1], points[2]*math.Pi/180, c.placeX,
		c.placeY, points[5], points[6], points[4] == 0, points[3] == 0)
	c.placeX, c.placeY = rasterx.AddArc(points[:7], cx, cy, c.placeX, c.placeY, &c.Path)
}

// CompilePath translates the svgPath description string into a rasterx path.
// All valid SVG path elements are interpreted to rasterx equivalents.
// The resulting path element is stored in the PathCursor.
func (c *PathCursor) CompilePath(svgPath string) error {
	c.init()
	// An empty (or whitespace-only) "d" attribute is valid SVG: it disables
	// rendering of the element and is not an error.
	if strings.TrimSpace(svgPath) == "" {
		return nil
	}
	lastIndex := -1
	segCount := 0
	for i, v := range svgPath {
		// 'e' and 'E' are exponent markers within a number (e.g. "1e-5"),
		// never path commands, so they must not split a segment here.
		if unicode.IsLetter(v) && v != 'e' && v != 'E' {
			if lastIndex != -1 {
				if err := c.addSeg(svgPath[lastIndex:i]); err != nil {
					return err
				}
				segCount++
			}
			lastIndex = i
		}
	}
	if lastIndex != -1 {
		if err := c.addSeg(svgPath[lastIndex:]); err != nil {
			return err
		}
		segCount++
	}
	if segCount == 0 {
		// No path command was ever found: empty or garbage "d" data.
		return errParamMismatch
	}
	return nil
}

func reflect(px, py, rx, ry float64) (x, y float64) {
	return px*2 - rx, py*2 - ry
}

func (c *PathCursor) valsToAbs(last float64) {
	for i := 0; i < len(c.points); i++ {
		last += c.points[i]
		c.points[i] = last
	}
}

func (c *PathCursor) pointsToAbs(sz int) {
	lastX := c.placeX
	lastY := c.placeY
	for j := 0; j < len(c.points); j += sz {
		for i := 0; i < sz; i += 2 {
			c.points[i+j] += lastX
			c.points[i+1+j] += lastY
		}
		lastX = c.points[(j+sz)-2]
		lastY = c.points[(j+sz)-1]
	}
}

func (c *PathCursor) hasSetsOrMore(sz int, rel bool) bool {
	if !(len(c.points) >= sz && len(c.points)%sz == 0) {
		return false
	}
	if rel {
		c.pointsToAbs(sz)
	}
	return true
}

func (c *PathCursor) reflectControlQuad() {
	switch c.lastKey {
	case 'q', 'Q', 'T', 't':
		c.cntlPtX, c.cntlPtY = reflect(c.placeX, c.placeY, c.cntlPtX, c.cntlPtY)
	default:
		c.cntlPtX, c.cntlPtY = c.placeX, c.placeY
	}
}

func (c *PathCursor) reflectControlCube() {
	switch c.lastKey {
	case 'c', 'C', 's', 'S':
		c.cntlPtX, c.cntlPtY = reflect(c.placeX, c.placeY, c.cntlPtX, c.cntlPtY)
	default:
		c.cntlPtX, c.cntlPtY = c.placeX, c.placeY
	}
}

// addSeg decodes an SVG seqment string into equivalent raster path commands saved
// in the cursor's Path
func (c *PathCursor) addSeg(segString string) error {
	k := segString[0]
	// Parse the string describing the numeric points in SVG format. Arc
	// commands get their own tokenizer since their large-arc-flag and
	// sweep-flag parameters are single digits that may be packed directly
	// against neighboring numbers without a separator.
	if k == 'a' || k == 'A' {
		if err := c.getPointsArc(segString[1:]); err != nil {
			return err
		}
	} else {
		if err := c.GetPoints(segString[1:]); err != nil {
			return err
		}
	}
	l := len(c.points)
	rel := false
	switch k {
	case 'z':
		fallthrough
	case 'Z':
		if len(c.points) != 0 {
			return errParamMismatch
		}
		if c.inPath {
			c.Path.Stop(true)
			c.placeX = c.pathStartX
			c.placeY = c.pathStartY
			c.inPath = false
		}
	case 'm':
		rel = true
		fallthrough
	case 'M':
		if !c.hasSetsOrMore(2, rel) {
			return errParamMismatch
		}
		c.pathStartX, c.pathStartY = c.points[0], c.points[1]
		c.inPath = true
		c.Path.Start(fixed.Point26_6{X: fixed.Int26_6((c.pathStartX) * 64), Y: fixed.Int26_6((c.pathStartY) * 64)})
		for i := 2; i < l-1; i += 2 {
			c.Path.Line(fixed.Point26_6{
				X: fixed.Int26_6((c.points[i]) * 64),
				Y: fixed.Int26_6((c.points[i+1]) * 64)})
		}
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 'l':
		rel = true
		fallthrough
	case 'L':
		if !c.hasSetsOrMore(2, rel) {
			return errParamMismatch
		}
		for i := 0; i < l-1; i += 2 {
			c.Path.Line(fixed.Point26_6{
				X: fixed.Int26_6((c.points[i]) * 64),
				Y: fixed.Int26_6((c.points[i+1]) * 64)})
		}
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 'v':
		c.valsToAbs(c.placeY)
		fallthrough
	case 'V':
		if !c.hasSetsOrMore(1, false) {
			return errParamMismatch
		}
		for _, p := range c.points {
			c.Path.Line(fixed.Point26_6{
				X: fixed.Int26_6((c.placeX) * 64),
				Y: fixed.Int26_6((p) * 64)})
		}
		c.placeY = c.points[l-1]
	case 'h':
		c.valsToAbs(c.placeX)
		fallthrough
	case 'H':
		if !c.hasSetsOrMore(1, false) {
			return errParamMismatch
		}
		for _, p := range c.points {
			c.Path.Line(fixed.Point26_6{
				X: fixed.Int26_6((p) * 64),
				Y: fixed.Int26_6((c.placeY) * 64)})
		}
		c.placeX = c.points[l-1]
	case 'q':
		rel = true
		fallthrough
	case 'Q':
		if !c.hasSetsOrMore(4, rel) {
			return errParamMismatch
		}
		for i := 0; i < l-3; i += 4 {
			c.Path.QuadBezier(
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i]) * 64),
					Y: fixed.Int26_6((c.points[i+1]) * 64)},
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i+2]) * 64),
					Y: fixed.Int26_6((c.points[i+3]) * 64)})
		}
		c.cntlPtX, c.cntlPtY = c.points[l-4], c.points[l-3]
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 't':
		rel = true
		fallthrough
	case 'T':
		if !c.hasSetsOrMore(2, rel) {
			return errParamMismatch
		}
		for i := 0; i < l-1; i += 2 {
			c.reflectControlQuad()
			c.Path.QuadBezier(
				fixed.Point26_6{
					X: fixed.Int26_6((c.cntlPtX) * 64),
					Y: fixed.Int26_6((c.cntlPtY) * 64)},
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i]) * 64),
					Y: fixed.Int26_6((c.points[i+1]) * 64)})
			c.lastKey = k
			c.placeX = c.points[i]
			c.placeY = c.points[i+1]
		}
	case 'c':
		rel = true
		fallthrough
	case 'C':
		if !c.hasSetsOrMore(6, rel) {
			return errParamMismatch
		}
		for i := 0; i < l-5; i += 6 {
			c.Path.CubeBezier(
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i]) * 64),
					Y: fixed.Int26_6((c.points[i+1]) * 64)},
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i+2]) * 64),
					Y: fixed.Int26_6((c.points[i+3]) * 64)},
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i+4]) * 64),
					Y: fixed.Int26_6((c.points[i+5]) * 64)})
		}
		c.cntlPtX, c.cntlPtY = c.points[l-4], c.points[l-3]
		c.placeX = c.points[l-2]
		c.placeY = c.points[l-1]
	case 's':
		rel = true
		fallthrough
	case 'S':
		if !c.hasSetsOrMore(4, rel) {
			return errParamMismatch
		}
		for i := 0; i < l-3; i += 4 {
			c.reflectControlCube()
			c.Path.CubeBezier(fixed.Point26_6{
				X: fixed.Int26_6((c.cntlPtX) * 64), Y: fixed.Int26_6((c.cntlPtY) * 64)},
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i]) * 64), Y: fixed.Int26_6((c.points[i+1]) * 64)},
				fixed.Point26_6{
					X: fixed.Int26_6((c.points[i+2]) * 64), Y: fixed.Int26_6((c.points[i+3]) * 64)})
			c.lastKey = k
			c.cntlPtX, c.cntlPtY = c.points[i], c.points[i+1]
			c.placeX = c.points[i+2]
			c.placeY = c.points[i+3]
		}
	case 'a', 'A':
		if !c.hasSetsOrMore(7, false) {
			return errParamMismatch
		}
		for i := 0; i < l-6; i += 7 {
			if k == 'a' {
				c.points[i+5] += c.placeX
				c.points[i+6] += c.placeY
			}
			c.AddArcFromA(c.points[i:])
		}
	default:
		if c.ErrorMode == StrictErrorMode {
			return errCommandUnknown
		}
		if c.ErrorMode == WarnErrorMode {
			log.Println("Ignoring svg command " + string(k))
		}
	}
	// So we know how to extend some segment types
	c.lastKey = k
	return nil
}

func (c *PathCursor) init() {
	c.placeX = 0.0
	c.placeY = 0.0
	c.points = c.points[0:0]
	c.lastKey = ' '
	c.Path.Clear()
	c.inPath = false
}
