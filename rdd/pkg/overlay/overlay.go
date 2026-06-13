// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package overlay

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"
)

// Distro is a mutable distro root. The tarball and ext4 image backends both
// implement it, so Apply drives them identically.
type Distro interface {
	// EnsureDir creates dir and any missing parents. When force is true it applies
	// uid/gid/mode/mtime even to a directory that already exists; otherwise it
	// leaves an existing directory untouched.
	EnsureDir(dir string, uid, gid int, mode os.FileMode, mtime time.Time, force bool) error
	// WriteFile creates or replaces a regular file with the given contents.
	WriteFile(file string, contents io.Reader, uid, gid int, mode os.FileMode, mtime time.Time) error
	// Symlink creates a symbolic link pointing to target.
	Symlink(link, target string, uid, gid int, mtime time.Time) error
	// Close flushes pending changes.
	Close() error
}

// Apply writes every manifest entry into the distro and closes it. Explicit
// directories run first so their permissions survive the implicit parent
// creation that files and symlinks trigger.
func Apply(d Distro, m *Manifest, sourceDir string) error {
	now := time.Now()
	for i := range m.Entries {
		e := &m.Entries[i]
		if e.kind() != TypeDir {
			continue
		}
		mode, _ := e.mode(0o755)
		if err := d.EnsureDir(e.Path, e.UID, e.GID, mode, entryMtime(e, now), true); err != nil {
			return fmt.Errorf("creating directory %s: %w", e.Path, err)
		}
	}
	for i := range m.Entries {
		e := &m.Entries[i]
		switch e.kind() {
		case TypeDir:
			continue // created above
		case TypeSymlink:
			if err := d.EnsureDir(path.Dir(e.Path), 0, 0, 0o755, now, false); err != nil {
				return fmt.Errorf("creating parent of %s: %w", e.Path, err)
			}
			if err := d.Symlink(e.Path, e.Target, e.UID, e.GID, entryMtime(e, now)); err != nil {
				return fmt.Errorf("creating symlink %s: %w", e.Path, err)
			}
		default: // TypeFile
			if err := d.EnsureDir(path.Dir(e.Path), 0, 0, 0o755, now, false); err != nil {
				return fmt.Errorf("creating parent of %s: %w", e.Path, err)
			}
			if err := writeFile(d, e, sourceDir, now); err != nil {
				return err
			}
		}
	}
	return d.Close()
}

// entryMtime resolves a timestamp for entries without a source file: the manifest
// override when present, otherwise the build time.
func entryMtime(e *Entry, buildTime time.Time) time.Time {
	if t, ok, _ := e.parseMtime(); ok {
		return t
	}
	return buildTime
}

func writeFile(d Distro, e *Entry, sourceDir string, buildTime time.Time) error {
	src, err := os.Open(filepath.Join(sourceDir, filepath.FromSlash(e.Source)))
	if err != nil {
		return fmt.Errorf("opening source for %s: %w", e.Path, err)
	}
	defer src.Close()
	// A file keeps its source timestamp unless the manifest overrides it.
	mtime := buildTime
	if info, err := src.Stat(); err == nil {
		mtime = info.ModTime()
	}
	if t, ok, _ := e.parseMtime(); ok {
		mtime = t
	}
	mode, _ := e.mode(0o644)
	if err := d.WriteFile(e.Path, src, e.UID, e.GID, mode, mtime); err != nil {
		return fmt.Errorf("writing file %s: %w", e.Path, err)
	}
	return nil
}
