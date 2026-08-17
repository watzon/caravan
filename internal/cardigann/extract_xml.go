package cardigann

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

const (
	maxXMLDepth    = 64
	maxXMLElements = 10000
)

type xmlNode struct {
	name     string
	attrs    map[string]string
	text     strings.Builder
	children []*xmlNode
}

func parseXMLDocument(data []byte) (*xmlNode, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var root *xmlNode
	stack := make([]*xmlNode, 0)
	elements := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("parse XML: %w", err)
		}
		switch token := token.(type) {
		case xml.Directive:
			return nil, fmt.Errorf("XML DTD and directives are not supported")
		case xml.ProcInst:
			if !strings.EqualFold(token.Target, "xml") {
				return nil, fmt.Errorf("XML processing instructions are not supported")
			}
		case xml.StartElement:
			elements++
			if elements > maxXMLElements || len(stack) >= maxXMLDepth {
				return nil, fmt.Errorf("XML document exceeds extraction limits")
			}
			node := &xmlNode{name: token.Name.Local, attrs: make(map[string]string, len(token.Attr))}
			for _, attr := range token.Attr {
				node.attrs[attr.Name.Local] = attr.Value
			}
			if len(stack) == 0 {
				if root != nil {
					return nil, fmt.Errorf("XML document has multiple roots")
				}
				root = node
			} else {
				stack[len(stack)-1].children = append(stack[len(stack)-1].children, node)
			}
			stack = append(stack, node)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write([]byte(token))
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].name != token.Name.Local {
				return nil, fmt.Errorf("XML element nesting is invalid")
			}
			stack = stack[:len(stack)-1]
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, fmt.Errorf("XML document is incomplete")
	}
	return root, nil
}

func xmlRows(root *xmlNode, selector string) []*xmlNode {
	return xmlSelect([]*xmlNode{root}, selector)
}

func xmlSelect(nodes []*xmlNode, selector string) []*xmlNode {
	selector = strings.ReplaceAll(strings.TrimSpace(selector), ">", ".")
	for _, part := range strings.Split(selector, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		next := make([]*xmlNode, 0)
		for _, node := range nodes {
			if node != nil && node.name == part {
				next = append(next, node)
				continue
			}
			if node == nil {
				continue
			}
			for _, child := range node.children {
				if child.name == part {
					next = append(next, child)
				}
			}
		}
		nodes = next
	}
	return nodes
}

func extractXMLField(row *xmlNode, field fieldBlock) (string, bool, error) {
	value := ""
	found := false
	if field.Text != "" {
		value, found = field.Text, true
	} else {
		nodes := xmlSelect([]*xmlNode{row}, field.Selector)
		if len(nodes) == 0 {
			return "", false, nil
		}
		value, found = nodes[0].text.String(), true
		if field.Attribute != "" {
			value, found = nodes[0].attrs[field.Attribute]
			if !found {
				return "", false, nil
			}
		}
	}
	if len(field.Case) > 0 {
		value, found = scalarCaseValue(strings.TrimSpace(value), field.Case)
		if !found {
			return "", false, nil
		}
	}
	if len(field.Case) > 0 && strings.Contains(value, "{{") {
		return strings.TrimSpace(value), true, nil
	}
	value, err := applyFilters(strings.TrimSpace(value), field.Filters)
	return value, true, err
}
