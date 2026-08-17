package cardigann

import (
	"fmt"
	"io/fs"
	"path"
)

// BuiltinProvider supplies the definitions embedded in the Caravan binary.
type BuiltinProvider struct{}

func (BuiltinProvider) Source() string { return BuiltinSource }

func (BuiltinProvider) Documents() ([]SourceDocument, error) {
	entries, err := fs.ReadDir(definitionFiles, "definitions")
	if err != nil {
		return nil, fmt.Errorf("read embedded definitions: %w", err)
	}
	documents := make([]SourceDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".yml" {
			continue
		}
		name := path.Join("definitions", entry.Name())
		data, err := definitionFiles.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		documents = append(documents, SourceDocument{Path: name, Data: data})
	}
	return documents, nil
}
