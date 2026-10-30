package oksvg

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/gofont/gomonobolditalic"
	"golang.org/x/image/font/gofont/gomonoitalic"
	"golang.org/x/image/font/sfnt"
)

// FaceSource supplies immutable font bytes; it is compatible with ntfonts.Source.
// Font registration/configuration should finish before measuring/parsing documents.
type FaceSource interface {
	Face(family, variant string) ([]byte, error)
}

// RegisterFontFamily atomically registers all four faces under a CSS family.
// Source calls and parsing happen before taking the registry lock. It does not
// load system fonts or import a font bundle. Each face is limited to 32 MiB.
func RegisterFontFamily(family string, source FaceSource) error {
	if strings.TrimSpace(family) == "" || source == nil {
		return fmt.Errorf("font family and source are required")
	}
	variants := []string{"Regular", "Bold", "Italic", "BoldItalic"}
	suffixes := []string{"", "-bold", "-italic", "-bolditalic"}
	fonts := make([]*sfnt.Font, 4)
	data := make([][]byte, 4)
	for i, v := range variants {
		raw, err := source.Face(family, v)
		if err != nil {
			return err
		}
		if len(raw) > 32<<20 {
			return fmt.Errorf("font face exceeds 32 MiB")
		}
		duplicate := -1
		for j := 0; j < i; j++ {
			if bytes.Equal(raw, data[j]) {
				duplicate = j
				break
			}
		}
		if duplicate >= 0 {
			data[i] = data[duplicate]
			fonts[i] = fonts[duplicate]
			continue
		}
		data[i] = append([]byte(nil), raw...)
		fonts[i], err = sfnt.Parse(data[i])
		if err != nil {
			return err
		}
	}
	fontRegistryMu.Lock()
	defer fontRegistryMu.Unlock()
	for i, s := range suffixes {
		name := strings.ToLower(strings.TrimSpace(family)) + s
		fontRegistry[name] = fonts[i]
		fontBytesRegistry[name] = data[i]
		fontIndexRegistry[name] = 0
	}
	return nil
}

// TextFontOptions configures the existing process-wide registry. FallbackFamilies
// are tried after every CSS family and before Go fonts. Their order is preserved.
// Configure once at host startup, as with RegisterFont. Changing registrations
// between measurement and parsing can change metrics.
type TextFontOptions struct {
	FallbackFamilies   []string
	DisableSystemFonts bool
}

var textFontOptions = TextFontOptions{FallbackFamilies: []string{"emoji", "emoji-0", "emoji-1", "symbols"}}

func ConfigureTextFonts(options TextFontOptions) error {
	if len(options.FallbackFamilies) > 32 {
		return fmt.Errorf("at most 32 fallback families are supported")
	}
	options.FallbackFamilies = append([]string(nil), options.FallbackFamilies...)
	fontRegistryMu.Lock()
	textFontOptions = options
	fontRegistryMu.Unlock()
	return nil
}
func loadTextSystemFonts() {
	fontRegistryMu.RLock()
	disabled := textFontOptions.DisableSystemFonts
	fontRegistryMu.RUnlock()
	if !disabled {
		LoadSystemFonts()
	}
}

func init() {
	for name, data := range map[string][]byte{
		"sans-serif-italic": goitalic.TTF, "default-italic": goitalic.TTF, "serif-italic": goitalic.TTF,
		"sans-serif-bolditalic": gobolditalic.TTF, "default-bolditalic": gobolditalic.TTF, "serif-bolditalic": gobolditalic.TTF,
		"monospace": gomono.TTF, "monospace-bold": gomonobold.TTF, "monospace-italic": gomonoitalic.TTF, "monospace-bolditalic": gomonobolditalic.TTF,
		"go mono": gomono.TTF, "go mono-bold": gomonobold.TTF, "go mono-italic": gomonoitalic.TTF, "go mono-bolditalic": gomonobolditalic.TTF,
	} {
		_ = RegisterFont(name, data)
	}
	_ = RegisterFontAlias("Go", "default")
}

func fontSuffix(weight, style string) string {
	italic := strings.HasPrefix(strings.ToLower(strings.TrimSpace(style)), "italic") || strings.HasPrefix(strings.ToLower(strings.TrimSpace(style)), "oblique")
	if isBold(weight) {
		if italic {
			return "-bolditalic"
		}
		return "-bold"
	}
	if italic {
		return "-italic"
	}
	return ""
}

