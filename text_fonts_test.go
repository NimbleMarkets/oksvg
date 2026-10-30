package oksvg

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/sfnt"
)

type testFaceSource struct{ fail string }

func (s testFaceSource) Face(_ string, v string) ([]byte, error) {
	if v == s.fail {
		return nil, fmt.Errorf("unavailable")
	}
	return map[string][]byte{"Regular": goregular.TTF, "Bold": gobold.TTF, "Italic": goitalic.TTF, "BoldItalic": gobolditalic.TTF}[v], nil
}

// Give a real Go font a small synthetic cmap. This isolates coverage decisions
// without depending on installed fonts or embedding a new font dependency.
func coverageFont(t *testing.T, rs ...rune) []byte {
	t.Helper()
	data := append([]byte(nil), goregular.TTF...)
	f, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	cmap := make([]byte, 28+len(rs)*12)
	put16 := binary.BigEndian.PutUint16
	put32 := binary.BigEndian.PutUint32
	put16(cmap[2:], 1)
	put16(cmap[4:], 3)
	put16(cmap[6:], 10)
	put32(cmap[8:], 12)
	put16(cmap[12:], 12)
	put32(cmap[16:], uint32(16+len(rs)*12))
	put32(cmap[24:], uint32(len(rs)))
	for i, r := range rs {
		index, _ := f.GlyphIndex(nil, r)
		if index == 0 {
			index, _ = f.GlyphIndex(nil, 'A')
		}
		o := 28 + i*12
		put32(cmap[o:], uint32(r))
		put32(cmap[o+4:], uint32(r))
		put32(cmap[o+8:], uint32(index))
	}
	for i := 0; i < int(binary.BigEndian.Uint16(data[4:])); i++ {
		o := 12 + i*16
		if string(data[o:o+4]) == "cmap" {
			put32(data[o+8:], uint32(len(data)))
			put32(data[o+12:], uint32(len(cmap)))
			return append(data, cmap...)
		}
	}
	t.Fatal("no cmap")
	return nil
}
func TestFontVariantsAndAtomicRegistration(t *testing.T) {
	if err := RegisterFontFamily("Test Variants", testFaceSource{}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterFontAlias("Test Alias", "Test Variants"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ weight, style, suffix string }{{"normal", "normal", ""}, {"700", "normal", "-bold"}, {"normal", "italic", "-italic"}, {"600", "oblique", "-bolditalic"}} {
		ps := DefaultStyle
		ps.FontFamily = "Test Alias"
		ps.FontWeight = v.weight
		ps.FontStyle = v.style
		if got := textFontCandidates(ps)[0]; got != registryFont("test variants"+v.suffix) {
			t.Fatalf("wrong variant %+v", v)
		}
	}
	before := registryFont("test variants")
	if err := RegisterFontFamily("Test Variants", testFaceSource{fail: "Bold"}); err == nil {
		t.Fatal("expected failure")
	}
	if registryFont("test variants") != before {
		t.Fatal("partial registration escaped")
	}
}
func TestCSSFallbackAndCombiningCluster(t *testing.T) {
	for name, rs := range map[string][]rune{"test-base": {'A'}, "test-next": {'A', 'V', 0x301}, "test-host": {'V'}} {
		if err := RegisterFont(name, coverageFont(t, rs...)); err != nil {
			t.Fatal(err)
		}
	}
	fontRegistryMu.RLock()
	old := textFontOptions
	fontRegistryMu.RUnlock()
	defer ConfigureTextFonts(old)
	if err := ConfigureTextFonts(TextFontOptions{FallbackFamilies: []string{"test-host"}, DisableSystemFonts: true}); err != nil {
		t.Fatal(err)
	}
	ps := DefaultStyle
	ps.FontFamily = "test-base, test-next"
	c := &IconCursor{}
	var buf sfnt.Buffer
	var visits []glyphVisit
	c.walkTextGlyphs([]textFragment{{text: "A\u0301V", style: ps}}, 0, 0, 0, &buf, func(g glyphVisit) { visits = append(visits, g) })
	for _, g := range visits {
		if g.font != registryFont("test-next") {
			t.Fatal("CSS remainder must cover entire accent cluster before host fallback")
		}
	}
	ps.FontFamily = "test-base"
	visits = nil
	c.walkTextGlyphs([]textFragment{{text: "V", style: ps}}, 0, 0, 0, &buf, func(g glyphVisit) { visits = append(visits, g) })
	if visits[0].font != registryFont("test-host") {
		t.Fatal("host fallback not used")
	}
}
func TestMeasureMatchesSVGAndMissingGlyphs(t *testing.T) {
	style := TextStyle{Family: "sans-serif", Weight: "bold", Style: "italic", Size: 24}
	text := "AV café \U0010fffd"
	m := MeasureText(text, style)
	ps := DefaultStyle
	ps.FontFamily = style.Family
	ps.FontWeight = style.Weight
	ps.FontStyle = style.Style
	ps.FontSize = style.Size
	c := &IconCursor{}
	var buf sfnt.Buffer
	advance, _ := c.walkTextGlyphs([]textFragment{{text: text, style: ps}}, 0, 0, 0, &buf, func(glyphVisit) {})
	if m.Advance != advance || m.MissingGlyphs != 1 {
		t.Fatalf("metrics %+v walk=%g", m, advance)
	}
	icon, err := ReadIconStream(strings.NewReader(`<svg><text font-size="24" font-weight="bold" font-style="italic">` + text + `</text></svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if icon.MissingGlyphs != 1 {
		t.Fatalf("missing %d", icon.MissingGlyphs)
	}
}
func TestBaselineInheritanceAndSpanRestoration(t *testing.T) {
	raw := `<svg><text x="20" y="50" dominant-baseline="central" font-style="italic" font-size="24">A<tspan dominant-baseline="alphabetic" font-style="normal">B</tspan>C</text></svg>`
	icon, err := ReadIconStream(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(icon.SVGPaths) != 3 {
		t.Fatalf("paths %d", len(icon.SVGPaths))
	}
	for i, want := range []string{"central", "alphabetic", "central"} {
		if icon.SVGPaths[i].DominantBaseline != want {
			t.Fatalf("baseline %d", i)
		}
	}
	for i, want := range []string{"italic", "normal", "italic"} {
		if icon.SVGPaths[i].FontStyle != want {
			t.Fatalf("style %d", i)
		}
	}
	ps := DefaultStyle
	ps.FontSize = 24
	f := textFontCandidates(ps)[0]
	var buf sfnt.Buffer
	// A font with ascenders and descenders places central below its alphabetic
	// baseline, middle at half x-height; these offsets must not accumulate.
	for _, mode := range []string{"middle", "central", "text-before-edge", "text-after-edge"} {
		ps.DominantBaseline = mode
		offset := baselineOffset(ps, f, &buf)
		if (mode == "text-after-edge" && offset >= 0) || (mode != "text-after-edge" && offset <= 0) {
			t.Fatalf("%s offset %g", mode, offset)
		}
	}
}
func TestMonospaceAndItalicAreDistinct(t *testing.T) {
	a := MeasureText("iiii", TextStyle{Family: "monospace", Size: 24})
	b := MeasureText("WWWW", TextStyle{Family: "monospace", Size: 24})
	if a.Advance != b.Advance {
		t.Fatal("generic monospace is proportional")
	}
	parse := func(style string) *SvgIcon {
		icon, err := ReadIconStream(strings.NewReader(`<svg><text font-size="24" font-style="` + style + `">Hello</text></svg>`))
		if err != nil {
			t.Fatal(err)
		}
		return icon
	}
	normal, italic := parse("normal"), parse("italic")
	if fmt.Sprint(normal.SVGPaths[0].Path) == fmt.Sprint(italic.SVGPaths[0].Path) {
		t.Fatal("italic has normal outlines")
	}
}

func TestBaselineDoesNotAccumulateAcrossFragments(t *testing.T) {
	ps := DefaultStyle
	ps.FontSize = 24
	ps.DominantBaseline = "central"
	normal := ps
	normal.DominantBaseline = "alphabetic"
	frags := []textFragment{{text: "A", style: ps, y: 50, hasY: true}, {text: "B", style: normal}, {text: "C", style: ps}}
	c := &IconCursor{}
	var buf sfnt.Buffer
	var ys []float64
	c.walkTextGlyphs(frags, 0, 0, 0, &buf, func(g glyphVisit) { ys = append(ys, g.penY) })
	if len(ys) != 3 || ys[0] != ys[2] || ys[1] != 50 || ys[0] <= 50 {
		t.Fatalf("baseline positions %v", ys)
	}
}
func BenchmarkSVGText(b *testing.B) {
	text := strings.Repeat("AV café e\u0301 → ", 64)
	MeasureText(text, TextStyle{Family: "sans-serif", Size: 24})
	b.Run("measure", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			MeasureText(text, TextStyle{Family: "sans-serif", Size: 24})
		}
	})
	raw := `<svg><text font-size="24">` + text + `</text></svg>`
	b.Run("parse", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := ReadIconStream(strings.NewReader(raw)); err != nil {
				b.Fatal(err)
			}
		}
	})
}
