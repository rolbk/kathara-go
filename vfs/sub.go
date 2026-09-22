package vfs

import (
	"io"
	"io/fs"
	"os"
	"path"
)

// subFS is fs.opendir(dir): a view of fsys rooted at dir.
// model/Machine.py:80 uses it to give each device its own filesystem
// (`self.lab.fs.opendir(self.name)`).
type subFS struct {
	parent FS
	prefix string // cleaned; "." is a view of the whole parent
}

// Sub returns an FS rooted at dir inside fsys, reproducing fs.opendir:
// the directory must already exist, otherwise the call fails.
// Machine.py:636 is the caller that MkdirAlls first.
func Sub(fsys FS, dir string) (FS, error) {
	if fsys == nil {
		return nil, ErrNoFilesystem
	}
	cleaned, err := CleanPath(dir)
	if err != nil {
		return nil, err
	}
	info, err := fs.Stat(fsys, cleaned)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "opendir", Path: cleaned, Err: ErrDirectoryExpected}
	}
	if s, ok := fsys.(*subFS); ok {
		return &subFS{parent: s.parent, prefix: path.Join(s.prefix, cleaned)}, nil
	}
	return &subFS{parent: fsys, prefix: cleaned}, nil
}

// TypeName is "sub", matching pyfilesystem: opendir returns a SubFS whose
// class name reduces to "sub" under FilesystemMixin.fs_type(), regardless of
// what backs it. Verified on both a mem:// and an osfs:// parent.
// SysPath still resolves through to the parent, so an OSFS-backed sub reports
// the real host subdirectory — also verified.
func (s *subFS) TypeName() string { return "sub" }

func (s *subFS) join(name string) (string, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return "", err
	}
	if cleaned == "." {
		return s.prefix, nil
	}
	return path.Join(s.prefix, cleaned), nil
}

// The io/fs read side keeps the io/fs contract, exactly as memFS and osDir do:
// a name that fs.ValidPath rejects is fs.ErrInvalid, not something CleanPath
// quietly forgives. Only the write side takes pyfilesystem-style paths.

func (s *subFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return s.parent.Open(full)
}

func (s *subFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return fs.Stat(s.parent, full)
}

func (s *subFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return fs.ReadDir(s.parent, full)
}

func (s *subFS) Create(name string) (io.WriteCloser, error) {
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return s.parent.Create(full)
}

// Append delegates through openAppend rather than asserting Appender itself.
// A subFS always satisfies the Appender assertion that appendBytes makes, so
// asserting here would make the parent's missing Appender fatal instead of
// falling back to the read-modify-rewrite the Appender contract promises.
func (s *subFS) Append(name string) (io.WriteCloser, error) {
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return openAppend(s.parent, full)
}

func (s *subFS) MkdirAll(name string, perm os.FileMode) error {
	full, err := s.join(name)
	if err != nil {
		return err
	}
	return s.parent.MkdirAll(full, perm)
}

func (s *subFS) Remove(name string) error {
	full, err := s.join(name)
	if err != nil {
		return err
	}
	return s.parent.Remove(full)
}

func (s *subFS) SysPath(name string) (string, bool) {
	full, err := s.join(name)
	if err != nil {
		return "", false
	}
	return s.parent.SysPath(full)
}
