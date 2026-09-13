// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// draw.go maps SVG element tags to the functions that compile them into paths.

package oksvg

import (
	"encoding/xml"
	"errors"
	"log"
	"strconv"
	"strings"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"
)

// svgFunc defines function interface to use as drawing implementation.
type svgFunc func(c *IconCursor, attrs []xml.Attr) error

// maxUseDepth caps <use> replay nesting so a pathologically deep (but acyclic)
// chain of references cannot exhaust the goroutine stack. Cyclic references are
// caught earlier by IconCursor.useActive; this is the belt-and-suspenders bound.
const maxUseDepth = 40

// maxUseReplay caps the total number of definition elements replayed through
// <use> across one document. The depth cap alone does not bound expansion:
// a chain of groups that each <use> the previous level twice fans out to 2^n
// elements at only n levels deep (a 940-byte document produced 131k paths at
// depth 17). Once the budget is spent every further replay stops in all error
// modes; strict additionally reports it.
const maxUseReplay = 100000

// statefulDefTags are element tags whose drawFuncs set mode flags (inTitleText,
// inDescText, inDefs, inDefsStyle, inText, ...) that are only cleared by the
// matching EndElement. The flat replay loops in useF and compileDefs never see
// end elements, so replaying one would leave its flag stuck on and swallow the
// document's later CharData (real <text> vanishing into Titles) or, when the
// replay ran against compileDefs' temporary icon, index the original icon's
// empty Titles/Descriptions at [-1]. They also carry no replayable geometry
// (CharData is never captured in def lists), so both loops skip them entirely.
var statefulDefTags = map[string]bool{
	"title": true,
	"desc":  true,
	"defs":  true,
	"style": true,
	"text":  true,
	"tspan": true,
}

