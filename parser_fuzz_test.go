package oksvg

import (
	"strings"
	"testing"
)

func FuzzReadIconStream(f *testing.F) {
	for _, svg := range []string{
		`<svg/>`,
		`<svg><path d="M0 0A10 10 0 0110 20Z"/></svg>`,
		`<svg><defs><g id="a"><use href="#a"/></g></defs><use href="#a"/></svg>`,
		`<svg><defs><pattern id="p" width="1" height="1"><title>x</title><rect fill="url(#p)" width="1" height="1"/></pattern></defs><rect fill="url(#p)" width="1" height="1"/></svg>`,
		`<svg><pattern><linearGradient><title>x</title></linearGradient></pattern> </svg>`,
		`<svg><g fill="rgb(1,,1)"><rect width="1" height="1"/></g></svg>`,
		`<svg viewBox="0 0 9 9"><rect width="100%" height="50%"/><circle r="10%"/></svg>`,
		`<svg><defs><marker id="m" viewBox="0 0 10 10" refX="10" refY="5" orient="auto-start-reverse"><path d="M0 0L10 5L0 10z" marker-end="url(#m)"/></marker></defs><path d="M0 0C1 2 3 4 5 5Z" marker="url(#m)"/></svg>`,
		`<svg><marker id="m" markerUnits="userSpaceOnUse" orient="45deg"><g><use href="#m"/></g></marker><polyline points="0,0 1,1 2,0" marker-mid="url(#m)"/></svg>`,
	} {
		f.Add(svg)
	}
	f.Fuzz(func(t *testing.T, svg string) {
		if len(svg) > 16384 {
			t.Skip()
		}
		_, _ = ReadIconStream(strings.NewReader(svg), IgnoreErrorMode)
		_, _ = ReadIconStream(strings.NewReader(svg), StrictErrorMode)
	})
}
