package prepare

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const appleDoubleMagic = "\x00\x05\x16\x07"

// removeAppleDoubleSidecars removes Finder's AppleDouble metadata files from
// a prepared drive. Both its name and AppleDouble file signature must match,
// and its non-AppleDouble sibling must still exist. An ordinary file named
// with the same prefix and an orphan sidecar are preserved. The os.Root
// capability keeps both the walk and removals inside the selected drive.
func removeAppleDoubleSidecars(root *os.Root) ([]string, error) {
	var removed []string
	err := fs.WalkDir(root.FS(), ".", func(rel string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "._") || len(name) == 2 {
			return nil
		}
		appleDouble, err := hasAppleDoubleMagic(root, rel)
		if err != nil {
			return err
		}
		if !appleDouble {
			return nil
		}

		sibling := strings.TrimPrefix(name, "._")
		if sibling == "." || sibling == ".." {
			return nil
		}
		parent := path.Dir(rel)
		siblingRel := path.Join(parent, sibling)
		if _, err := root.Lstat(filepath.FromSlash(siblingRel)); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}

		if err := root.Remove(filepath.FromSlash(rel)); err != nil {
			return err
		}
		removed = append(removed, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(removed)
	return removed, nil
}

func hasAppleDoubleMagic(root *os.Root, rel string) (bool, error) {
	file, err := root.Open(filepath.FromSlash(rel))
	if err != nil {
		return false, err
	}
	defer file.Close()
	var header [len(appleDoubleMagic)]byte
	if _, err := io.ReadFull(file, header[:]); errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return bytes.Equal(header[:], []byte(appleDoubleMagic)), nil
}
