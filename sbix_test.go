// Copyright 2018 The oksvg Authors. All rights reserved.
package oksvg

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"strings"
	"testing"
)

// --- synthetic sbix font builders -----------------------------------------
//
// These helpers build the smallest possible byte tables that satisfy the
// parts of the sfnt/sbix format parseSBIX actually reads, without needing a
// real font file. Layout (non-collection case):
//
//	[0:12]   sfnt offset table (version, numTables, ...)
//	[12:28]  one table directory entry for 'sbix'
//	[28:...] the sbix table itself: version, flags, numStrikes,
//	         strikeOffsets[numStrikes], then for the one strike:
//	         ppem, resolution, glyphDataOffsets[numGlyphs+1], glyph records
//
// Each glyph record is originX(2) + originY(2) + graphicType(4) + data.

type sbixGlyphSpec struct {
	pngData          []byte
	originX, originY int16
}

func concatBytes(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// buildSBIXFont builds a minimal non-collection font with a single sbix
// strike at ppem containing len(glyphs) glyphs (glyph index == slice
// index). If shrinkLastRecordBy is nonzero, the *declared* length of the
// last glyph's data record (via its glyphDataOffsets entry) is reduced by
// that many bytes without shrinking the physical buffer. That reproduces a
// font that passes the coarse "does this offset fit in the file" checks but
// whose per-glyph record is only a few bytes long -- the shape of input
// that previously panicked with a "slice bounds out of range" error.
//
// baseOffset is the absolute byte position this font's own byte 0 will
// occupy in the buffer it ends up embedded in (0 for standalone use, or
// the TTC header length when embedding via wrapAsTTC): per the sfnt spec,
// table-directory offsets (unlike the sbix-internal ones) are absolute
// from the start of the file, so they must be adjusted for embedding.
func buildSBIXFont(ppem uint16, glyphs []sbixGlyphSpec, shrinkLastRecordBy int, baseOffset uint32) []byte {
	const glyphRecordHeaderLen = 8 // originX(2) + originY(2) + graphicType(4)

	numGlyphs := len(glyphs)
	glyphOffsets := make([]uint32, numGlyphs+1)
	// glyphDataOffsets values are measured from the start of the strike
	// (i.e. from the ppem field), so they must include the fixed 4-byte
	// strike header plus the glyphDataOffsets array itself.
	prefixLen := uint32(4 + (numGlyphs+1)*4)
	var records []byte
	for i, g := range glyphs {
		glyphOffsets[i] = prefixLen + uint32(len(records))
		rec := make([]byte, glyphRecordHeaderLen+len(g.pngData))
		binary.BigEndian.PutUint16(rec[0:2], uint16(g.originX))
		binary.BigEndian.PutUint16(rec[2:4], uint16(g.originY))
		binary.BigEndian.PutUint32(rec[4:8], tagPNG)
		copy(rec[8:], g.pngData)
		records = concatBytes(records, rec)
	}
	glyphOffsets[numGlyphs] = prefixLen + uint32(len(records))
	if shrinkLastRecordBy != 0 {
		glyphOffsets[numGlyphs] -= uint32(shrinkLastRecordBy)
	}

	strikeHeader := make([]byte, 4) // ppem(2) + resolution(2)
	binary.BigEndian.PutUint16(strikeHeader[0:2], ppem)
	binary.BigEndian.PutUint16(strikeHeader[2:4], 72)

	glyphOffsetsBuf := make([]byte, len(glyphOffsets)*4)
	for i, off := range glyphOffsets {
		binary.BigEndian.PutUint32(glyphOffsetsBuf[i*4:i*4+4], off)
	}

	strike := concatBytes(strikeHeader, glyphOffsetsBuf, records)

	sbixHeader := make([]byte, 8)
	binary.BigEndian.PutUint16(sbixHeader[0:2], 1) // version
	binary.BigEndian.PutUint16(sbixHeader[2:4], 0) // flags
	binary.BigEndian.PutUint32(sbixHeader[4:8], 1) // numStrikes

	strikeOffsets := make([]byte, 4)
	// strike starts right after this (single-entry) offsets array
	binary.BigEndian.PutUint32(strikeOffsets[0:4], uint32(len(sbixHeader)+4))

	sbix := concatBytes(sbixHeader, strikeOffsets, strike)

	head := make([]byte, 12+16) // offset table + one table directory entry
	binary.BigEndian.PutUint32(head[0:4], 0x00010000)
	binary.BigEndian.PutUint16(head[4:6], 1) // numTables

	sbixOffsetAbs := baseOffset + uint32(len(head))
	binary.BigEndian.PutUint32(head[12:16], tagSBIX)
	binary.BigEndian.PutUint32(head[16:20], 0) // checksum, unused by parseSBIX
	binary.BigEndian.PutUint32(head[20:24], sbixOffsetAbs)
	binary.BigEndian.PutUint32(head[24:28], uint32(len(sbix)))

	return concatBytes(head, sbix)
}

// ttcHeaderLen is the size of the TTC header wrapAsTTC prepends; callers
// that intend to wrap a font built by buildSBIXFont must build it with
// baseOffset=ttcHeaderLen so its internal absolute offsets stay correct.
const ttcHeaderLen = 16

// wrapAsTTC wraps inner (built with baseOffset=ttcHeaderLen) as the sole
// member of a one-font TTC collection.
func wrapAsTTC(inner []byte) []byte {
	head := make([]byte, ttcHeaderLen)
	binary.BigEndian.PutUint32(head[0:4], tagTTCF)
	binary.BigEndian.PutUint32(head[4:8], 0x00010000) // ttc version
	binary.BigEndian.PutUint32(head[8:12], 1)         // numFonts
	binary.BigEndian.PutUint32(head[12:16], uint32(len(head)))
	return concatBytes(head, inner)
}

// runNoPanic calls parseSBIX and fails the test (instead of crashing the
// whole test binary) if it panics.
func runNoPanic(t *testing.T, fontBytes []byte, fontIndex, glyphIndex int) (pngBytes []byte, originX, originY, maxPPEM int, err error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("parseSBIX panicked: %v", r)
		}
	}()
	return parseSBIX(fontBytes, fontIndex, glyphIndex)
}

