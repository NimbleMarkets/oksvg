package oksvg

import (
	"encoding/binary"
	"errors"
	// reflectpkg: aliased because path_cursor.go already declares a
	// package-level function named reflect in this package.
	reflectpkg "reflect"
	"sync"

	"golang.org/x/image/font/sfnt"
)

const (
	tagTTCF = 0x74746366 // 'ttcf' collection header tag
	tagSBIX = 0x73626978 // 'sbix' table tag
	tagPNG  = 0x706e6720 // 'png ' graphic type tag
)

// sbixStrike caches the location of the best (highest-PPEM) bitmap strike
// found in a font's sbix table, plus the end of the sbix table itself so
// every per-glyph offset can be validated to stay within it.
type sbixStrike struct {
	bestSizeOffset int
	maxPPEM        int
	sbixEnd        int
}

// sbixCacheKey indexes the sbix strike cache. dataPtr is used only as a fast
// hash bucket, never dereferenced; the authoritative identity check compares the
// cache entry's pinned slice against the caller's slice on a hit (see
// sbixCacheEntry and sbixStrikeForFont).
type sbixCacheKey struct {
	dataPtr   uintptr
	dataLen   int
	fontIndex int
}

// sbixCacheEntry stores a resolved strike (nil == no usable strike) and the
// error alongside the []byte it was resolved from.
//
// Storing the slice PINS its backing array for as long as the entry lives, so
// that array's address can never be reused by a later allocation. This closes
// the GC-unsoundness of keying purely on a uintptr: a freed font's address could
// otherwise be reused by a *different* font, whose bytes would then collide with
// the stale key and be painted with the first font's strike offsets. Stale
// entries for replaced fonts become dead-but-safe memory, bounded by the number
// of distinct registrations (matching the fontBytesRegistry semantics).
type sbixCacheEntry struct {
	data   []byte
	strike *sbixStrike
	err    error
}

var (
	sbixCacheMu sync.Mutex
	// sbixCache maps a font's identity + collection index to its resolved
	// sbix strike location. A nil strike means "no usable sbix strike was
	// found for this font", so glyph lookups against fonts without bitmap
	// glyphs cost one table-directory walk in total rather than one per
	// glyph.
	sbixCache = map[sbixCacheKey]sbixCacheEntry{}
)

var errSBIXUnavailable = errors.New("sbix table not found")

// slicePtr returns the address of a slice's backing array (0 for an empty
// slice), used both to build cache keys and to verify byte identity on a hit.
func slicePtr(b []byte) uintptr {
	if len(b) == 0 {
		return 0
	}
	return reflectpkg.ValueOf(b).Pointer()
}

func sbixCacheKeyFor(fontBytes []byte, fontIndex int) sbixCacheKey {
	return sbixCacheKey{dataPtr: slicePtr(fontBytes), dataLen: len(fontBytes), fontIndex: fontIndex}
}

// sbixStrikeForFont resolves (and caches) the best bitmap strike for
// fontBytes/fontIndex, so repeated glyph lookups against the same font pay
// the sfnt table-directory walk only once.
func sbixStrikeForFont(fontBytes []byte, fontIndex int) (*sbixStrike, error) {
	key := sbixCacheKeyFor(fontBytes, fontIndex)

	sbixCacheMu.Lock()
	entry, cached := sbixCache[key]
	sbixCacheMu.Unlock()
	// On a hit, confirm the cached entry was resolved from THIS exact slice
	// (same backing array pointer and length). Because the entry pins that
	// array, a matching pointer guarantees identity; any mismatch forces a fresh
	// parse rather than reusing a strike belonging to different bytes.
	if cached && len(entry.data) == len(fontBytes) && slicePtr(entry.data) == slicePtr(fontBytes) {
		return entry.strike, entry.err
	}

	strike, err := findSBIXStrike(fontBytes, fontIndex)

	sbixCacheMu.Lock()
	// Store fontBytes to pin its backing array; negative results are cached too.
	sbixCache[key] = sbixCacheEntry{data: fontBytes, strike: strike, err: err}
	sbixCacheMu.Unlock()

	return strike, err
}

