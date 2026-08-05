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
	prefix string // cleaned, never "."
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
	if cleaned == "." {
		return fsys, nil
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

func (s *subFS) Open(name string) (fs.File, error) {
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return s.parent.Open(full)
}

func (s *subFS) Stat(name string) (fs.FileInfo, error) {
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	return fs.Stat(s.parent, full)
}

func (s *subFS) ReadDir(name string) ([]fs.DirEntry, error) {
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

func (s *subFS) Append(name string) (io.WriteCloser, error) {
	full, err := s.join(name)
	if err != nil {
		return nil, err
	}
	if a, ok := s.parent.(Appender); ok {
		return a.Append(full)
	}
	return nil, &fs.PathError{Op: "append", Path: name, Err: fs.ErrInvalid}
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
