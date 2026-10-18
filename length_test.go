// Copyright 2026 The oksvg Authors. All rights reserved.
//
// length_test.go covers percentage lengths in shape geometry.

package oksvg

import (
	"strings"
	"testing"
)

func TestReadLengthPercentages(t *testing.T) {
	c := &IconCursor{icon: &SvgIcon{}}
	c.icon.ViewBox.W, c.icon.ViewBox.H = 200, 100
	for _, tc := range []struct {
		v    string
		axis lengthAxis
		want float64
	}{
		{"50%", horizontal, 100},
		{" 50% ", vertical, 50},
		{"100%", other, 158.11388300841898}, // sqrt((200²+100²)/2)
		{"12", horizontal, 12},
		{"12px", vertical, 12},
	} {
		got, err := c.readLength(tc.v, tc.axis)
		if err != nil || got != tc.want {
			t.Errorf("readLength(%q, %d) = %v, %v; want %v", tc.v, tc.axis, got, err, tc.want)
		}
	}
	if _, err := c.readLength("x%", horizontal); err == nil {
		t.Error("a malformed percentage should be an error")
	}
}

// go-mermaid paints its page background with a 100%-sized rect; before
// percentages resolved, the parse error dropped the rect and left the page
// transparent.
func TestPercentRectFillsViewport(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="20" viewBox="0 0 40 20">
  <rect width="100%" height="100%" fill="#ff0000"/>
</svg>`
	img := renderSVG(t, svg, 40, 20, StrictErrorMode)
	for _, p := range [][2]int{{1, 1}, {38, 18}, {20, 10}} {
		if r, g, b, a := rgba8(img, p[0], p[1]); r != 255 || g != 0 || b != 0 || a != 255 {
			t.Fatalf("pixel %v = %d,%d,%d,%d; want opaque red", p, r, g, b, a)
		}
	}
}

func TestPercentShapesResolveAgainstViewport(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50">
  <rect x="50%" y="0" width="50%" height="100%" fill="#0000ff"/>
  <circle cx="25%" cy="50%" r="10%" fill="#00ff00"/>
  <line x1="0" y1="10%" x2="100%" y2="10%" stroke="#ff0000" stroke-width="2"/>
</svg>`
	img := renderSVG(t, svg, 100, 50, StrictErrorMode)
	if r, g, b, _ := rgba8(img, 75, 25); r != 0 || g != 0 || b != 255 {
		t.Errorf("right half should be the blue rect, got %d,%d,%d", r, g, b)
	}
	if r, g, b, _ := rgba8(img, 25, 25); r != 0 || g != 255 || b != 0 {
		t.Errorf("circle at 25%%,50%% should be green, got %d,%d,%d", r, g, b)
	}
	if r, _, _, a := rgba8(img, 10, 5); r < 200 || a < 200 {
		t.Errorf("line at y=10%% should be red, got r=%d a=%d", r, a)
	}
	if _, _, _, a := rgba8(img, 25, 45); a != 0 {
		t.Errorf("below the circle should be empty, alpha %d", a)
	}
}

func TestPercentRootSizeKeepsViewBox(t *testing.T) {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100%" height="100%" viewBox="0 0 30 10"><rect width="30" height="10"/></svg>`
	icon, err := ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	if err != nil {
		t.Fatal(err)
	}
	if icon.ViewBox.W != 30 || icon.ViewBox.H != 10 {
		t.Fatalf("viewBox = %+v, want 30×10", icon.ViewBox)
	}
}
