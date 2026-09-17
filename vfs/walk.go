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
//
// It classifies every entry by its DIRECTORY-ENTRY TYPE, which does not follow
// symlinks — so a link to a directory is reported as a plain, non-directory
// entry and is not descended into. That is NOT what pyfilesystem's walker
// does; anything porting an `fs.walk` / `copy_fs` call wants [WalkFollow].
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
//
// This is what `fs.walk` actually does. `OSFS._scandir` builds each Info from
// `os.DirEntry.is_dir()`, and that follows by default, so a symlink to a
// directory is a directory to the walker and its target is descended into.
// Oracle-verified on a tree holding `linkdir -> real/` and `linkfile ->
// plain.txt`: `Walker().dirs()` returns `['/linkdir', '/real']` and
// `copy_fs` writes `/linkdir/f.txt` alongside `/real/f.txt`.
//
// [Walk] cannot express that: a symlink's DirEntry reports `ModeSymlink`, so
// `IsDir()` is false and a caller that splits entries into "directories" and
// "files" hands the link to its file path, where reading it fails with
// [ErrFileExpected] partway through the tree. It is the same asymmetry
// [CopyDirectory] documents on the host side.
//
// The stat failures are split the way CPython's `DirEntry.is_dir()` splits
// them, and the difference is oracle-visible:
//
//   - "does not exist" is swallowed and the entry stays a non-directory, which
//     is how a BROKEN symlink behaves — `is_dir()` is False for it, the walker
//     lists it among the files, and the failure surfaces later when the copy
//     opens it (`ResourceNotFound`). ENOTDIR arrives here as [fs.ErrNotExist]
//     too, since osDir maps it that way for the file operations.
//   - every other error propagates, which is how a symlink LOOP behaves:
//     `is_dir()` raises `OSError: [Errno 40] Too many levels of symbolic
//     links` and pyfilesystem re-raises it as `fs.errors.OperationFailed`,
//     aborting the walk. Verified live with a mutual `a -> b -> a` pair.
//
// A self-referencing link (`self -> .`) therefore terminates without any cycle
// detection — which Python has none of either: the path grows one component
// per level until the kernel refuses it with ELOOP. Go hits that on the stat
// where Python hits it on the following scandir, so the erroring path can
// differ by one component; the errno, and the abort, are the same.
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
