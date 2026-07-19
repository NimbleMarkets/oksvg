// Copyright 2026 The oksvg Authors. All rights reserved.
//
// path_grammar_test.go covers Task T2's SVG path grammar fixes:
//   - the multi-arc AddArcFromA windowing bug
//   - the SVG number tokenizer (signs, exponents) used by GetPoints
//   - compact (unseparated) arc flags handled by getPointsArc
//   - CompilePath treating 'e'/'E' as part of a number, not a command letter
//   - malformed path data must error or degrade, never panic
package oksvg

import (
	"testing"

	"github.com/srwiley/rasterx"
)

// floatsEqual compares two float64 slices for exact equality. It exists
// because this package (oksvg) declares its own package-level "reflect"
// helper function (see path_cursor.go), which shadows the standard library
// "reflect" package and makes reflect.DeepEqual unavailable here.
func floatsEqual(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pathsEqual compares two rasterx.Path token streams for exact equality.
func pathsEqual(a, b rasterx.Path) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMultiArcBug reproduces the confirmed bug in AddArcFromA: it passed the
// *entire* remaining c.points slice to rasterx.AddArc instead of the 7-value
// window belonging to the current arc. For a single arc this is harmless
// (the window and the full slice are the same 7 values), but a path with two
// or more arcs packed into one "A"/"a" command corrupts every arc after the
// first. A multi-arc command must draw (and end at) exactly the same place
// as the equivalent path split into one arc per command.
func TestMultiArcBug(t *testing.T) {
	multi := new(PathCursor)
	if err := multi.CompilePath("M10 50A10 10 0 0 1 30 50 20 5 0 0 1 70 50"); err != nil {
		t.Fatalf("CompilePath(multi-arc) returned error: %v", err)
	}

	split := new(PathCursor)
	if err := split.CompilePath("M10 50A10 10 0 0 1 30 50A20 5 0 0 1 70 50"); err != nil {
		t.Fatalf("CompilePath(split-arc) returned error: %v", err)
	}

	if multi.placeX != 70 || multi.placeY != 50 {
		t.Errorf("multi-arc command cursor ended at (%v,%v), want (70,50)", multi.placeX, multi.placeY)
	}
	if split.placeX != 70 || split.placeY != 50 {
		t.Fatalf("sanity check failed: split-arc command cursor ended at (%v,%v), want (70,50)", split.placeX, split.placeY)
	}
	if !pathsEqual(multi.Path, split.Path) {
		t.Errorf("multi-arc path differs from the equivalent split-arc path\n multi: %s\n split: %s",
			multi.Path.ToSVGPath(), split.Path.ToSVGPath())
	}
}

// TestNumberTokenizerExponents exercises GetPoints as a standalone SVG
// number lexer: a sign (+/-) may start a number, there may be one decimal
// point, and an exponent marker (e or E) may be followed by an optional
// sign and digits.
func TestNumberTokenizerExponents(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []float64
	}{
		{"lower-e-plus-exponent", "1e+5", []float64{100000}},
		{"upper-E-exponent", "1E5", []float64{100000}},
		{"leading-minus-dot", "-.5", []float64{-0.5}},
		{"leading-plus-dot-exp-minus", "+.5e-2", []float64{0.005}},
		{"mixed-list", "1e+5 1E5 -.5 +.5e-2", []float64{100000, 100000, -0.5, 0.005}},
		{"adjacent-signed-numbers", "1e+5-2", []float64{100000, -2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := new(PathCursor)
			if err := c.GetPoints(tt.in); err != nil {
				t.Fatalf("GetPoints(%q) returned error: %v", tt.in, err)
			}
			if !floatsEqual(c.points, tt.want) {
				t.Errorf("GetPoints(%q) = %v, want %v", tt.in, c.points, tt.want)
			}
		})
	}
}

// TestCompactArcFlags exercises getPointsArc, which must tokenize arc
// parameters knowing that the large-arc-flag and sweep-flag are single
// digits (0 or 1) that the SVG grammar allows to run directly into
// neighboring numbers without a separator.
func TestCompactArcFlags(t *testing.T) {
	c := new(PathCursor)
	if err := c.getPointsArc("26.8 26.8 0 01-2.5-12.8"); err != nil {
		t.Fatalf("getPointsArc returned error: %v", err)
	}
	want := []float64{26.8, 26.8, 0, 0, 1, -2.5, -12.8}
	if !floatsEqual(c.points, want) {
		t.Errorf("getPointsArc(%q) points = %v, want %v", "26.8 26.8 0 01-2.5-12.8", c.points, want)
	}

	c2 := new(PathCursor)
	if err := c2.getPointsArc("1 1 0 011 1"); err != nil {
		t.Fatalf("getPointsArc returned error: %v", err)
	}
	if len(c2.points) != 7 {
		t.Fatalf("getPointsArc(%q) points = %v, want 7 values", "1 1 0 011 1", c2.points)
	}
	if c2.points[3] != 0 || c2.points[4] != 1 {
		t.Errorf("getPointsArc(%q) flags = (%v,%v), want (0,1)", "1 1 0 011 1", c2.points[3], c2.points[4])
	}

	// End to end through addSeg/CompilePath, which is where compact arc
	// flags actually appear in real SVG documents.
	full := new(PathCursor)
	if err := full.CompilePath("M0 0a26.8 26.8 0 01-2.5-12.8"); err != nil {
		t.Fatalf("CompilePath with compact arc flags returned error: %v", err)
	}
}

