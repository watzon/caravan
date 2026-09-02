package cardigann

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

var hyphenatedTemplateLookup = regexp.MustCompile(`\.(Config|Result|Fields)\.([A-Za-z0-9_]+(?:-[A-Za-z0-9_-]+)?)`)

func normalizeDefinitionTemplate(source string) string {
	// Parentheses keep the rewritten lookup usable as one argument inside a
	// function call, e.g. {{ re_replace .Config.sort "_" "" }}.
	source = hyphenatedTemplateLookup.ReplaceAllString(source, `(index .$1 "$2")`)
	var out strings.Builder
	out.Grow(len(source) + 16)
	inAction := false
	depth := 0
	for offset := 0; offset < len(source); {
		if !inAction && strings.HasPrefix(source[offset:], "{{") {
			inAction = true
			depth = 0
			out.WriteString("{{")
			offset += 2
			continue
		}
		if inAction && strings.HasPrefix(source[offset:], "}}") {
			inAction = false
			out.WriteString("}}")
			offset += 2
			continue
		}
		// Upstream definitions occasionally carry a stray closing paren, which
		// their regex-based evaluator ignores. Dropping it keeps the site usable.
		if inAction && source[offset] == '(' {
			depth++
		}
		if inAction && source[offset] == ')' {
			if depth == 0 {
				offset++
				continue
			}
			depth--
		}
		if inAction && source[offset] == '"' {
			start := offset
			offset++
			var content strings.Builder
			closed := false
			for offset < len(source) {
				if source[offset] == '\\' && offset+1 < len(source) {
					content.WriteByte(source[offset])
					content.WriteByte(source[offset+1])
					offset += 2
					continue
				}
				if source[offset] == '"' {
					offset++
					closed = true
					break
				}
				content.WriteByte(source[offset])
				offset++
			}
			if !closed {
				out.WriteString(source[start:])
				break
			}
			out.WriteString(strconv.Quote(content.String()))
			continue
		}
		out.WriteByte(source[offset])
		offset++
	}
	return out.String()
}

func definitionTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"urlquery":   url.QueryEscape,
		"join":       templateJoin,
		"re_replace": templateRegexReplace,
	}
}

func templateRegexReplace(value, pattern, replacement string) (string, error) {
	if len(value) > maxRenderedTemplateBytes || len(pattern) == 0 || len(pattern) > 4096 || len(replacement) > 4096 {
		return "", fmt.Errorf("re_replace arguments exceed size limits")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("re_replace pattern: %w", err)
	}
	result := re.ReplaceAllString(value, replacement)
	if len(result) > maxRenderedTemplateBytes {
		return "", fmt.Errorf("re_replace result exceeds size limit")
	}
	return result, nil
}

func templateJoin(first, second any) (string, error) {
	var parts []string
	separator := ""
	switch value := first.(type) {
	case nil:
		return "", nil
	case string:
		// Go templates append a pipeline value as the last function argument:
		// `.Config.cat | join ","` arrives as join(",", "anime").
		scalar, ok := second.(string)
		if !ok {
			return "", fmt.Errorf("join scalar has unsupported type %T", second)
		}
		return scalar, nil
	case []string:
		parts = append([]string(nil), value...)
	case []int:
		parts = make([]string, len(value))
		for i, item := range value {
			parts[i] = strconv.Itoa(item)
		}
	default:
		return "", fmt.Errorf("join value has unsupported type %T", first)
	}
	var ok bool
	separator, ok = second.(string)
	if !ok {
		return "", fmt.Errorf("join separator has unsupported type %T", second)
	}
	if len(separator) > 64 {
		return "", fmt.Errorf("join separator exceeds size limit")
	}
	if len(parts) > 256 {
		return "", fmt.Errorf("join value exceeds item limit")
	}
	joined := strings.Join(parts, separator)
	if len(joined) > maxRenderedTemplateBytes {
		return "", fmt.Errorf("join result exceeds size limit")
	}
	return joined, nil
}