// findSBIXStrike walks the sfnt table directory (following a TTC collection
// header, if present) to find fontBytes' 'sbix' table, then picks the
// highest-PPEM strike within it. All offset arithmetic is done in int64 and
// validated against the actual buffer length (and, once known, against the
// declared bounds of the sbix table itself) before ever being used as a
// slice index, so malformed or truncated input is rejected with an error
// instead of panicking.
func findSBIXStrike(fontBytes []byte, fontIndex int) (*sbixStrike, error) {
	if len(fontBytes) < 12 {
		return nil, errors.New("font data too short")
	}
	fontLen := int64(len(fontBytes))

	var offset int64
	magic := binary.BigEndian.Uint32(fontBytes[0:4])
	if magic == tagTTCF {
		numFonts := binary.BigEndian.Uint32(fontBytes[8:12])
		dirEnd := int64(12) + int64(numFonts)*4
		if dirEnd > fontLen {
			return nil, errors.New("truncated ttc font directory")
		}
		if fontIndex < 0 || int64(fontIndex) >= int64(numFonts) {
			return nil, errors.New("font index out of range")
		}
		entryOff := int64(12) + int64(fontIndex)*4
		offset = int64(binary.BigEndian.Uint32(fontBytes[entryOff : entryOff+4]))
	}

	if offset < 0 || offset+12 > fontLen {
		return nil, errors.New("invalid offset table")
	}
	off := int(offset)

	numTables := binary.BigEndian.Uint16(fontBytes[off+4 : off+6])
	tableOffset := offset + 12
	var sbixOffset, sbixLength int64

	for i := 0; i < int(numTables); i++ {
		if tableOffset+16 > fontLen {
			break
		}
		to := int(tableOffset)
		tag := binary.BigEndian.Uint32(fontBytes[to : to+4])
		if tag == tagSBIX {
			sbixOffset = int64(binary.BigEndian.Uint32(fontBytes[to+8 : to+12]))
			sbixLength = int64(binary.BigEndian.Uint32(fontBytes[to+12 : to+16]))
			break
		}
		tableOffset += 16
	}

	if sbixOffset <= 0 || sbixLength <= 0 {
		return nil, errSBIXUnavailable
	}
	sbixEnd := sbixOffset + sbixLength
	if sbixOffset+8 > fontLen || sbixEnd > fontLen {
		return nil, errors.New("invalid sbix table bounds")
	}

	so := int(sbixOffset)
	version := binary.BigEndian.Uint16(fontBytes[so : so+2])
	if version != 1 {
		return nil, errors.New("unsupported sbix table version")
	}

	numSizes := binary.BigEndian.Uint32(fontBytes[so+4 : so+8])
	sizesEnd := sbixOffset + 8 + int64(numSizes)*4
	if sizesEnd < 0 || sizesEnd > sbixEnd {
		return nil, errors.New("invalid sbix numSizes")
	}

	// Find the largest size (highest resolution) strike in the sbix table.
	var bestSizeOffset int64 = -1
	var maxPPEMVal int

	for i := 0; i < int(numSizes); i++ {
		entryOff := int(sbixOffset + 8 + int64(i)*4)
		sizeOffset := int64(binary.BigEndian.Uint32(fontBytes[entryOff : entryOff+4]))
		absSizeOffset := sbixOffset + sizeOffset
		if absSizeOffset+4 > sbixEnd {
			continue
		}
		aso := int(absSizeOffset)
		ppem := int(binary.BigEndian.Uint16(fontBytes[aso : aso+2]))
		if ppem > maxPPEMVal {
			maxPPEMVal = ppem
			bestSizeOffset = absSizeOffset
		}
	}

	if bestSizeOffset < 0 {
		return nil, errors.New("no valid size found in sbix")
	}

	return &sbixStrike{
		bestSizeOffset: int(bestSizeOffset),
		maxPPEM:        maxPPEMVal,
		sbixEnd:        int(sbixEnd),
	}, nil
}

// parseSBIX searches the font bytes for the 'sbix' table and extracts the
// raw PNG bytes, origin offsets, and native design size (maxPPEM) for a
// given glyph index. It never panics on malformed or truncated input;
// every failure mode returns a non-nil error instead.
func parseSBIX(fontBytes []byte, fontIndex int, glyphIndex int) (pngBytes []byte, originX, originY int, maxPPEM int, err error) {
	if glyphIndex < 0 {
		return nil, 0, 0, 0, errors.New("invalid glyph index")
	}

	strike, err := sbixStrikeForFont(fontBytes, fontIndex)
	if err != nil {
		return nil, 0, 0, 0, err
	}

	bestSizeOffset := int64(strike.bestSizeOffset)
	sbixEnd := int64(strike.sbixEnd)

	// Offset of the glyphDataOffsets array is bestSizeOffset + 4; requiring
	// glyphOffsetField+8 <= sbixEnd both bounds-checks the two uint32 reads
	// below and implicitly caps glyphIndex at the strike's glyph count.
	glyphOffsetField := bestSizeOffset + 4 + int64(glyphIndex)*4
	if glyphOffsetField+8 > sbixEnd {
		return nil, 0, 0, 0, errors.New("glyph index out of range for sbix size")
	}
	gof := int(glyphOffsetField)
	glyphStartOffset := int64(binary.BigEndian.Uint32(fontBytes[gof : gof+4]))
	glyphEndOffset := int64(binary.BigEndian.Uint32(fontBytes[gof+4 : gof+8]))

	if glyphStartOffset == glyphEndOffset {
		return nil, 0, 0, 0, errors.New("empty glyph data")
	}

	absStart := bestSizeOffset + glyphStartOffset
	absEnd := bestSizeOffset + glyphEndOffset

	// absStart+8 <= absEnd guarantees the origin+graphicType header (8
	// bytes) and the final fontBytes[absStart+8:absEnd] slice are both
	// well-formed; a glyph record shorter than 8 bytes (as seen with real
	// malformed sbix fonts) is rejected here instead of panicking with
	// "slice bounds out of range" further down. absEnd <= sbixEnd keeps the
	// glyph data within the sbix table's own declared bounds.
	if absStart < 0 || absStart+8 > absEnd || absEnd > sbixEnd {
		return nil, 0, 0, 0, errors.New("invalid glyph offset bounds")
	}

	as := int(absStart)
	originOffsetX := int(int16(binary.BigEndian.Uint16(fontBytes[as : as+2])))
	originOffsetY := int(int16(binary.BigEndian.Uint16(fontBytes[as+2 : as+4])))

	graphicType := binary.BigEndian.Uint32(fontBytes[as+4 : as+8])
	if graphicType != tagPNG {
		return nil, 0, 0, 0, errors.New("unsupported graphic type (not png)")
	}

	ae := int(absEnd)
	return fontBytes[as+8 : ae], originOffsetX, originOffsetY, strike.maxPPEM, nil
}

// findFontBytesAndIndex searches the fontRegistry for the given font pointer
// and returns its registered raw bytes and TTC collection index.
func findFontBytesAndIndex(f *sfnt.Font) ([]byte, int) {
	fontRegistryMu.RLock()
	defer fontRegistryMu.RUnlock()
	for name, registeredFont := range fontRegistry {
		if registeredFont == f {
			return fontBytesRegistry[name], fontIndexRegistry[name]
		}
	}
	return nil, 0
}
