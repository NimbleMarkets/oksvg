package oksvg

import (
	"encoding/binary"
	"errors"

	"golang.org/x/image/font/sfnt"
)

// parseSBIX searches the font bytes for the 'sbix' table and extracts
// the raw PNG bytes, origin offsets, and native design size (maxPPEM) for a given glyph index.
func parseSBIX(fontBytes []byte, fontIndex int, glyphIndex int) (pngBytes []byte, originX, originY int, maxPPEM int, err error) {
	if len(fontBytes) < 12 {
		return nil, 0, 0, 0, errors.New("font data too short")
	}

	offset := 0
	// Check if TTC
	magic := binary.BigEndian.Uint32(fontBytes[0:4])
	if magic == 0x74746366 { // 'ttcf'
		numFonts := binary.BigEndian.Uint32(fontBytes[8:12])
		if fontIndex < 0 || fontIndex >= int(numFonts) {
			return nil, 0, 0, 0, errors.New("font index out of range")
		}
		offset = int(binary.BigEndian.Uint32(fontBytes[12+fontIndex*4 : 16+fontIndex*4]))
	}

	if offset < 0 || offset+12 > len(fontBytes) {
		return nil, 0, 0, 0, errors.New("invalid offset table")
	}

	numTables := binary.BigEndian.Uint16(fontBytes[offset+4 : offset+6])
	tableOffset := offset + 12
	sbixOffset := 0
	sbixLength := 0

	for i := 0; i < int(numTables); i++ {
		if tableOffset+16 > len(fontBytes) {
			break
		}
		tag := binary.BigEndian.Uint32(fontBytes[tableOffset : tableOffset+4])
		if tag == 0x73626978 { // 'sbix'
			sbixOffset = int(binary.BigEndian.Uint32(fontBytes[tableOffset+8 : tableOffset+12]))
			sbixLength = int(binary.BigEndian.Uint32(fontBytes[tableOffset+12 : tableOffset+16]))
			break
		}
		tableOffset += 16
	}

	if sbixOffset == 0 || sbixLength == 0 {
		return nil, 0, 0, 0, errors.New("sbix table not found")
	}

	if sbixOffset+8 > len(fontBytes) {
		return nil, 0, 0, 0, errors.New("invalid sbix table offset")
	}

	version := binary.BigEndian.Uint16(fontBytes[sbixOffset : sbixOffset+2])
	if version != 1 {
		return nil, 0, 0, 0, errors.New("unsupported sbix table version")
	}

	numSizes := binary.BigEndian.Uint32(fontBytes[sbixOffset+4 : sbixOffset+8])
	if sbixOffset+8+int(numSizes)*4 > len(fontBytes) {
		return nil, 0, 0, 0, errors.New("invalid sbix numSizes")
	}

	// Find the largest size (highest resolution) available in the sbix table
	var bestSizeOffset int
	var maxPPEMVal int

	for i := 0; i < int(numSizes); i++ {
		sizeOffset := int(binary.BigEndian.Uint32(fontBytes[sbixOffset+8+i*4 : sbixOffset+12+i*4]))
		absSizeOffset := sbixOffset + sizeOffset
		if absSizeOffset+4 > len(fontBytes) {
			continue
		}
		ppem := int(binary.BigEndian.Uint16(fontBytes[absSizeOffset : absSizeOffset+2]))
		if ppem > maxPPEMVal {
			maxPPEMVal = ppem
			bestSizeOffset = absSizeOffset
		}
	}

	if bestSizeOffset == 0 {
		return nil, 0, 0, 0, errors.New("no valid size found in sbix")
	}

	// Offset of glyphDataOffsets array is bestSizeOffset + 4
	glyphOffsetField := bestSizeOffset + 4 + glyphIndex*4
	if glyphOffsetField+8 > len(fontBytes) {
		return nil, 0, 0, 0, errors.New("glyph index out of range for sbix size")
	}

	glyphStartOffset := int(binary.BigEndian.Uint32(fontBytes[glyphOffsetField : glyphOffsetField+4]))
	glyphEndOffset := int(binary.BigEndian.Uint32(fontBytes[glyphOffsetField+4 : glyphOffsetField+8]))

	if glyphStartOffset == glyphEndOffset {
		return nil, 0, 0, 0, errors.New("empty glyph data")
	}

	absStart := bestSizeOffset + glyphStartOffset
	absEnd := bestSizeOffset + glyphEndOffset

	if absStart+8 > len(fontBytes) || absEnd > len(fontBytes) || absStart > absEnd {
		return nil, 0, 0, 0, errors.New("invalid glyph offset bounds")
	}

	originOffsetX := int(int16(binary.BigEndian.Uint16(fontBytes[absStart : absStart+2])))
	originOffsetY := int(int16(binary.BigEndian.Uint16(fontBytes[absStart+2 : absStart+4])))

	graphicType := binary.BigEndian.Uint32(fontBytes[absStart+4 : absStart+8])
	if graphicType != 0x706e6720 { // 'png '
		return nil, 0, 0, 0, errors.New("unsupported graphic type (not png)")
	}

	return fontBytes[absStart+8 : absEnd], originOffsetX, originOffsetY, maxPPEMVal, nil
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
