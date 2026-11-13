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