var (
	drawFuncs = map[string]svgFunc{
		"svg":            svgF,
		"g":              gF,
		"line":           lineF,
		"stop":           stopF,
		"rect":           rectF,
		"circle":         circleF,
		"ellipse":        circleF, //circleF handles ellipse also
		"polyline":       polylineF,
		"polygon":        polygonF,
		"path":           pathF,
		"desc":           descF,
		"defs":           defsF,
		"style":          styleF,
		"title":          titleF,
		"linearGradient": linearGradientF,
		"radialGradient": radialGradientF,
		"pattern":        patternF,
		"text":           textF,
		"tspan":          tspanF,
	}

	patternF svgFunc = func(*IconCursor, []xml.Attr) error { return nil }

	textF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.inText = true
		c.textFragments = nil
		c.textX = 0
		c.textY = 0
		c.textDx = 0
		c.textDy = 0
		c.hasTextX = false
		c.hasTextY = false
		return readTextCoordAttrs(c, attrs)
	}

	tspanF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		return readTextCoordAttrs(c, attrs)
	}

	svgF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.icon.ViewBox.X = 0
		c.icon.ViewBox.Y = 0
		c.icon.ViewBox.W = 0
		c.icon.ViewBox.H = 0
		var width, height float64
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "viewBox":
				err = c.GetPoints(attr.Value)
				if len(c.points) != 4 {
					return errParamMismatch
				}
				c.icon.ViewBox.X = c.points[0]
				c.icon.ViewBox.Y = c.points[1]
				c.icon.ViewBox.W = c.points[2]
				c.icon.ViewBox.H = c.points[3]
			case "width":
				width, err = parseFloat(attr.Value, 64)
			case "height":
				height, err = parseFloat(attr.Value, 64)
			}
			if err != nil {
				return err
			}
		}
		if c.icon.ViewBox.W == 0 {
			c.icon.ViewBox.W = width
		}
		if c.icon.ViewBox.H == 0 {
			c.icon.ViewBox.H = height
		}
		return nil
	}
	gF    svgFunc = func(*IconCursor, []xml.Attr) error { return nil } // g does nothing but push the style
	rectF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var x, y, w, h, rx, ry float64
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "x":
				x, err = parseFloat(attr.Value, 64)
			case "y":
				y, err = parseFloat(attr.Value, 64)
			case "width":
				w, err = parseFloat(attr.Value, 64)
			case "height":
				h, err = parseFloat(attr.Value, 64)
			case "rx":
				rx, err = parseFloat(attr.Value, 64)
			case "ry":
				ry, err = parseFloat(attr.Value, 64)
			}
			if err != nil {
				return err
			}
		}
		if w == 0 || h == 0 {
			return nil
		}
		rasterx.AddRoundRect(x, y, w+x, h+y, rx, ry, 0, rasterx.RoundGap, &c.Path)
		return nil
	}
	circleF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var cx, cy, rx, ry float64
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "cx":
				cx, err = parseFloat(attr.Value, 64)
			case "cy":
				cy, err = parseFloat(attr.Value, 64)
			case "r":
				rx, err = parseFloat(attr.Value, 64)
				ry = rx
			case "rx":
				rx, err = parseFloat(attr.Value, 64)
			case "ry":
				ry, err = parseFloat(attr.Value, 64)
			}
			if err != nil {
				return err
			}
		}
		if rx == 0 || ry == 0 { // not drawn, but not an error
			return nil
		}
		c.EllipseAt(cx, cy, rx, ry)
		return nil
	}
	lineF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var x1, x2, y1, y2 float64
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "x1":
				x1, err = parseFloat(attr.Value, 64)
			case "x2":
				x2, err = parseFloat(attr.Value, 64)
			case "y1":
				y1, err = parseFloat(attr.Value, 64)
			case "y2":
				y2, err = parseFloat(attr.Value, 64)
			}
			if err != nil {
				return err
			}
		}
		c.Path.Start(fixed.Point26_6{
			X: fixed.Int26_6((x1) * 64),
			Y: fixed.Int26_6((y1) * 64)})
		c.Path.Line(fixed.Point26_6{
			X: fixed.Int26_6((x2) * 64),
			Y: fixed.Int26_6((y2) * 64)})
		return nil
	}
	polylineF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "points":
				err = c.GetPoints(attr.Value)
				if len(c.points)%2 != 0 {
					return errors.New("polygon has odd number of points")
				}
			}
			if err != nil {
				return err
			}
		}
		if len(c.points) >= 4 {
			c.Path.Start(fixed.Point26_6{
				X: fixed.Int26_6((c.points[0]) * 64),
				Y: fixed.Int26_6((c.points[1]) * 64)})
			for i := 2; i < len(c.points)-1; i += 2 {
				c.Path.Line(fixed.Point26_6{
					X: fixed.Int26_6((c.points[i]) * 64),
					Y: fixed.Int26_6((c.points[i+1]) * 64)})
			}
		}
		return nil
	}
	polygonF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		err := polylineF(c, attrs)
		if len(c.points) >= 4 {
			c.Path.Stop(true)
		}
		return err
	}
	pathF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "d":
				err = c.CompilePath(attr.Value)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	descF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.inDescText = true
		c.icon.Descriptions = append(c.icon.Descriptions, "")
		return nil
	}
	titleF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.inTitleText = true
		c.icon.Titles = append(c.icon.Titles, "")
		return nil
	}
	defsF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.inDefs = true
		return nil
	}
	styleF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.inDefsStyle = true
		return nil
	}
	linearGradientF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var err error
		c.inGrad = true
		c.grad = &rasterx.Gradient{Points: [5]float64{0, 0, 1, 0, 0},
			IsRadial: false, Bounds: c.icon.ViewBox, Matrix: rasterx.Identity}
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "id":
				id := attr.Value
				if id == "" {
					return errZeroLengthID
				}
				c.icon.Grads[id] = c.grad
			case "x1":
				c.grad.Points[0], err = readFraction(attr.Value)
			case "y1":
				c.grad.Points[1], err = readFraction(attr.Value)
			case "x2":
				c.grad.Points[2], err = readFraction(attr.Value)
			case "y2":
				c.grad.Points[3], err = readFraction(attr.Value)
			default:
				err = c.ReadGradAttr(attr)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	radialGradientF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		c.inGrad = true
		c.grad = &rasterx.Gradient{Points: [5]float64{0.5, 0.5, 0.5, 0.5, 0.5},
			IsRadial: true, Bounds: c.icon.ViewBox, Matrix: rasterx.Identity}
		var setFx, setFy bool
		var err error
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "id":
				id := attr.Value
				if id == "" {
					return errZeroLengthID
				}
				c.icon.Grads[id] = c.grad
			case "r":
				c.grad.Points[4], err = readFraction(attr.Value)
			case "cx":
				c.grad.Points[0], err = readFraction(attr.Value)
			case "cy":
				c.grad.Points[1], err = readFraction(attr.Value)
			case "fx":
				setFx = true
				c.grad.Points[2], err = readFraction(attr.Value)
			case "fy":
				setFy = true
				c.grad.Points[3], err = readFraction(attr.Value)
			default:
				err = c.ReadGradAttr(attr)
			}
			if err != nil {
				return err
			}
		}
		if !setFx { // set fx to cx by default
			c.grad.Points[2] = c.grad.Points[0]
		}
		if !setFy { // set fy to cy by default
			c.grad.Points[3] = c.grad.Points[1]
		}
		return nil
	}
	stopF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var err error
		if c.inGrad {
			stop := rasterx.GradStop{Opacity: 1.0}
			for _, attr := range attrs {
				switch attr.Name.Local {
				case "offset":
					stop.Offset, err = readFraction(attr.Value)
				case "stop-color":
					//todo: add current color inherit
					stop.StopColor, err = ParseSVGColor(attr.Value)
				case "stop-opacity":
					stop.Opacity, err = parseFloat(attr.Value, 64)
				}
				if err != nil {
					return err
				}
			}
			c.grad.Stops = append(c.grad.Stops, stop)
		}
		return nil
	}
	useF svgFunc = func(c *IconCursor, attrs []xml.Attr) error {
		var (
			href string
			x, y float64
			err  error
		)
		for _, attr := range attrs {
			switch attr.Name.Local {
			case "href":
				href = attr.Value
			case "x":
				x, err = parseFloat(attr.Value, 64)
			case "y":
				y, err = parseFloat(attr.Value, 64)
			}
			if err != nil {
				return err
			}
		}
		// Translate the Style adder matrix by use's x and y
		c.StyleStack[len(c.StyleStack)-1].mAdder.M =
			c.StyleStack[len(c.StyleStack)-1].mAdder.M.Translate(x, y)
		if href == "" {
			return errors.New("only use tags with href is supported")
		}
		if !strings.HasPrefix(href, "#") {
			return errors.New("only the ID CSS selector is supported")
		}
		id := href[1:]
		defs, ok := c.icon.Defs[id]
		if !ok {
			return errors.New("href ID in use statement was not found in saved defs")
		}
		// Cyclic or too-deeply-nested <use> guard. A self-reference (#a->#a) or a
		// mutual cycle (#a->#b->#a) would otherwise recurse until the goroutine
		// stack overflows (an unrecoverable process crash). Route it through the
		// error-mode policy instead: strict aborts, warn logs and skips, ignore
		// skips silently.
		if c.useActive == nil {
			c.useActive = map[string]bool{}
		}
		if c.useActive[id] || c.useDepth >= maxUseDepth {
			errStr := "cyclic or too-deeply-nested use reference: " + href
			if c.ErrorMode == StrictErrorMode {
				return errors.New(errStr)
			} else if c.ErrorMode == WarnErrorMode {
				log.Println(errStr)
			}
			return nil
		}
		c.useActive[id] = true
		c.useDepth++
		defer func() {
			delete(c.useActive, id)
			c.useDepth--
		}()
		// baseDepth is the StyleStack depth at entry (the <use> element's own
		// style is already pushed). useF must be stack-neutral: it never pops
		// below baseDepth (guarding against stray endg/endpattern markers in
		// today's unbalanced def lists) and truncates any styles it leaked
		// (e.g. a group whose matching endg is missing) on return. This keeps
		// the outer </use> and </svg> pops from underflowing.
		baseDepth := len(c.StyleStack)
		defer func() {
			if len(c.StyleStack) > baseDepth {
				c.StyleStack = c.StyleStack[:baseDepth]
			}
		}()
		for i := 0; i < len(defs); i++ {
			def := defs[i]
			c.useReplayed++
			if c.useReplayed > maxUseReplay {
				errStr := "use expansion exceeds replay budget of " +
					strconv.Itoa(maxUseReplay) + " elements: " + href
				if !c.useBudgetSpent {
					c.useBudgetSpent = true
					if c.ErrorMode == WarnErrorMode {
						log.Println(errStr)
					}
				}
				if c.ErrorMode == StrictErrorMode {
					return errors.New(errStr)
				}
				return nil
			}
			if def.Tag == "endg" {
				// pop style, but never below baseDepth (guards against
				// unbalanced endg markers in the def list).
				if len(c.StyleStack) > baseDepth {
					c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
				}
				continue
			}
			if def.Tag == "pattern" {
				// Pattern defs are tiled paint, not drawable content. Skip the
				// whole pattern subtree up to its matching endpattern, tracking
				// depth for nested patterns.
				depth := 1
				for i++; i < len(defs) && depth > 0; i++ {
					switch defs[i].Tag {
					case "pattern":
						depth++
					case "endpattern":
						depth--
					}
				}
				i-- // outer loop's i++ will step past the matched endpattern
				continue
			}
			if def.Tag == "endpattern" {
				// Stray marker (unbalanced list); ignore.
				continue
			}
			if statefulDefTags[def.Tag] {
				continue
			}
			if err = c.PushStyle(def.Attrs); err != nil {
				return err
			}
			df, ok := drawFuncs[def.Tag]
			if !ok {
				errStr := "Cannot process svg element " + def.Tag
				if c.ErrorMode == StrictErrorMode {
					return errors.New(errStr)
				} else if c.ErrorMode == WarnErrorMode {
					log.Println(errStr)
				}
				// Unknown tag: skip this element (pop the style we pushed) and
				// keep processing the rest of the def list.
				if len(c.StyleStack) > baseDepth {
					c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
				}
				continue
			}
			if err := df(c, def.Attrs); err != nil {
				return err
			}
			//Did c.Path get added to during the drawFunction call iteration?
			if len(c.Path) > 0 {
				//The cursor parsed a path from the xml element
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
				// pop style
				if len(c.StyleStack) > baseDepth {
					c.StyleStack = c.StyleStack[:len(c.StyleStack)-1]
				}
			}
		}
		return nil
	}
)

func init() {
	// avoids cyclical static declaration
	// called on package initialization
	drawFuncs["use"] = useF
}

// readTextCoordAttrs reads x/y/dx/dy attributes shared by <text> and <tspan>
// and propagates parseFloat errors instead of silently zeroing the coordinate,
// matching the error-handling style of the other shape draw funcs.
func readTextCoordAttrs(c *IconCursor, attrs []xml.Attr) error {
	var err error
	for _, attr := range attrs {
		switch attr.Name.Local {
		case "x":
			c.textX, err = parseFloat(attr.Value, 64)
			c.hasTextX = true
		case "y":
			c.textY, err = parseFloat(attr.Value, 64)
			c.hasTextY = true
		case "dx":
			c.textDx, err = parseFloat(attr.Value, 64)
		case "dy":
			c.textDy, err = parseFloat(attr.Value, 64)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
