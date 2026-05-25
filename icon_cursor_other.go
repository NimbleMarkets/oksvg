//go:build !darwin && !windows && !linux

package oksvg

import "golang.org/x/image/font/sfnt"

var systemFonts = []struct {
	name string
	path string
}{}

var emojiFonts = []struct {
	name string
	path string
	coll bool
}{}

func resolveFlagGlyph(code string, emojiFont *sfnt.Font, buf *sfnt.Buffer) (sfnt.GlyphIndex, bool) {
	return 0, false
}
