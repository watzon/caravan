package cardigann

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxYAMLDepth = 64
	maxYAMLNodes = 10000
)

func validateYAMLSource(data []byte) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return err
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("definition must contain exactly one YAML document")
	}
	count := 0
	return validateYAMLGraph(&document, 0, &count)
}

func validateYAMLGraph(node *yaml.Node, depth int, count *int) error {
	if node == nil {
		return nil
	}
	(*count)++
	if depth > maxYAMLDepth || *count > maxYAMLNodes {
		return fmt.Errorf("definition YAML exceeds structural limits")
	}
	if node.Kind == yaml.AliasNode || node.Anchor != "" {
		return fmt.Errorf("definition YAML aliases and anchors are not supported")
	}
	if strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!") {
		return fmt.Errorf("definition YAML custom tags are not supported")
	}
	for _, child := range node.Content {
		if err := validateYAMLGraph(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}