// (a) glyph record 1-7 bytes long must error, not panic.
func TestParseSBIX_ShortGlyphRecordReturnsError(t *testing.T) {
	full := []sbixGlyphSpec{{pngData: []byte{0xDE, 0xAD, 0xBE, 0xEF}, originX: 1, originY: 2}}
	fullRecordLen := 8 + len(full[0].pngData) // header + data == 12
	shrinkTo4 := fullRecordLen - 4            // declared record length becomes 4 (in [1,7])

	buf := buildSBIXFont(64, full, shrinkTo4, 0)

	_, _, _, _, err := runNoPanic(t, buf, 0, 0)
	if err == nil {
		t.Fatalf("expected error for short glyph record, got nil")
	}
}

// (b) TTC claiming numFonts=1000 with only a 12-byte file must error.
func TestParseSBIX_TTCNumFontsOverflowReturnsError(t *testing.T) {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], tagTTCF)
	binary.BigEndian.PutUint32(buf[4:8], 0x00010000)
	binary.BigEndian.PutUint32(buf[8:12], 1000) // numFonts, file has no room for the directory

	_, _, _, _, err := runNoPanic(t, buf, 0, 0)
	if err == nil {
		t.Fatalf("expected error for truncated ttc font directory, got nil")
	}
}

// (c) A valid synthetic table returns the PNG bytes and correct origin/ppem.
func TestParseSBIX_ValidSyntheticTable(t *testing.T) {
	pngData := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xde, 0xad, 0xbe, 0xef}
	buf := buildSBIXFont(96, []sbixGlyphSpec{{pngData: pngData, originX: -3, originY: 7}}, 0, 0)

	png, ox, oy, ppem, err := runNoPanic(t, buf, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(png) != string(pngData) {
		t.Errorf("png bytes = %v, want %v", png, pngData)
	}
	if ox != -3 || oy != 7 {
		t.Errorf("origin = (%d,%d), want (-3,7)", ox, oy)
	}
	if ppem != 96 {
		t.Errorf("maxPPEM = %d, want 96", ppem)
	}
}

