// Package vfs is the filesystem abstraction that replaces pyfilesystem2
// (PORT_SPEC §6, PACKAGE_GRAPH §0.2 #9).
//
// The package has no intra-project imports: it is a stdlib-only leaf, per
// PACKAGE_GRAPH.md §1.2. In particular it must not import kerrors, so the
// "no filesystem set" condition is raised as a package-local sentinel that the
// model / cmd layers map onto the Invocation error code.
//
// Two implementations are provided, mirroring the two open_fs() calls in
// model/Lab.py:79-81 — OSDir(path) for `osfs://<path>` and Memory() for `mem://`.
//
// The FilesystemMixin convenience methods live here as free functions
// (files.go, lines.go) instead of as methods hung on Lab and Machine.
package vfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// FS is the filesystem interface from PORT_SPEC §6, verbatim.
//
// Paths are slash-separated and unrooted in the io/fs sense ("a/b", never
// "/a/b" and never "."-relative escapes).
//
// The free functions in this package and the write-side methods below accept
// pyfilesystem-style rooted paths ("/a/b") and a bare "" for the root: they run
// CleanPath first, which clamps traversal at the root the way pyfilesystem's
// normpath does. The io/fs read side (Open, and the Stat/ReadDir of the
// fs.StatFS / fs.ReadDirFS implementations) does NOT: it keeps the io/fs
// contract and rejects anything fs.ValidPath rejects with fs.ErrInvalid, so a
// generic io/fs consumer sees the standard behaviour from every implementation
// here.
type FS interface {
	fs.FS

	// Create truncates-or-creates name and returns a writer for it.
	// It does NOT create parent directories: pyfilesystem's fs.open(p, "w")
	// raises ResourceNotFound when the parent is missing, and every
	// FilesystemMixin caller calls makedirs first.
	Create(name string) (io.WriteCloser, error)

	// MkdirAll is fs.makedirs(name, recreate=True).
	MkdirAll(name string, perm os.FileMode) error

	// Remove deletes a file or an empty directory.
	Remove(name string) error

	// SysPath reports the host path backing name, and whether one exists.
	// Memory() always returns ("", false); it is fs.hassyspath/getsyspath.
	SysPath(name string) (string, bool)
}

// Appender is the optional interface used by UpdateFileFrom* to reproduce
// pyfilesystem's fs.open(p, "a"). Both OSDir and Memory implement it.
// An FS that does not gets a read-modify-rewrite fallback with the same
// observable result.
type Appender interface {
	Append(name string) (io.WriteCloser, error)
}

// TypeNamer is the optional interface backing Type. It exists so that the §6
// FS interface stays exactly as the spec froze it while FilesystemMixin.fs_type()
// still has a home.
type TypeNamer interface {
	TypeName() string
}

// Python message strings for the Invocation code (ERROR_CODES.md §2 catalog).
// They are exported as constants rather than baked into the error values so
// that the CLI layer can reproduce them byte-exact without vfs importing kerrors.
const (
	MsgNoFilesystem       = "There is no filesystem associated to this object."
	MsgNoFilesystemCreate = "Cannot create a file if the filesystem is not set."
)

// InvocationError carries a frozen Python message. It is a distinct type (not
// errors.New) so the capitalised, punctuated Python text does not trip ST1005.
type InvocationError struct {
	Msg string
}

func (e *InvocationError) Error() string { return e.Msg }

// Is makes both no-filesystem sentinels answer errors.Is(err, ErrNoFilesystem),
// so callers need a single check while the two Python messages stay distinct.
func (e *InvocationError) Is(target error) bool {
	return target == ErrNoFilesystem &&
		(e == ErrNoFilesystem || e == ErrNoFilesystemCreate)
}

var (
	// ErrNoFilesystem is FilesystemMixin's "There is no filesystem associated
	// to this object." guard (FilesystemMixin.py:79,99,122,142,166,194,218,256,293).
	ErrNoFilesystem = &InvocationError{Msg: MsgNoFilesystem}

	// ErrNoFilesystemCreate is the one-off variant raised by
	// create_file_from_string (FilesystemMixin.py:56). errors.Is reports it as
	// ErrNoFilesystem too.
	ErrNoFilesystemCreate = &InvocationError{Msg: MsgNoFilesystemCreate}

	// ErrFileExpected mirrors fs.errors.FileExpected.
	ErrFileExpected = errors.New("path should be a file")

	// ErrDirectoryExpected mirrors fs.errors.DirectoryExpected.
	ErrDirectoryExpected = errors.New("path should be a directory")

	// ErrDirectoryNotEmpty mirrors fs.errors.DirectoryNotEmpty.
	ErrDirectoryNotEmpty = errors.New("directory is not empty")

	// ErrRemoveRoot mirrors fs.errors.RemoveRootError. FS.Remove covers both
	// fs.remove and fs.removedir, and pyfilesystem refuses the filesystem root
	// on either spelling (removedir("/") raises RemoveRootError; remove("/")
	// raises ResourceNotFound on MemoryFS and FileExpected on OSFS). Without
	// this, OSDir.Remove("") would delete the lab's own host directory.
	ErrRemoveRoot = errors.New("root directory may not be removed")
)

