package cardigann

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	MaxCorpusDefinitionFiles                = 8192
	MaxCorpusDefinitionAggregateBytes int64 = 128 << 20
)

// CorpusReport is a deterministic, read-only compiler inventory. It never
// copies, activates, or registers external definition bytes.
type CorpusReport struct {
	Source              string
	Total               int
	Runnable            int
	Inert               int
	CapabilityHistogram map[CapabilityCode]int
	Definitions         []DefinitionDescriptor
}

type corpusProvider struct {
	source    string
	documents []SourceDocument
}

func (p corpusProvider) Source() string                       { return p.source }
func (p corpusProvider) Documents() ([]SourceDocument, error) { return p.documents, nil }

// ClassifyCorpusDirectory reports the current compiler's view of direct YAML
// files under root. It is intentionally separate from DirectoryProvider: the
// local-owner provider remains capped at 256 files while this read-only audit
// can inspect a larger external research corpus.
func ClassifyCorpusDirectory(source, root string) (CorpusReport, error) {
	if _, err := ParseDefinitionRef(strings.TrimSpace(source) + ":source-check"); err != nil {
		return CorpusReport{}, fmt.Errorf("corpus source: %w", err)
	}
	documents, err := readCorpusDocuments(root)
	if err != nil {
		return CorpusReport{}, err
	}
	descriptors, err := DescribeProvider(corpusProvider{source: source, documents: documents})
	if err != nil {
		return CorpusReport{}, err
	}
	report := CorpusReport{
		Source:              source,
		Total:               len(descriptors),
		CapabilityHistogram: make(map[CapabilityCode]int),
		Definitions:         descriptors,
	}
	for _, descriptor := range descriptors {
		if descriptor.State == DefinitionStateRunnableUnverified {
			report.Runnable++
		} else {
			report.Inert++
		}
		for _, capability := range descriptor.Unsupported {
			report.CapabilityHistogram[capability]++
		}
	}
	return report, nil
}

func readCorpusDocuments(root string) ([]SourceDocument, error) {
	root = strings.TrimSpace(root)
	if root == "" || hasTraversalComponent(root) {
		return nil, fmt.Errorf("corpus requires an explicit non-traversing directory")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve corpus directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve corpus directory symlinks: %w", err)
	}
	// A corpus is a read-only audit input, not an installed source. Canonicalize
	// macOS's /tmp symlink rather than rejecting a conventional local corpus
	// path; every opened file remains directly under this resolved directory.
	absolute = resolved
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("stat corpus directory: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("corpus directory must be a real directory")
	}
	opened, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open corpus directory: %w", err)
	}
	defer opened.Close()
	directory, err := opened.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open corpus root: %w", err)
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read corpus directory: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close corpus directory: %w", closeErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	documents := make([]SourceDocument, 0, len(entries))
	var aggregate int64
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".yml" {
			continue
		}
		if len(documents) >= MaxCorpusDefinitionFiles {
			return nil, fmt.Errorf("corpus exceeds %d YAML files", MaxCorpusDefinitionFiles)
		}
		data, err := readCorpusFile(opened, name, MaxDefinitionFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read corpus definition %q: %w", name, err)
		}
		aggregate += int64(len(data))
		if aggregate > MaxCorpusDefinitionAggregateBytes {
			return nil, fmt.Errorf("corpus exceeds %d aggregate bytes", MaxCorpusDefinitionAggregateBytes)
		}
		documents = append(documents, SourceDocument{Path: name, Data: data})
	}
	return documents, nil
}

func readCorpusFile(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%q must be a bounded regular non-symlink file", name)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	opened, statErr := file.Stat()
	if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		file.Close()
		return nil, fmt.Errorf("%q changed identity or is not a regular file", name)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", name, limit)
	}
	return data, nil
}
