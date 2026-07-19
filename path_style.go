// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// path_style.go defines the PathStyle used while drawing an SVG path.

package oksvg

import (
	"image/color"

	"github.com/srwiley/rasterx"
)

// PathStyle holds the state of the SVG style.
type PathStyle struct {
	FillOpacity, LineOpacity          float64
	LineWidth, DashOffset, MiterLimit float64
	Dash                              []float64
	UseNonZeroWinding                 bool
	fillerColor, linerColor           interface{} // color.Color, rasterx.Gradient, or *Pattern
	LineGap                           rasterx.GapFunc
	LeadLineCap                       rasterx.CapFunc // This is used if different than LineCap
	LineCap                           rasterx.CapFunc
	LineJoin                          rasterx.JoinMode
	mAdder                            rasterx.MatrixAdder // current transform
	FontFamily                        string
	FontSize                          float64
	TextAnchor                        string
	FontWeight                        string

	// groupOpacity is the accumulated opacity contributed by ancestor group
	// (<g opacity=...>) elements. It multiplies the element's own fill/line
	// opacity at draw time. DefaultStyle sets it to 1.0.
	groupOpacity float64
	// pendingFillURL and pendingStrokeURL hold unresolved url(#id) paint
	// references for forward-declared gradients/patterns. Reserved for a later
	// wave; declared here so the field layout is stable.
	pendingFillURL, pendingStrokeURL string
}

// styleAttribute describes draw options, such as {"fill":"black"; "stroke":"white"}.
type styleAttribute = map[string]string

// DefaultStyle sets the default PathStyle to fill black, winding rule,
// full opacity, no stroke, ButtCap line end and Bevel line connect, with default font settings.
var DefaultStyle = PathStyle{
	FillOpacity:       1.0,
	LineOpacity:       1.0,
	LineWidth:         1.0, // SVG spec default stroke-width (was 2.0; intentional behavior change)
	DashOffset:        0.0,
	MiterLimit:        4.0,
	UseNonZeroWinding: true,
	fillerColor:       color.NRGBA{0x00, 0x00, 0x00, 0xff},
	LineCap:           rasterx.ButtCap,
	LineJoin:          rasterx.Bevel,
	mAdder:            rasterx.MatrixAdder{M: rasterx.Identity},
	FontFamily:        "sans-serif",
	FontSize:          12.0,
	TextAnchor:        "start",
	FontWeight:        "normal",
	groupOpacity:      1.0,
}
