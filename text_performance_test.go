package oksvg

import (
	"fmt"
	reflectpkg "reflect"
	"strings"
	"testing"

	"golang.org/x/image/font/sfnt"
)

func BenchmarkRelativeText(b *testing.B) {
	LoadSystemFonts()
	text := strings.Repeat("AV café e\u0301 → ", 64)
	style := DefaultStyle
	style.FontSize = 24
	fragments := []textFragment{{text: text, style: style}}
	b.Run("measure", func(b *testing.B) {
		c := &IconCursor{}
		var buf sfnt.Buffer
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			c.walkTextGlyphs(fragments, 0, 0, 0, &buf, func(glyphVisit) {})
		}
	})
	for _, fixture := range []struct{ name, svg string }{
		{"plain", `<svg><text font-size="24">` + text + `</text></svg>`},
		{"styled", `<svg><text font-size="24" dominant-baseline="central">` + strings.Repeat(`AV <tspan font-weight="bold" font-style="italic">café</tspan> → `, 64) + `</text></svg>`},
	} {
		b.Run(fixture.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := ReadIconStream(strings.NewReader(fixture.svg)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Eviction must only change lookup cost, never the resolved fallback order.
func TestTextFontCacheEviction(t *testing.T) {
	var cache textFontCache
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < 24; i++ {
			s := DefaultStyle
			s.FontFamily = fmt.Sprintf("unknown-%d, sans-serif", i)
			if i%2 == 0 {
				s.FontWeight = "bold"
			}
			if i%3 == 0 {
				s.FontStyle = "italic"
			}
			want := textFontCandidates(s)
			for repeat := 0; repeat < 2; repeat++ {
				got := cache.candidates(s)
				if !reflectpkg.DeepEqual(got, want) {
					t.Fatalf("lookup %d/%d differs after cache reuse", pass, i)
				}
			}
		}
	}
}