// CleanPath converts a pyfilesystem-style path into an io/fs path.
//
// Leading slashes are dropped ("/a/b" and "a/b" name the same resource, as in
// pyfilesystem), "." and ".." are resolved, and traversal is clamped at the
// root ("../../x" becomes "x"). The root is ".".
//
// DIVERGENCE (SPIKES/vfs.md §5.11, awaiting a ruling): the clamping is NOT
// what fs.path.normpath does. pyfilesystem REFUSES a path whose ".." escapes
// the root — normpath("/../../x") raises fs.errors.IllegalBackReference, and
// so create_file_from_string("x", "/../../esc.txt") writes nothing on both
// backends. This clamps instead, so the same call silently creates "esc.txt"
// at the root. Both are contained (neither can escape the filesystem root);
// the difference is a silent wrong-target write versus an error. Reachable
// only through a caller-supplied path on the §7 client API.
//
// Backslashes are NOT translated: pyfilesystem treats "\" as an ordinary
// filename byte on POSIX and only utils.pack_file_for_tar rewrites it.
func CleanPath(name string) (string, error) {
	cleaned := strings.TrimPrefix(path.Clean("/"+name), "/")
	if cleaned == "" {
		cleaned = "."
	}
	if !fs.ValidPath(cleaned) {
		return "", &fs.PathError{Op: "clean", Path: name, Err: fs.ErrInvalid}
	}
	return cleaned, nil
}

// parentDir returns the directory component of an already-cleaned path,
// reproducing os.path.dirname + pyfilesystem's "" == root convention.
func parentDir(cleaned string) string {
	dir := path.Dir(cleaned)
	if dir == "" {
		return "."
	}
	return dir
}

// Type is FilesystemMixin.fs_type(): "os", "memory", or "" for a nil FS.
func Type(fsys FS) string {
	if fsys == nil {
		return ""
	}
	if t, ok := fsys.(TypeNamer); ok {
		return t.TypeName()
	}
	return ""
}

// Path is FilesystemMixin.fs_path(): the host path of the filesystem root, or
// ("", false) when it has none.
//
// Divergence from pyfilesystem, deliberate: getsyspath("") returns a trailing
// separator ("/tmp/x/"); this returns the cleaned path ("/tmp/x"). The Python
// test itself normpaths the value before comparing.
func Path(fsys FS) (string, bool) {
	if fsys == nil {
		return "", false
	}
	return fsys.SysPath("")
}

// Exists reports whether name resolves to anything in fsys.
func Exists(fsys FS, name string) bool {
	if fsys == nil {
		return false
	}
	cleaned, err := CleanPath(name)
	if err != nil {
		return false
	}
	_, err = fs.Stat(fsys, cleaned)
	return err == nil
}

// IsDir reports whether name resolves to a directory in fsys.
func IsDir(fsys FS, name string) bool {
	if fsys == nil {
		return false
	}
	cleaned, err := CleanPath(name)
	if err != nil {
		return false
	}
	info, err := fs.Stat(fsys, cleaned)
	return err == nil && info.IsDir()
}

// IsEmpty is pyfilesystem's isempty: true when the directory has no entries.
func IsEmpty(fsys FS, name string) (bool, error) {
	if fsys == nil {
		return false, ErrNoFilesystem
	}
	cleaned, err := CleanPath(name)
	if err != nil {
		return false, err
	}
	entries, err := fs.ReadDir(fsys, cleaned)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// ReadFile reads name in full.
func ReadFile(fsys FS, name string) ([]byte, error) {
	if fsys == nil {
		return nil, ErrNoFilesystem
	}
	cleaned, err := CleanPath(name)
	if err != nil {
		return nil, err
	}
	if err := requireFile(fsys, cleaned); err != nil {
		return nil, err
	}
	return fs.ReadFile(fsys, cleaned)
}

// requireFile turns "missing" and "is a directory" into the pyfilesystem
// distinction the FilesystemMixin tests assert: ResourceNotFound vs FileExpected.
func requireFile(fsys FS, cleaned string) error {
	info, err := fs.Stat(fsys, cleaned)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &fs.PathError{Op: "open", Path: cleaned, Err: ErrFileExpected}
	}
	return nil
}

// writeAll writes b to name through fsys.Create, closing on every path.
func writeAll(fsys FS, cleaned string, b []byte) (err error) {
	w, err := fsys.Create(cleaned)
	if err != nil {
		return err
	}
	defer func() {
		cerr := w.Close()
		if err == nil {
			err = cerr
		}
	}()
	_, err = w.Write(b)
	return err
}
