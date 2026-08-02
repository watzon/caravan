package relocate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ValidateRoot checks a candidate storage root for a re-point.
//
// It returns warnings separately from the error on purpose. "This folder does
// not look like a Caravan library" is exactly what a user re-pointing at a
// freshly formatted drive should be told, and exactly what must not stop them:
// the empty-root case is a first run, and refusing it would make the operation
// useless for the thing it is most often used for.
func ValidateRoot(root string) ([]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("the storage root is required")
	}
	if !filepath.IsAbs(root) {
		// The one absolute path Caravan persists (SPEC §10). A relative root
		// would resolve against whatever directory the process happens to have
		// been started in, which is a different folder on every restart.
		return nil, errors.New("the storage root must be an absolute path")
	}

	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s does not exist", root)
	}
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", root)
	}

	warnings := []string{}
	switch entries, err := os.ReadDir(filepath.Join(root, libraryTree)); {
	case errors.Is(err, fs.ErrNotExist):
		warnings = append(warnings, fmt.Sprintf(
			"there is no %s folder here yet, so Caravan will start with an empty library until something is imported", libraryTree))
	case err != nil:
		warnings = append(warnings, fmt.Sprintf("the %s folder here cannot be read: %v", libraryTree, err))
	case len(entries) == 0:
		warnings = append(warnings, fmt.Sprintf("the %s folder here is empty", libraryTree))
	}
	return warnings, nil
}

// ValidateMove checks the pair of roots a migration would move between.
//
// Every rule here describes a way to lose a library, which is why it runs twice:
// once when the migration is queued, and again inside the job, because hours can
// pass in between and a path can be re-mounted, deleted or replaced by a symlink
// in that time.
//
// The empty-target rule is deliberately *not* here — see ValidateFreshTarget.
func ValidateMove(source, target string) error {
	source = strings.TrimSpace(source)
	if source == "" {
		return errors.New("there is no storage root to move from")
	}
	if _, err := ValidateRoot(target); err != nil {
		return err
	}
	if !filepath.IsAbs(source) {
		return errors.New("the current storage root is not an absolute path")
	}
	if info, err := os.Stat(source); err != nil {
		return fmt.Errorf("the current storage root %s cannot be read: %w", source, err)
	} else if !info.IsDir() {
		return fmt.Errorf("the current storage root %s is not a folder", source)
	}

	from, to := resolve(source), resolve(target)
	if from == to {
		return errors.New("the new storage root is the current one")
	}
	// Nesting either way makes the move eat itself: moving a parent into its own
	// child recurses forever, and moving a child into its parent walks a tree it
	// is simultaneously writing into.
	if within(from, to) {
		return fmt.Errorf("%s is inside the current storage root", target)
	}
	if within(to, from) {
		return fmt.Errorf("the current storage root is inside %s", target)
	}
	return nil
}

// ValidateFreshTarget refuses a target that already holds media.
//
// It is what makes rollback knowable. Everything under the target's own library
// and incomplete folders belongs to the migration that put it there, so undoing
// a failed move is "put back whatever is at the target" — no bookkeeping to
// lose, and correct after a crash that took the bookkeeping with it. Merging
// into an occupied target would break that, and a rollback would then delete
// files the migration never moved.
func ValidateFreshTarget(target string) error {
	for _, tree := range trees {
		entries, err := os.ReadDir(filepath.Join(target, tree))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("the %s folder in %s cannot be read: %w", tree, target, err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("%s already has a non-empty %s folder; migrate into an empty root", target, tree)
		}
	}
	return nil
}

// resolve canonicalises a path for comparison: absolute, cleaned, and with
// symlinks resolved where the filesystem can answer. A path that cannot be
// resolved falls back to its cleaned absolute form rather than failing — the
// comparison is a guard, and a guard that errors on an unreadable path would
// refuse moves that are perfectly safe.
func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = filepath.Clean(p)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}

// within reports whether child is parent or sits underneath it.
func within(parent, child string) bool {
	if parent == child {
		return true
	}
	return strings.HasPrefix(child, parent+string(filepath.Separator))
}
