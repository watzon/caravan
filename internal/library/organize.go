package library

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// osLink is the default Manager.link. It exists so the field has a named,
// greppable default rather than an inline reference to os.Link.
func osLink(oldname, newname string) error { return os.Link(oldname, newname) }

// maxCollisionSuffix bounds the " (n)" search for a free filename. Hitting it
// means something is generating names in a loop, and failing loudly beats
// spinning.
const maxCollisionSuffix = 999

// placeFile moves the file at srcRel to dstRel, both storage-root-relative,
// and returns the path it actually landed on.
//
// The destination is collision-safe: if a *different* file already occupies
// dstRel, a " (1)", " (2)", … suffix is appended before the extension. If
// dstRel already is the source file — the common case on a rescan of an
// already-organized library — nothing is touched.
func (m *Manager) placeFile(srcRel, dstRel string) (string, error) {
	srcAbs := m.abs(srcRel)
	dstAbs := m.abs(dstRel)

	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return "", fmt.Errorf("library: stat %s: %w", srcRel, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return "", fmt.Errorf("library: create %s: %w", filepath.Dir(dstRel), err)
	}

	finalAbs, same, err := uniqueDest(dstAbs, srcInfo)
	if err != nil {
		return "", err
	}
	if !same {
		if err := m.transfer(srcAbs, finalAbs); err != nil {
			return "", err
		}
	}
	return m.rel(finalAbs)
}

// uniqueDest finds a free destination path, or reports that dst already is
// src. Identity is os.SameFile rather than a string compare so a case-
// insensitive filesystem cannot trick Caravan into copying a file onto itself.
func uniqueDest(dst string, src fs.FileInfo) (string, bool, error) {
	ext := filepath.Ext(dst)
	stem := strings.TrimSuffix(dst, ext)

	for i := 0; i <= maxCollisionSuffix; i++ {
		candidate := dst
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		info, err := os.Stat(candidate)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return candidate, false, nil
		case err != nil:
			return "", false, fmt.Errorf("library: stat %s: %w", candidate, err)
		case os.SameFile(info, src):
			return candidate, true, nil
		}
	}
	return "", false, fmt.Errorf("library: no free filename for %s after %d attempts", dst, maxCollisionSuffix)
}

// transfer puts src at dst, both absolute, leaving nothing behind at src.
//
// Hardlinking is tried first: it is instant, costs no extra space, and is what
// the phase-2 import pipeline needs so a seeding torrent keeps its copy
// (SPEC §5.1). The source link is then removed so a rescan cannot rediscover
// the original, unorganized name. Where hardlinks are unavailable — exFAT has
// none (SPEC §3), and the source may live on another device — it falls back to
// rename, and finally to a copy into a temporary file that is renamed into
// place, which is the only option across filesystems.
func (m *Manager) transfer(src, dst string) error {
	if err := m.link(src, dst); err == nil {
		if err := os.Remove(src); err != nil {
			return fmt.Errorf("library: remove %s after hardlink: %w", src, err)
		}
		return nil
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyThenReplace(src, dst)
}

// copyThenReplace copies src into a temporary file beside dst, renames it into
// place, and only then removes src. The rename is what makes the destination
// atomic: a crash mid-copy leaves a stray temp file, never a truncated media
// file the user would have to notice.
func copyThenReplace(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("library: open %s: %w", src, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".caravan-*")
	if err != nil {
		return fmt.Errorf("library: create temp beside %s: %w", dst, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename below succeeds

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("library: copy %s to %s: %w", src, dst, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("library: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("library: close %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("library: chmod %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("library: rename %s to %s: %w", tmpName, dst, err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("library: remove %s after copy: %w", src, err)
	}
	return nil
}