// Also confirm a valid font wrapped in a well-formed TTC collection parses
// correctly, so the TTC bounds hardening doesn't break legitimate input.
func TestParseSBIX_ValidTTCCollection(t *testing.T) {
	pngData := []byte{1, 2, 3, 4, 5, 6}
	inner := buildSBIXFont(40, []sbixGlyphSpec{{pngData: pngData, originX: 0, originY: 1}}, 0, ttcHeaderLen)
	buf := wrapAsTTC(inner)

	png, _, _, ppem, err := runNoPanic(t, buf, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(png) != string(pngData) {
		t.Errorf("png bytes = %v, want %v", png, pngData)
	}
	if ppem != 40 {
		t.Errorf("maxPPEM = %d, want 40", ppem)
	}
}

// (d) Truncating a valid table at every possible length must never panic.
func TestParseSBIX_TruncationNeverPanics(t *testing.T) {
	glyphs := []sbixGlyphSpec{
		{pngData: []byte{1, 2, 3, 4, 5}, originX: 1, originY: -1},
		{pngData: []byte{9, 9}, originX: 0, originY: 0},
	}
	full := buildSBIXFont(32, glyphs, 0, 0)
	ttc := wrapAsTTC(buildSBIXFont(32, glyphs, 0, ttcHeaderLen))

	for _, variant := range [][]byte{full, ttc} {
		for n := 0; n <= len(variant); n++ {
			truncated := variant[:n]
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("parseSBIX panicked at truncation length %d: %v", n, r)
					}
				}()
				parseSBIX(truncated, 0, 0)
				parseSBIX(truncated, 0, 1)
			}()
		}
	}
}

// The per-font strike cache must not change results, and must be keyed on
// font identity (not glyph index): looking up two different glyphs from the
// same font bytes should populate exactly one cache entry.
func TestParseSBIX_CacheSharedAcrossGlyphs(t *testing.T) {
	buf := buildSBIXFont(48, []sbixGlyphSpec{
		{pngData: []byte{1, 2, 3}, originX: 0, originY: 0},
		{pngData: []byte{4, 5, 6, 7}, originX: 1, originY: 1},
	}, 0, 0)

	sbixCacheMu.Lock()
	sbixCache = map[sbixCacheKey]sbixCacheEntry{}
	sbixCacheMu.Unlock()

	png0, _, _, ppem0, err0 := runNoPanic(t, buf, 0, 0)
	png1, _, _, ppem1, err1 := runNoPanic(t, buf, 0, 1)
	if err0 != nil || err1 != nil {
		t.Fatalf("unexpected errors: %v, %v", err0, err1)
	}
	if string(png0) != "\x01\x02\x03" || string(png1) != "\x04\x05\x06\x07" {
		t.Errorf("unexpected glyph payloads: %q, %q", png0, png1)
	}
	if ppem0 != 48 || ppem1 != 48 {
		t.Errorf("maxPPEM mismatch: %d, %d", ppem0, ppem1)
	}

	sbixCacheMu.Lock()
	n := len(sbixCache)
	sbixCacheMu.Unlock()
	if n != 1 {
		t.Errorf("expected exactly one cached strike entry for one font, got %d", n)
	}
}

// TestParseSBIX_CacheByteIdentityGuard reproduces the F11 hazard: the cache
// keyed on a bare uintptr, so a freed font's address reused by a DIFFERENT font
// would collide with the stale key and apply the first font's strike offsets to
// the second's bytes. The fix pins the bytes in the entry and verifies byte
// identity on a hit. This test simulates the collision by poisoning font96's
// cache slot with font48's (different) strike+bytes; the lookup must detect the
// mismatch and re-parse font96's own strike (ppem 96), not reuse the stale one.
func TestParseSBIX_CacheByteIdentityGuard(t *testing.T) {
	font48 := buildSBIXFont(48, []sbixGlyphSpec{{pngData: []byte{1, 2, 3}, originX: 0, originY: 0}}, 0, 0)
	font96 := buildSBIXFont(96, []sbixGlyphSpec{{pngData: []byte{9, 8, 7, 6}, originX: 0, originY: 0}}, 0, 0)

	stale, err := findSBIXStrike(font48, 0)
	if err != nil {
		t.Fatalf("build stale strike: %v", err)
	}

	key := sbixCacheKeyFor(font96, 0)
	sbixCacheMu.Lock()
	sbixCache = map[sbixCacheKey]sbixCacheEntry{
		key: {data: font48, strike: stale, err: nil},
	}
	sbixCacheMu.Unlock()

	_, _, _, ppem, err := runNoPanic(t, font96, 0, 0)
	if err != nil {
		t.Fatalf("unexpected error resolving font96: %v", err)
	}
	if ppem != 96 {
		t.Errorf("stale strike reused for a different font: got ppem %d, want 96 (font96's own strike)", ppem)
	}
}

