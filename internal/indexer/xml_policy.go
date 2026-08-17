package indexer

import (
	"encoding/xml"
	"fmt"
	"strings"
)

const (
	maxXMLDepth = 64
	// maxXMLElements is sized for the 8 MiB body cap: an extended Torznab
	// item costs ~20 elements, so ordinary large feeds (500+ items) must
	// fit while element floods stay bounded.
	maxXMLElements = 400000
)

type boundedXMLTokenReader struct {
	source   xml.TokenReader
	depth    int
	elements int
}

func (r *boundedXMLTokenReader) Token() (xml.Token, error) {
	token, err := r.source.Token()
	if token == nil {
		return nil, err
	}
	switch value := token.(type) {
	case xml.Directive:
		return nil, fmt.Errorf("XML DTD and directives are not supported")
	case xml.ProcInst:
		if !strings.EqualFold(value.Target, "xml") {
			return nil, fmt.Errorf("XML processing instructions are not supported")
		}
	case xml.StartElement:
		r.depth++
		r.elements++
		if r.depth > maxXMLDepth || r.elements > maxXMLElements {
			return nil, fmt.Errorf("XML document exceeds parsing limits")
		}
	case xml.EndElement:
		if r.depth <= 0 {
			return nil, fmt.Errorf("XML element nesting is invalid")
		}
		r.depth--
	}
	return token, err
}
