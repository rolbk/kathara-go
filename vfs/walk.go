package vfs

import (
	"errors"
	"io/fs"
	"path"
	"sort"
)

// Walk is fs.WalkDir over an FS, rooted at root.
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

// WalkFollow is [Walk] with pyfilesystem's classification: every entry is
// typed by a STAT, which follows symlinks, instead of by its directory-entry
// type, which does not.
func WalkFollow(fsys FS, root string, fn fs.WalkDirFunc) error {
	if fsys == nil {
		return ErrNoFilesystem
	}
	cleaned, err := CleanPath(root)
	if err != nil {
		return err
	}
	info, err := fs.Stat(fsys, cleaned)
	if err != nil {
		err = fn(cleaned, nil, err)
	} else {
		err = walkFollow(fsys, cleaned, fs.FileInfoToDirEntry(info), fn)
	}
	if errors.Is(err, fs.SkipDir) || errors.Is(err, fs.SkipAll) {
		return nil
	}
	return err
}

// walkFollow is [fs.WalkDir]'s recursion with the entry classification
// replaced; the SkipDir / SkipAll contract is kept as the stdlib spells it.
func walkFollow(fsys FS, name string, entry fs.DirEntry, fn fs.WalkDirFunc) error {
	if err := fn(name, entry, nil); err != nil || !entry.IsDir() {
		if errors.Is(err, fs.SkipDir) && entry.IsDir() {
			return nil
		}
		return err
	}

	entries, err := fs.ReadDir(fsys, name)
	if err != nil {
		// Second call, to report the ReadDir error.
		err = fn(name, entry, err)
		if err != nil {
			if errors.Is(err, fs.SkipDir) {
				return nil
			}
			return err
		}
	}

	for _, child := range entries {
		childPath := path.Join(name, child.Name())
		childEntry, err := followEntry(fsys, childPath, child)
		if err != nil {
			return err
		}
		if err := walkFollow(fsys, childPath, childEntry, fn); err != nil {
			if errors.Is(err, fs.SkipDir) {
				break
			}
			return err
		}
	}
	return nil
}

// followEntry is `os.DirEntry.is_dir()`: stat the entry, and on a
// "does not exist" failure keep the unfollowed entry, which is a
// non-directory for the broken symlink that is the only way to get here.
func followEntry(fsys FS, name string, entry fs.DirEntry) (fs.DirEntry, error) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return entry, nil
		}
		return nil, err
	}
	return fs.FileInfoToDirEntry(info), nil
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
	// [WalkFollow], not [Walk]: `fs.walk.files` and `fs.walk.dirs` classify
	// through `DirEntry.is_dir()`, which follows symlinks.
	err = WalkFollow(fsys, cleanedRoot, func(p string, d fs.DirEntry, err error) error {
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
