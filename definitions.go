// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// definitions.go defines the types used to collect SVG <defs> content for later
// replay by <use> and pattern references.

package oksvg

import (
	"encoding/xml"
)

// definition is used to store XML-tags of SVG source definitions data.
type definition struct {
	ID, Tag string
	Attrs   []xml.Attr
}

// defCollector accumulates the definitions that make up one ID'd element while
// <defs> is being parsed. openDepth is the parse depth at which the element
// started; the collector is flushed to SvgIcon.Defs when parsing returns to
// that depth. Because a nested ID'd element belongs to every ancestor's
// definition list as well as its own, each StartElement appends to every open
// collector, which keeps the flat replay lists depth-balanced.
type defCollector struct {
	id        string
	openDepth int
	defs      []definition
}