// pngHeaderOnly returns the bytes of a PNG that declares a w x h IHDR but
// carries no image data: png.DecodeConfig reports the size, while png.Decode
// would allocate w*h*4 bytes for it before failing on the missing data.
func pngHeaderOnly(w, h uint32) []byte {
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 6 // RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	var out []byte
	out = append(out, 0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n')
	out = binary.BigEndian.AppendUint32(out, 13)
	out = append(out, chunk...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(chunk))
	return out
}

// encodedPNG returns a complete, valid w x h PNG.
func encodedPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestDecodeBitmapGlyphRejectsHugeDimensions: sbix PNG data comes from the
// registered font bytes, so the declared IHDR size is attacker-controlled. The
// size gate must fire before png.Decode allocates. Complete, valid PNGs just
// over the limit prove the gate (an unrestricted decoder accepts them), and the
// error text distinguishes the gate from a decode failure on the header-only
// case (an unrestricted decoder would reject that too, after allocating).
func TestDecodeBitmapGlyphRejectsHugeDimensions(t *testing.T) {
	wantGate := func(name string, img image.Image, err error) {
		t.Helper()
		if err == nil || img != nil {
			t.Fatalf("%s: got img=%v err=%v, want rejection", name, img, err)
		}
		if !strings.Contains(err.Error(), "exceeds") {
			t.Errorf("%s: rejected by %q, want the size gate, not the decoder", name, err)
		}
	}
	img, err := decodeBitmapGlyph(encodedPNG(t, maxBitmapGlyphSide+1, 1))
	wantGate("over-wide valid PNG", img, err)
	img, err = decodeBitmapGlyph(encodedPNG(t, 1, maxBitmapGlyphSide+1))
	wantGate("over-tall valid PNG", img, err)
	img, err = decodeBitmapGlyph(pngHeaderOnly(30000, 30000)) // 3.6 GB RGBA if decoded
	wantGate("huge header-only PNG", img, err)

	img, err = decodeBitmapGlyph(encodedPNG(t, maxBitmapGlyphSide, 1))
	if err != nil || img == nil || img.Bounds().Dx() != maxBitmapGlyphSide {
		t.Errorf("at-limit PNG: img=%v err=%v, want accepted", img, err)
	}
	img, err = decodeBitmapGlyph(encodedPNG(t, 4, 4))
	if err != nil || img == nil || img.Bounds().Dx() != 4 {
		t.Errorf("small valid PNG: img=%v err=%v, want 4x4 image", img, err)
	}
}

// TestBitmapGlyphCacheAndBudget: a glyph repeated in a document must decode
// once and share the image; distinct glyphs are charged against a per-document
// pixel budget, after which further bitmaps are refused (the caller then falls
// back to the vector outline) so a text run cannot allocate without bound.
func TestBitmapGlyphCacheAndBudget(t *testing.T) {
	c := &IconCursor{}
	c.bitmapPixelLimit = 2 * 16 * 16 // room for exactly two 16x16 glyphs
	pngA := encodedPNG(t, 16, 16)

	first, err := c.bitmapGlyph(nil, 7, pngA)
	if err != nil || first == nil {
		t.Fatalf("first decode: img=%v err=%v", first, err)
	}
	again, err := c.bitmapGlyph(nil, 7, pngA)
	if err != nil || again != first {
		t.Errorf("repeated glyph: img=%p err=%v, want the cached image %p", again, err, first)
	}
	if c.bitmapPixels != 16*16 {
		t.Errorf("pixels charged = %d after one distinct glyph, want %d", c.bitmapPixels, 16*16)
	}

	if _, err := c.bitmapGlyph(nil, 8, pngA); err != nil {
		t.Fatalf("second distinct glyph within budget: %v", err)
	}
	third, err := c.bitmapGlyph(nil, 9, pngA)
	if err == nil || third != nil {
		t.Errorf("third distinct glyph: img=%v err=%v, want budget refusal", third, err)
	}
	if c.bitmapPixels != 2*16*16 {
		t.Errorf("refused glyph was charged: pixels=%d", c.bitmapPixels)
	}
	// Cached glyphs stay available after the budget is spent.
	if img, err := c.bitmapGlyph(nil, 7, pngA); err != nil || img != first {
		t.Errorf("cached glyph after budget spent: img=%p err=%v, want %p", img, err, first)
	}
}
