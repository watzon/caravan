package cardigann

import (
	"fmt"
	"regexp"
	"strings"
)

// BuiltinSource identifies definitions embedded in the Caravan binary. Bare
// legacy definition IDs resolve only against this source.
const BuiltinSource = "builtin"

var (
	definitionSourcePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	definitionIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// DefinitionRef identifies one definition within an explicitly named source.
// Its canonical string form is "source:id".
type DefinitionRef struct {
	Source string
	ID     string
}

func (r DefinitionRef) String() string {
	return r.Source + ":" + r.ID
}

// ParseDefinitionRef parses a namespaced reference. A bare ID remains a
// backwards-compatible reference to the embedded builtin source.
func ParseDefinitionRef(value string) (DefinitionRef, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefinitionRef{}, fmt.Errorf("definition reference is empty")
	}
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		parts = []string{BuiltinSource, parts[0]}
	}
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return DefinitionRef{}, fmt.Errorf("definition reference %q must be source:id", value)
	}
	source, id := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	if !definitionSourcePattern.MatchString(source) || !definitionIDPattern.MatchString(id) {
		return DefinitionRef{}, fmt.Errorf("definition reference %q contains unsupported characters", value)
	}
	return DefinitionRef{Source: source, ID: strings.ToLower(id)}, nil
}