// Build once per fragment, not once per character. Each configured family
// contributes its selected variant (or regular), with duplicate faces removed.
func textFontCandidates(style PathStyle) []*sfnt.Font {
	fontRegistryMu.RLock()
	defer fontRegistryMu.RUnlock()
	suffix := fontSuffix(style.FontWeight, style.FontStyle)
	var out []*sfnt.Font
	add := func(name string) {
		name = strings.ToLower(strings.Trim(strings.TrimSpace(name), "'\""))
		f := fontRegistry[name+suffix]
		if f == nil {
			f = fontRegistry[name]
		}
		if f == nil {
			return
		}
		for _, old := range out {
			if old == f {
				return
			}
		}
		out = append(out, f)
	}
	names := strings.Split(style.FontFamily, ",")
	if len(names) > 16 {
		names = names[:16]
	}
	for _, name := range names {
		add(name)
	}
	if len(out) == 0 {
		add("sans-serif")
	}
	for _, name := range textFontOptions.FallbackFamilies {
		add(name)
	}
	add("default")
	add("go mono")
	return out
}
func clusterFont(fonts []*sfnt.Font, cluster []rune, buf *sfnt.Buffer) *sfnt.Font {
	if len(fonts) == 0 {
		return nil
	}
	base := fonts[0]
	baseFound := false
	for _, f := range fonts {
		all := true
		for i, r := range cluster {
			idx, err := f.GlyphIndex(buf, r)
			covered := err == nil && idx != 0
			if i == 0 && covered && !baseFound {
				base = f
				baseFound = true
			}
			if !covered {
				all = false
			}
		}
		if all {
			return f
		}
	}
	return base
}
func combining(r rune) bool { return unicode.Is(unicode.M, r) }

// TextStyle is the font selection used by MeasureText and SVG text layout.
type TextStyle struct {
	Family, Weight, Style string
	Size                  float64
}
type TextMetrics struct {
	Advance, Ascent, Descent, XHeight float64
	MissingGlyphs                     int
}

// MeasureText measures a single unnormalized text run with the same fallback,
// kerning and glyph advances as SVG layout. It does not shape complex scripts.
func MeasureText(text string, style TextStyle) TextMetrics {
	loadTextSystemFonts()
	ps := DefaultStyle
	if style.Family != "" {
		ps.FontFamily = style.Family
	}
	ps.FontWeight = style.Weight
	ps.FontStyle = style.Style
	ps.FontSize = style.Size
	c := &IconCursor{}
	var buf sfnt.Buffer
	var result TextMetrics
	result.Advance, _ = c.walkTextGlyphs([]textFragment{{text: text, style: ps}}, 0, 0, 0, &buf, func(g glyphVisit) {
		if g.idx == 0 {
			result.MissingGlyphs++
		}
	})
	if fonts := textFontCandidates(ps); len(fonts) > 0 {
		if m, err := fonts[0].Metrics(&buf, fragmentPPEM(ps.FontSize), font.HintingNone); err == nil {
			result.Ascent = float64(m.Ascent) / 64
			result.Descent = float64(m.Descent) / 64
			result.XHeight = float64(m.XHeight) / 64
		}
	}
	return result
}
func baselineOffset(style PathStyle, f *sfnt.Font, buf *sfnt.Buffer) float64 {
	if style.DominantBaseline == "" || style.DominantBaseline == "auto" || style.DominantBaseline == "alphabetic" {
		return 0
	}
	m, err := f.Metrics(buf, fragmentPPEM(style.FontSize), font.HintingNone)
	if err != nil {
		return 0
	}
	switch style.DominantBaseline {
	case "middle":
		return float64(m.XHeight) / 128
	case "central":
		return float64(m.Ascent-m.Descent) / 128
	case "text-before-edge", "before-edge":
		return float64(m.Ascent) / 64
	case "text-after-edge", "after-edge":
		return -float64(m.Descent) / 64
	}
	return 0
}

// RegisterFontAlias maps an alias (including a CSS generic family) to an already
// registered family and its variants. It does not copy or parse font data.
func RegisterFontAlias(alias, family string) error {
	alias = strings.ToLower(strings.TrimSpace(alias))
	family = strings.ToLower(strings.TrimSpace(family))
	fontRegistryMu.Lock()
	defer fontRegistryMu.Unlock()
	if alias == "" || fontRegistry[family] == nil {
		return fmt.Errorf("unknown font family %q", family)
	}
	for _, suffix := range []string{"", "-bold", "-italic", "-bolditalic"} {
		name := family + suffix
		f := fontRegistry[name]
		if f == nil {
			name = family
			f = fontRegistry[name]
		}
		fontRegistry[alias+suffix] = f
		fontBytesRegistry[alias+suffix] = fontBytesRegistry[name]
		fontIndexRegistry[alias+suffix] = fontIndexRegistry[name]
	}
	return nil
}
