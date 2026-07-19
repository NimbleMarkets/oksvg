//go:build !darwin && !windows && !linux

// icon_cursor_other.go supplies empty system/emoji font tables for platforms
// without a known set of font locations (e.g. js/wasm). Flag-ligature resolution
// is the shared no-op in icon_cursor_flags_stub.go.

package oksvg

var systemFonts = []systemFontInfo{}

var emojiFonts = []emojiFontInfo{}
