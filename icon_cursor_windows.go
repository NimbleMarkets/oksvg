//go:build windows

package oksvg

import "golang.org/x/image/font/sfnt"

var systemFonts = []struct {
	name string
	path string
}{
	{"arial", "C:\\Windows\\Fonts\\arial.ttf"},
	{"arial-bold", "C:\\Windows\\Fonts\\arialbd.ttf"},
	{"arial black", "C:\\Windows\\Fonts\\ariblk.ttf"},
	{"arial black-bold", "C:\\Windows\\Fonts\\ariblk.ttf"},
	{"impact", "C:\\Windows\\Fonts\\impact.ttf"},
	{"impact-bold", "C:\\Windows\\Fonts\\impact.ttf"},
}

var emojiFonts = []struct {
	name string
	path string
	coll bool
}{
	{"emoji", "C:\\Windows\\Fonts\\seguiemj.ttf", false},
}

func resolveFlagGlyph(code string, emojiFont *sfnt.Font, buf *sfnt.Buffer) (sfnt.GlyphIndex, bool) {
	return 0, false
}
