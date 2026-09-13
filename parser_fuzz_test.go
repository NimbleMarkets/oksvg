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
