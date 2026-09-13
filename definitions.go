// Copyright 2017 The oksvg Authors. All rights reserved.
// created: 2/12/2017 by S.R.Wiley
//
// definitions.go defines the types used to collect SVG <defs> content for later
// replay by <use> and pattern references.

package oksvg

import (
	"encoding/xml"
	"fmt"
)

// Count stored copies, not source elements: nested IDs append each descendant
// to every ancestor's replay list, otherwise amplifying a small input quadratically.
const maxDefinitionEntries = 100000

func (c *IconCursor) reserveDefinitions(n int) (bool, error) {
	if c.definitionBudgetSpent {
		return false, nil
	}
	if n > maxDefinitionEntries-c.definitionEntries {
		c.definitionBudgetSpent = true
		// Incomplete collectors cannot be replayed safely. Keep only definitions
		// that closed before exhaustion, and stop collecting for this document.
		c.openDefs = nil
		c.defDepth = 0
		msg := fmt.Sprintf("definition storage exceeds budget of %d entries", maxDefinitionEntries)
		if c.returnError(msg) {
			return false, fmt.Errorf("%s", msg)
		}
		return false, nil
	}
	c.definitionEntries += n
	return true, nil
}

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
