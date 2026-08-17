package cardigann

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"gopkg.in/yaml.v3"
)

type caseRule struct {
	Match string
	Value string
}

type caseBlock []caseRule

func (rules *caseBlock) UnmarshalYAML(node *yaml.Node) error {
	if node == nil || node.Kind == 0 {
		return nil
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("field case must be a mapping")
	}
	if len(node.Content)/2 > 64 {
		return fmt.Errorf("field case has too many rules")
	}
	out := make(caseBlock, 0, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || value.Kind != yaml.ScalarNode {
			return fmt.Errorf("field case rules must use scalar keys and values")
		}
		out = append(out, caseRule{Match: key.Value, Value: value.Value})
	}
	*rules = out
	return nil
}

func scalarCaseValue(raw string, rules caseBlock) (string, bool) {
	fallback := ""
	hasFallback := false
	for _, rule := range rules {
		match := strings.TrimSpace(rule.Match)
		if match == "*" {
			fallback, hasFallback = rule.Value, true
			continue
		}
		if raw == match || caseContains(match, raw) {
			return rule.Value, true
		}
	}
	return fallback, hasFallback
}

func htmlCaseValue(row, found *goquery.Selection, raw string, rules caseBlock) (string, bool, error) {
	fallback := ""
	hasFallback := false
	for _, rule := range rules {
		match := strings.TrimSpace(rule.Match)
		if match == "*" {
			fallback, hasFallback = rule.Value, true
			continue
		}
		if raw == match || caseContains(match, raw) {
			return rule.Value, true, nil
		}
		matcher, err := cascadia.Compile(match)
		if err != nil {
			continue
		}
		if found != nil && (found.IsMatcher(matcher) || found.FindMatcher(matcher).Length() > 0) {
			return rule.Value, true, nil
		}
		if row != nil && (row.IsMatcher(matcher) || row.FindMatcher(matcher).Length() > 0) {
			return rule.Value, true, nil
		}
	}
	return fallback, hasFallback, nil
}

func caseContains(match, raw string) bool {
	const prefix = ":contains("
	if !strings.HasPrefix(match, prefix) || !strings.HasSuffix(match, ")") {
		return false
	}
	argument := strings.TrimSpace(match[len(prefix) : len(match)-1])
	if len(argument) < 2 || argument[0] != argument[len(argument)-1] || argument[0] != '\'' && argument[0] != '"' {
		return false
	}
	return strings.Contains(raw, argument[1:len(argument)-1])
}
