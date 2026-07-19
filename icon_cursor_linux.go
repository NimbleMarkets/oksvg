//go:build linux

// icon_cursor_linux.go lists common Linux system/emoji font locations.
// Flag-ligature resolution is the shared no-op in icon_cursor_flags_stub.go.

package oksvg

var systemFonts = []systemFontInfo{
	{"arial", "/usr/share/fonts/truetype/msttcorefonts/Arial.ttf"},
	{"arial-bold", "/usr/share/fonts/truetype/msttcorefonts/Arial_Bold.ttf"},
	{"arial black", "/usr/share/fonts/truetype/msttcorefonts/Arial_Black.ttf"},
	{"arial black-bold", "/usr/share/fonts/truetype/msttcorefonts/Arial_Black.ttf"},
	{"impact", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf"},
	{"impact-bold", "/usr/share/fonts/truetype/msttcorefonts/Impact.ttf"},
}

var emojiFonts = []emojiFontInfo{
	{"emoji", "/usr/share/fonts/truetype/noto/NotoColorEmoji.ttf", false},
}
