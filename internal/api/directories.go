package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// directoryListing is one folder the storage-root picker can show: the folder
// itself, the parent the Up control walks to, and the child folders a click
// descends into. Files are omitted — the picker chooses a root, not a media
// file.
type directoryListing struct {
	Path        string           `json:"path"`
	Parent      string           `json:"parent"`
	Directories []directoryEntry `json:"directories"`
}

// directoryEntry is one child folder. Path is absolute so the client can send
// it back without reconstructing separators.
type directoryEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleListDirectories is the browse half of choosing a storage root. The
// text field still accepts a typed path; this is how the picker fills it
// without asking the user to remember the host's layout.
func (s *server) handleListDirectories(w http.ResponseWriter, r *http.Request) {
	listing, err := listDirectories(strings.TrimSpace(r.URL.Query().Get("path")))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func listDirectories(raw string) (directoryListing, error) {
	if raw == "" {
		return listFilesystemRoot()
	}
	if !filepath.IsAbs(raw) {
		return directoryListing{}, errRelativeDirectory
	}
	path := filepath.Clean(raw)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return directoryListing{}, errMissingDirectory
		}
		return directoryListing{}, errUnreadableDirectory
	}
	if !info.IsDir() {
		return directoryListing{}, errNotADirectory
	}
	children, err := readChildDirectories(path)
	if err != nil {
		return directoryListing{}, errUnreadableDirectory
	}
	return directoryListing{
		Path:        path,
		Parent:      directoryParent(path),
		Directories: children,
	}, nil
}

func listFilesystemRoot() (directoryListing, error) {
	if runtime.GOOS == "windows" {
		return directoryListing{
			Directories: windowsVolumes(),
		}, nil
	}
	children, err := readChildDirectories("/")
	if err != nil {
		return directoryListing{}, errUnreadableDirectory
	}
	return directoryListing{Path: "/", Directories: children}, nil
}

func readChildDirectories(path string) ([]directoryEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	out := make([]directoryEntry, 0, len(entries))
	for _, entry := range entries {
		if !entryIsDir(path, entry) {
			continue
		}
		name := entry.Name()
		out = append(out, directoryEntry{
			Name: name,
			Path: filepath.Join(path, name),
		})
	}
	slices.SortFunc(out, func(a, b directoryEntry) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// entryIsDir follows a symlink once: a link to a folder is a folder the picker
// can descend into, which is how mounted volumes usually appear.
func entryIsDir(parent string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	info, err := os.Stat(filepath.Join(parent, entry.Name()))
	return err == nil && info.IsDir()
}

func directoryParent(path string) string {
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(path)
		cleaned := filepath.Clean(path)
		if cleaned == vol+`\` || cleaned == vol+`/` {
			return ""
		}
	}
	parent := filepath.Dir(path)
	if parent == path {
		return ""
	}
	return parent
}

func windowsVolumes() []directoryEntry {
	out := make([]directoryEntry, 0, 4)
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err != nil {
			continue
		}
		out = append(out, directoryEntry{Name: root, Path: root})
	}
	return out
}

var (
	errRelativeDirectory   = directoryError("path must be absolute")
	errMissingDirectory    = directoryError("directory does not exist")
	errNotADirectory       = directoryError("path is not a directory")
	errUnreadableDirectory = directoryError("directory cannot be read")
)

type directoryError string

func (e directoryError) Error() string { return string(e) }
