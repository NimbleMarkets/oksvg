// Copyright 2026 The oksvg Authors. All rights reserved.
//
// length.go resolves SVG lengths, including percentages of the viewport.

package oksvg

import (
	"math"
	"strconv"
	"strings"
)

// lengthAxis says what a percentage length is a percentage of.
type lengthAxis int

const (
	// horizontal lengths (x, width, cx, rx, x1, x2) use the viewport width.
	horizontal lengthAxis = iota
	// vertical lengths (y, height, cy, ry, y1, y2) use the viewport height.
	vertical
	// other lengths (r) use the normalized diagonal, sqrt((w²+h²)/2).
	other
)

// readLength parses a geometry length. A percentage resolves against the root
// viewport (the viewBox, or the width and height when there is none), as SVG
// specifies for shapes. Nested <svg> viewports are not tracked, so lengths in
// them resolve against the root.
func (c *IconCursor) readLength(v string, axis lengthAxis) (float64, error) {
	v = strings.TrimSpace(v)
	if !strings.HasSuffix(v, "%") {
		return parseFloat(v, 64)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(v, "%")), 64)
	if err != nil {
		return 0, err
	}
	w, h := c.icon.ViewBox.W, c.icon.ViewBox.H
	ref := math.Sqrt((w*w + h*h) / 2)
	switch axis {
	case horizontal:
		ref = w
	case vertical:
		ref = h
	}
	return f / 100 * ref, nil
}
