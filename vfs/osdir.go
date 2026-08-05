package vfs

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

func (o *osDir) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	f, err := os.Open(o.host(name))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (o *osDir) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	return os.Stat(o.host(name))
}

func (o *osDir) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	return os.ReadDir(o.host(name))
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
	return os.Create(o.host(cleaned))
}

func (o *osDir) Append(name string) (io.WriteCloser, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return nil, err
	}
	if info, serr := os.Stat(o.host(cleaned)); serr == nil && info.IsDir() {
		return nil, &fs.PathError{Op: "append", Path: cleaned, Err: ErrFileExpected}
	}
	return os.OpenFile(o.host(cleaned), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
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
	return os.MkdirAll(o.host(cleaned), perm)
}

func (o *osDir) Remove(name string) error {
	cleaned, err := CleanPath(name)
	if err != nil {
		return err
	}
	return os.Remove(o.host(cleaned))
}

func (o *osDir) SysPath(name string) (string, bool) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return "", false
	}
	return o.host(cleaned), true
}
