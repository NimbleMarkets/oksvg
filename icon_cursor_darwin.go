//go:build darwin

package oksvg

import (
	"strings"

	"golang.org/x/image/font/sfnt"
)

var systemFonts = []struct {
	name string
	path string
}{
	{"arial", "/System/Library/Fonts/Supplemental/Arial.ttf"},
	{"arial-bold", "/System/Library/Fonts/Supplemental/Arial Bold.ttf"},
	{"arial black", "/System/Library/Fonts/Supplemental/Arial Black.ttf"},
	{"arial black-bold", "/System/Library/Fonts/Supplemental/Arial Black.ttf"},
	{"impact", "/System/Library/Fonts/Supplemental/Impact.ttf"},
	{"impact-bold", "/System/Library/Fonts/Supplemental/Impact.ttf"},
}

var emojiFonts = []struct {
	name string
	path string
	coll bool
}{
	{"emoji", "/System/Library/Fonts/Apple Color Emoji.ttc", true},
	{"symbols", "/System/Library/Fonts/Apple Symbols.ttf", false},
}

func resolveFlagGlyph(code string, emojiFont *sfnt.Font, buf *sfnt.Buffer) (sfnt.GlyphIndex, bool) {
	if emojiFont != nil && isAppleColorEmoji(emojiFont, buf) {
		if gIdx, ok := emojiFlagGlyphs[code]; ok {
			return sfnt.GlyphIndex(gIdx), true
		}
	}
	return 0, false
}

func isAppleColorEmoji(f *sfnt.Font, buf *sfnt.Buffer) bool {
	name, err := f.Name(buf, sfnt.NameID(1))
	return err == nil && strings.Contains(name, "Apple Color Emoji")
}
