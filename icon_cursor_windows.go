//go:build windows

// icon_cursor_windows.go lists common Windows system/emoji font locations.
// Flag-ligature resolution is the shared no-op in icon_cursor_flags_stub.go.

package oksvg

var systemFonts = []systemFontInfo{
	{"arial", "C:\\Windows\\Fonts\\arial.ttf"},
	{"arial-bold", "C:\\Windows\\Fonts\\arialbd.ttf"},
	{"arial black", "C:\\Windows\\Fonts\\ariblk.ttf"},
	{"arial black-bold", "C:\\Windows\\Fonts\\ariblk.ttf"},
	{"impact", "C:\\Windows\\Fonts\\impact.ttf"},
	{"impact-bold", "C:\\Windows\\Fonts\\impact.ttf"},
}

var emojiFonts = []emojiFontInfo{
	{"emoji", "C:\\Windows\\Fonts\\seguiemj.ttf", false},
}