// TestArcFlagFullNumberForm reproduces F13: the arc large-arc/sweep flag scanner
// accepted only a single '0'/'1' digit, rejecting the full-number form "1.0"/
// "0.0" some producers emit (a regression vs HEAD). It must accept a flag digit
// followed by a decimal, iff the value is exactly 0 or 1, while keeping compact
// "01" flags working and still rejecting non-boolean values.
func TestArcFlagFullNumberForm(t *testing.T) {
	full := new(PathCursor)
	if err := full.CompilePath("M50 50 a25 25 0 1.0 1.0 0 50"); err != nil {
		t.Fatalf("CompilePath with full-number arc flags returned error: %v", err)
	}
	ref := new(PathCursor)
	if err := ref.CompilePath("M50 50 a25 25 0 1 1 0 50"); err != nil {
		t.Fatalf("CompilePath ref returned error: %v", err)
	}
	if !pathsEqual(full.Path, ref.Path) {
		t.Errorf("full-number arc flags produced a different path than the single-digit form\n full: %s\n ref:  %s",
			full.Path.ToSVGPath(), ref.Path.ToSVGPath())
	}

	// "0.0" is also accepted.
	if err := new(PathCursor).CompilePath("M50 50 a25 25 0 0.0 0.0 0 50"); err != nil {
		t.Errorf("CompilePath with 0.0 flags returned error: %v", err)
	}

	// Compact "01" flags still work.
	if err := new(PathCursor).CompilePath("M0 0a26.8 26.8 0 01-2.5-12.8"); err != nil {
		t.Errorf("compact arc flags regressed: %v", err)
	}

	// Non-boolean flag values still error.
	if err := new(PathCursor).CompilePath("M50 50 a25 25 0 2.0 1.0 0 50"); err == nil {
		t.Error("arc flag value 2.0 should error")
	}
	if err := new(PathCursor).CompilePath("M50 50 a25 25 0 1.5 1.0 0 50"); err == nil {
		t.Error("arc flag value 1.5 should error")
	}
}

// TestCompilePathExponentNotCommand reproduces the bug where CompilePath
// split path data into segments on every unicode letter, including the 'E'
// of a scientific-notation exponent. "M1E1 0L5 5z" must be read as a moveto
// to (10,0) -- 1E1 is 10 -- followed by a lineto and a close, not as an
// (invalid) "M1", "E1 0" ... split.
func TestCompilePathExponentNotCommand(t *testing.T) {
	c := new(PathCursor)
	if err := c.CompilePath("M1E1 0L5 5z"); err != nil {
		t.Fatalf("CompilePath(%q) returned error: %v", "M1E1 0L5 5z", err)
	}
	if c.pathStartX != 10 || c.pathStartY != 0 {
		t.Errorf("path start = (%v,%v), want (10,0)", c.pathStartX, c.pathStartY)
	}
	// 'z' closes back to the path start.
	if c.placeX != 10 || c.placeY != 0 {
		t.Errorf("final cursor = (%v,%v), want (10,0) after close", c.placeX, c.placeY)
	}

	segCount := 0
	for i := 0; i < len(c.Path); {
		switch rasterx.PathCommand(c.Path[i]) {
		case rasterx.PathMoveTo, rasterx.PathLineTo:
			segCount++
			i += 3
		case rasterx.PathClose:
			i++
		default:
			t.Fatalf("unexpected path command %v in %s", c.Path[i], c.Path.ToSVGPath())
		}
	}
	if segCount != 2 {
		t.Errorf("segCount = %d (path %s), want 2", segCount, c.Path.ToSVGPath())
	}
}

// TestMalformedPathNoPanic makes sure malformed "d" strings are rejected
// with an error (or, for degenerate-but-technically-valid input, silently
// accepted) and never panic.
func TestMalformedPathNoPanic(t *testing.T) {
	tests := []struct {
		name    string
		d       string
		wantErr bool
	}{
		// An empty "d" is valid SVG (disables rendering) — no error, no path.
		{"empty", "", false},
		{"garbage-commas", "M,,", true},
		{"lone-moveto-no-coords", "M", true},
		{"moveto-odd-coord-count", "M1,2,3", true},
		{"no-command-letters", "123,456", true},
		{"bad-arc-flag-digit", "M0 0a1 1 0 21 1", true},
		{"truncated-arc", "M0 0a1 1 0 01", true},
		{"lone-close", "z", false},
		{"valid-path", "M1 1L2 2z", false},
		{"valid-compact-arc-flags", "M0 0a26.8 26.8 0 01-2.5-12.8", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("CompilePath(%q) panicked: %v", tt.d, r)
				}
			}()
			c := new(PathCursor)
			err := c.CompilePath(tt.d)
			if tt.wantErr && err == nil {
				t.Errorf("CompilePath(%q) = nil error, want an error", tt.d)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("CompilePath(%q) = %v, want nil error", tt.d, err)
			}
		})
	}
}
