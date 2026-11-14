// Copyright 2026 The oksvg Authors. All rights reserved.
package oksvg

import (
	"strings"
	"testing"
)

func TestFillRuleCascade(t *testing.T) {
	for _, tc := range []struct {
		name, svg string
		want      bool
	}{
		{"default", `<path d="M0 0H10V10Z"/>`, true},
		{"attribute", `<path fill-rule="evenodd" d="M0 0H10V10Z"/>`, false},
		{"inherited", `<g fill-rule="evenodd"><path d="M0 0H10V10Z"/></g>`, false},
		{"inline wins", `<path fill-rule="nonzero" style="fill-rule:evenodd" d="M0 0H10V10Z"/>`, false},
		{"child override", `<g fill-rule="evenodd"><path fill-rule="nonzero" d="M0 0H10V10Z"/></g>`, true},
		{"inherit overrides attribute", `<g fill-rule="evenodd"><path fill-rule="nonzero" style="fill-rule:inherit" d="M0 0H10V10Z"/></g>`, false},
		{"initial", `<g fill-rule="evenodd"><path fill-rule="initial" d="M0 0H10V10Z"/></g>`, true},
		{"class", `<defs><style>.hole{fill-rule:evenodd}</style></defs><path class="hole" d="M0 0H10V10Z"/>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ic, e := ReadIconStream(strings.NewReader(`<svg>`+tc.svg+`</svg>`), StrictErrorMode)
			if e != nil {
				t.Fatal(e)
			}
			if len(ic.SVGPaths) != 1 || ic.SVGPaths[0].UseNonZeroWinding != tc.want {
				t.Fatalf("fill rule: %+v", ic.SVGPaths)
			}
		})
	}
}
func TestFillRuleInvalidKeepsPriorValue(t *testing.T) {
	src := `<svg><path style="fill-rule:evenodd;fill-rule:bogus" d="M0 0H10V10Z"/></svg>`
	if _, e := ReadIconStream(strings.NewReader(src), StrictErrorMode); e == nil {
		t.Fatal("accepted invalid rule")
	}
	ic, e := ReadIconStream(strings.NewReader(src), IgnoreErrorMode)
	if e != nil {
		t.Fatal(e)
	}
	if ic.SVGPaths[0].UseNonZeroWinding {
		t.Fatal("invalid declaration overwrote evenodd")
	}
}

// Exercise the parser and the published scanner together. Both subpaths have
// the same direction, so only even-odd winding cuts out the center. The second
// path checks that a preceding even-odd fill does not leak into default fills.
func TestFillRuleRenderedHoles(t *testing.T) {
	for _, tc := range []struct {
		name, attr  string
		centerAlpha uint8
	}{
		{"default", "", 255},
		{"nonzero", `fill-rule="nonzero"`, 255},
		{"evenodd", `fill-rule="evenodd"`, 0},
		{"inline", `style="fill-rule:evenodd"`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `<svg viewBox="0 0 32 16"><g ` + tc.attr + `><path fill="#ff0000" d="M1 1H15V15H1Z M5 5H11V11H5Z"/></g><path fill="#ff0000" d="M17 1H31V15H17Z M21 5H27V11H21Z"/></svg>`
			icon, err := ReadIconStream(strings.NewReader(src), StrictErrorMode)
			if err != nil {
				t.Fatal(err)
			}
			for _, scale := range []int{1, 2} {
				img := renderIcon(icon, 32*scale, 16*scale)
				for _, p := range []struct {
					x, y  int
					alpha uint8
				}{
					{8, 8, tc.centerAlpha}, {2, 8, 255}, {0, 8, 0}, {24, 8, 255},
				} {
					if got := img.RGBAAt(p.x*scale, p.y*scale).A; got != p.alpha {
						t.Errorf("scale %d pixel (%d,%d): alpha %d, want %d", scale, p.x, p.y, got, p.alpha)
					}
				}
			}
		})
	}
}
