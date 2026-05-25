//go:build linux

package oksvg

import "golang.org/x/image/font/sfnt"

var systemFonts = []struct {
	name string
	path string
}{
	{"arial", "/usr/share/fonts/truetype/msttcorefonts/Arial.ttf"},
	{"arial-bold", "/usr/share/fonts/truetype/msttcorefonts/Arial_Bold.ttf"},
	{"arial black", "/usr/share/fonts/truetype/msttcorefonts/Arial_Black.ttf"},
	{"arial black-bold", "/usr/share/fonts/truetype/msttcorefonts/Arial_Black.ttf"},
	{"impact", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf"},
	{"impact-bold", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf"},
}

var emojiFonts = []struct {
	name string
	path string
	coll bool
}{
	{"emoji", "/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf", false},
}

func resolveFlagGlyph(code string, emojiFont *sfnt.Font, buf *sfnt.Buffer) (sfnt.GlyphIndex, bool) {
	return 0, false
}
