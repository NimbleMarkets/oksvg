//go:build !darwin

// icon_cursor_flags_stub.go provides the no-op regional-indicator (flag)
// ligature resolver for every platform except darwin. Only macOS ships the Apple
// Color Emoji font whose raw flag glyph indices oksvg knows (see
// icon_cursor_darwin.go); elsewhere regional-indicator pairs fall through to the
// normal per-rune fallback path.

package oksvg

import "golang.org/x/image/font/sfnt"

// resolveFlagGlyph never resolves a combined flag glyph off darwin. The
// parameters mirror the darwin implementation's contract: code is two uppercase
// ASCII letters and buf is a caller-owned scratch buffer.
func resolveFlagGlyph(code string, emojiFont *sfnt.Font, buf *sfnt.Buffer) (sfnt.GlyphIndex, bool) {
	return 0, false
}
