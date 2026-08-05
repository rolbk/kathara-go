package vfs

import (
	"io/fs"
	"sort"
)

// Walk is fs.WalkDir over an FS, rooted at root (pyfilesystem's fs.walk).
// Both OSDir and Memory implement fs.ReadDirFS with name-sorted output, so the
// traversal order is deterministic on both.
func Walk(fsys FS, root string, fn fs.WalkDirFunc) error {
	if fsys == nil {
		return ErrNoFilesystem
	}
	cleaned, err := CleanPath(root)
	if err != nil {
		return err
	}
	return fs.WalkDir(fsys, cleaned, fn)
}

// Files is fs.walk.files(): every regular file under root, sorted, as paths
// relative to the FS root (no leading "/" — pyfilesystem returns "/a/b", io/fs
// convention here is "a/b").
func Files(fsys FS, root string) ([]string, error) {
	return collect(fsys, root, false)
}

// Dirs is fs.walk.dirs(): every directory under root, sorted, root excluded.
func Dirs(fsys FS, root string) ([]string, error) {
	return collect(fsys, root, true)
}

func collect(fsys FS, root string, wantDirs bool) ([]string, error) {
	if fsys == nil {
		return nil, ErrNoFilesystem
	}
	cleanedRoot, err := CleanPath(root)
	if err != nil {
		return nil, err
	}
	var out []string
	err = Walk(fsys, cleanedRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if wantDirs && p != cleanedRoot {
				out = append(out, p)
			}
			return nil
		}
		if !wantDirs {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}
