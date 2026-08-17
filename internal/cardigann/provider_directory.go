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

// MaxDefinitionFileBytes bounds one locally supplied manifest before YAML
// decoding. Definition providers never recurse into child directories.
const (
	MaxDefinitionFileBytes      int64 = 1 << 20
	MaxDefinitionAggregateBytes int64 = 16 << 20
	MaxDefinitionFiles                = 256
)

// DirectoryProvider reads one explicitly configured directory as a named
// definition source. It accepts only direct, regular .yml files and ignores
// symlinks, directories, and every other entry type.
type DirectoryProvider struct {
	source  string
	dir     string
	dirInfo fs.FileInfo
}

func NewDirectoryProvider(source, dir string) (*DirectoryProvider, error) {
	source = strings.TrimSpace(source)
	if _, err := ParseDefinitionRef(source + ":source-check"); err != nil {
		return nil, fmt.Errorf("directory provider source: %w", err)
	}
	dir = strings.TrimSpace(dir)
	if dir == "" || hasTraversalComponent(dir) {
		return nil, fmt.Errorf("directory provider requires an explicit non-traversing directory")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve definition directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve definition directory symlinks: %w", err)
	}
	if resolved != absolute {
		return nil, fmt.Errorf("definition directory path must not contain symlinks")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("stat definition directory: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("definition directory must be a real directory")
	}
	return &DirectoryProvider{source: source, dir: absolute, dirInfo: info}, nil
}

func hasTraversalComponent(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func (p *DirectoryProvider) Source() string {
	if p == nil {
		return ""
	}
	return p.source
}

func (p *DirectoryProvider) Documents() ([]SourceDocument, error) {
	if p == nil {
		return nil, fmt.Errorf("directory provider is nil")
	}
	if err := p.validateDirectoryPath(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(p.dir)
	if err != nil {
		return nil, fmt.Errorf("open definition directory: %w", err)
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open definition directory root: %w", err)
	}
	openedDirectoryInfo, err := directory.Stat()
	if err != nil {
		directory.Close()
		return nil, fmt.Errorf("stat opened definition directory: %w", err)
	}
	if p.dirInfo == nil || !os.SameFile(p.dirInfo, openedDirectoryInfo) {
		directory.Close()
		return nil, fmt.Errorf("definition directory identity changed after provider construction")
	}
	if err := p.validateDirectoryPath(); err != nil {
		directory.Close()
		return nil, err
	}
	entries, err := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err != nil {
		return nil, fmt.Errorf("read definition directory: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close definition directory: %w", closeErr)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	documents := make([]SourceDocument, 0, len(entries))
	var aggregateBytes int64
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yml" {
			continue
		}
		info, err := root.Lstat(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("stat definition %q: %w", entry.Name(), err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("definition %q must be a regular non-symlink file", entry.Name())
		}
		if info.Size() > MaxDefinitionFileBytes {
			return nil, fmt.Errorf("definition %q exceeds %d bytes", entry.Name(), MaxDefinitionFileBytes)
		}
		if len(documents) >= MaxDefinitionFiles {
			return nil, fmt.Errorf("definition directory exceeds %d files", MaxDefinitionFiles)
		}
		file, err := root.OpenFile(entry.Name(), os.O_RDONLY, 0)
		if err != nil {
			return nil, fmt.Errorf("open definition %q: %w", entry.Name(), err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			file.Close()
			return nil, fmt.Errorf("opened definition %q changed identity or is not a regular file", entry.Name())
		}
		currentInfo, lstatErr := root.Lstat(entry.Name())
		if lstatErr != nil || currentInfo.Mode()&fs.ModeSymlink != 0 || !currentInfo.Mode().IsRegular() || !os.SameFile(openedInfo, currentInfo) {
			file.Close()
			return nil, fmt.Errorf("definition %q path changed during open", entry.Name())
		}
		data, readErr := io.ReadAll(io.LimitReader(file, MaxDefinitionFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read definition %q: %w", entry.Name(), readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close definition %q: %w", entry.Name(), closeErr)
		}
		if int64(len(data)) > MaxDefinitionFileBytes {
			return nil, fmt.Errorf("definition %q exceeds %d bytes", entry.Name(), MaxDefinitionFileBytes)
		}
		aggregateBytes += int64(len(data))
		if aggregateBytes > MaxDefinitionAggregateBytes {
			return nil, fmt.Errorf("definition directory exceeds %d aggregate bytes", MaxDefinitionAggregateBytes)
		}
		documents = append(documents, SourceDocument{Path: entry.Name(), Data: data})
	}
	return documents, nil
}

func (p *DirectoryProvider) validateDirectoryPath() error {
	info, err := os.Lstat(p.dir)
	if err != nil {
		return fmt.Errorf("stat definition directory: %w", err)
	}
	if p.dirInfo == nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(p.dirInfo, info) {
		return fmt.Errorf("definition directory path changed after provider construction")
	}
	return nil
}
