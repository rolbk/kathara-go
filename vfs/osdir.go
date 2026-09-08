package vfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// osDir is the `osfs://<path>` implementation (model/Lab.py:79).
type osDir struct {
	root string
}

// OSDir returns an FS rooted at the host directory path.
//
// Divergence from pyfilesystem, deliberate and documented in SPIKES/vfs.md:
// open_fs("osfs://missing") raises CreateFailed eagerly, whereas the §6
// signature `OSDir(path) FS` cannot report an error. Construction therefore
// succeeds and the first operation fails with fs.ErrNotExist. Callers that
// need the eager check (model.NewLab) should stat the directory themselves.
func OSDir(path string) FS {
	return &osDir{root: filepath.Clean(path)}
}

func (o *osDir) TypeName() string { return "os" }

// host maps a caller path onto the host filesystem. CleanPath has already
// clamped ".." at the root, so the join cannot escape o.root through the path
// string. Symlinks inside the tree are not chased, matching OSFS.
func (o *osDir) host(cleaned string) string {
	if cleaned == "." {
		return o.root
	}
	return filepath.Join(o.root, filepath.FromSlash(cleaned))
}

// convert maps host errno values onto the pyfilesystem error classes,
// reproducing fs/error_tools.py's _ConvertOSErrors tables — the context
// manager every OSFS method wraps itself in.
//
// The FILE/DIR split is real and load-bearing. ENOTDIR ("a component of the
// path is not a directory") is ResourceNotFound for the file operations
// (getinfo, openbin, remove — FILE_ERRORS has the DirectoryExpected mapping
// commented out) and DirectoryExpected for the directory ones (listdir,
// scandir, makedir, removedir). Verified live against OSFS with a file at
// /afile: getinfo("/afile/child") raises ResourceNotFound while
// listdir("/afile/child") raises DirectoryExpected.
//
// Without this the raw syscall.ENOTDIR escapes, and it satisfies none of
// errors.Is(err, fs.ErrNotExist) / ErrDirectoryExpected / ErrFileExpected, so
// a condition Memory() reports as fs.ErrNotExist would be unclassifiable here.
func (o *osDir) convert(op, cleaned string, dirOp bool, err error) error {
	if err == nil {
		return nil
	}
	wrap := func(target error) error {
		return &fs.PathError{Op: op, Path: cleaned, Err: target}
	}
	switch {
	case isNotDirErr(err):
		if dirOp {
			return wrap(ErrDirectoryExpected)
		}
		return wrap(fs.ErrNotExist)
	case isNotEmptyErr(err):
		return wrap(ErrDirectoryNotEmpty)
	case errors.Is(err, syscall.EISDIR):
		return wrap(ErrFileExpected)
	}
	return err
}

func (o *osDir) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	f, err := os.Open(o.host(name))
	if err != nil {
		return nil, o.convert("open", name, false, err)
	}
	return f, nil
}

func (o *osDir) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	info, err := os.Stat(o.host(name))
	if err != nil {
		return nil, o.convert("stat", name, false, err)
	}
	return info, nil
}

func (o *osDir) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	entries, err := os.ReadDir(o.host(name))
	if err != nil {
		// A directory operation, so ENOTDIR is DirectoryExpected — covering
		// both "name is a file" (isempty on a file) and "an ancestor is".
		return nil, o.convert("readdir", name, true, err)
	}
	return entries, nil
}

func (o *osDir) Create(name string) (io.WriteCloser, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return nil, err
	}
	// Normalise EISDIR into the pyfilesystem FileExpected the tests assert.
	if info, serr := os.Stat(o.host(cleaned)); serr == nil && info.IsDir() {
		return nil, &fs.PathError{Op: "create", Path: cleaned, Err: ErrFileExpected}
	}
	f, err := os.Create(o.host(cleaned))
	if err != nil {
		return nil, o.convert("create", cleaned, false, err)
	}
	return f, nil
}

func (o *osDir) Append(name string) (io.WriteCloser, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return nil, err
	}
	if info, serr := os.Stat(o.host(cleaned)); serr == nil && info.IsDir() {
		return nil, &fs.PathError{Op: "append", Path: cleaned, Err: ErrFileExpected}
	}
	f, err := os.OpenFile(o.host(cleaned), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return nil, o.convert("append", cleaned, false, err)
	}
	return f, nil
}

func (o *osDir) MkdirAll(name string, perm os.FileMode) error {
	cleaned, err := CleanPath(name)
	if err != nil {
		return err
	}
	// os.MkdirAll is a no-op on an existing directory (== recreate=True) but
	// returns ENOTDIR on an existing file; pyfilesystem raises DirectoryExpected.
	if info, serr := os.Stat(o.host(cleaned)); serr == nil && !info.IsDir() {
		return &fs.PathError{Op: "mkdir", Path: cleaned, Err: ErrDirectoryExpected}
	}
	return o.convert("mkdir", cleaned, true, os.MkdirAll(o.host(cleaned), perm))
}

func (o *osDir) Remove(name string) error {
	cleaned, err := CleanPath(name)
	if err != nil {
		return err
	}
	// pyfilesystem never removes the filesystem root: removedir("/") raises
	// RemoveRootError and remove("/") raises FileExpected on OSFS. os.Remove
	// has no such guard, so without this "" / "/" / "." would delete the lab's
	// own host directory.
	if cleaned == "." {
		return &fs.PathError{Op: "remove", Path: cleaned, Err: ErrRemoveRoot}
	}
	return o.convert("remove", cleaned, false, os.Remove(o.host(cleaned)))
}

func (o *osDir) SysPath(name string) (string, bool) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return "", false
	}
	return o.host(cleaned), true
}
