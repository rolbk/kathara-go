package vfs

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// dirPerm is the mode handed to MkdirAll by the FilesystemMixin creators.
// pyfilesystem's makedirs takes no mode and lands on 0777&~umask for OSFS;
// 0755 is the closest fixed value and matches what Kathara-created lab
// directories look like in practice.
const dirPerm os.FileMode = 0o755

// CreateFileFromString is FilesystemMixin.create_file_from_string:
// makedirs(dirname(dst), recreate=True) then open(dst, "w") — i.e. create or
// TRUNCATE, never append.
//
// A nil FS yields ErrNoFilesystemCreate, whose message is the one-off
// "Cannot create a file if the filesystem is not set." (FilesystemMixin.py:56).
func CreateFileFromString(fsys FS, content, dstPath string) error {
	return createBytes(fsys, []byte(content), dstPath, ErrNoFilesystemCreate)
}

// CreateFileFromList is create_file_from_list: every element gets a trailing
// "\n", including the last one.
func CreateFileFromList(fsys FS, lines []string, dstPath string) error {
	return createBytes(fsys, joinLines(lines), dstPath, ErrNoFilesystem)
}

// UpdateFileFromString is update_file_from_string: open(dst, "a") — APPEND.
//
// It deliberately does not makedirs: Python does not either, so a missing
// parent directory surfaces as fs.ErrNotExist (probe P6b) while a missing file
// in an existing directory is created (probe P6).
func UpdateFileFromString(fsys FS, content, dstPath string) error {
	return appendBytes(fsys, []byte(content), dstPath)
}

// UpdateFileFromList is update_file_from_list: appends every element with a
// trailing "\n".
func UpdateFileFromList(fsys FS, lines []string, dstPath string) error {
	return appendBytes(fsys, joinLines(lines), dstPath)
}

// CreateFileFromPath is create_file_from_path: the host file at srcPath is
// copied into fsys byte-for-byte (Python opens it "rb"; no CRLF or BOM
// rewriting happens here — that is pack_file_for_tar's job, not this one).
func CreateFileFromPath(fsys FS, srcPath, dstPath string) error {
	if fsys == nil {
		return ErrNoFilesystem
	}
	cleaned, err := prepareCreate(fsys, dstPath)
	if err != nil {
		return err
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	return copyInto(fsys, cleaned, src)
}

// CreateFileFromStream is create_file_from_stream: the reader is drained into
// dstPath verbatim.
//
// Python branches on stream.mode and, for a text-mode stream, lets the host
// open() apply universal-newline translation before writing. Go readers carry
// no mode, so this is the binary branch only; the text branch's translation is
// the caller's job. The write-only-stream test
// (test_create_file_from_stream_unsupported_operation) is DROP per
// EXPECTATIONS-core.md §5.
func CreateFileFromStream(fsys FS, stream io.Reader, dstPath string) error {
	if fsys == nil {
		return ErrNoFilesystem
	}
	cleaned, err := prepareCreate(fsys, dstPath)
	if err != nil {
		return err
	}
	return copyInto(fsys, cleaned, stream)
}

// CopyDirectory is copy_directory_from_path: the host directory tree at
// srcPath is copied into fsys at dstPath.
//
// Verified against fs.copy.copy_dir (probes CD1-CD3):
//   - dstPath is created when missing, and merged into when it already exists;
//   - files with colliding names are overwritten, unrelated files survive;
//   - empty source directories are reproduced as empty directories;
//   - a missing srcPath is an error (Python: CreateFailed from open_fs).
//
// The walk visits in lexical order, so the resulting FS is identical on every
// run; Python's copy_dir uses scandir order, which is unobservable in the
// result.
func CopyDirectory(fsys FS, srcPath, dstPath string) error {
	if fsys == nil {
		return ErrNoFilesystem
	}
	abs, err := filepath.Abs(srcPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return &fs.PathError{Op: "opendir", Path: srcPath, Err: ErrDirectoryExpected}
	}
	dst, err := CleanPath(dstPath)
	if err != nil {
		return err
	}
	if err := fsys.MkdirAll(dst, dirPerm); err != nil {
		return err
	}
	return copyHostDir(fsys, abs, dst)
}

// copyHostDir is the recursion behind CopyDirectory.
//
// It classifies each entry with os.Stat, which FOLLOWS symlinks, because that
// is what the Python side does: copy_dir walks an OSFS, OSFS.scandir asks
// os.DirEntry.is_dir(), and that follows by default. Verified live on both a
// mem:// and an osfs:// destination — a source holding "linkdir -> real/" and
// "linkfile -> plain.txt" copies through both links, yielding /dst/linkdir/f.txt
// and /dst/linkfile.
//
// os.DirFS + fs.WalkDir cannot express this: it reports a symlink as a non-dir
// entry, so a link to a directory would be opened as a file and the copy would
// abort with EISDIR partway through the tree.
//
// A symlink loop terminates the way Python's does: os.Stat eventually fails
// with ELOOP (Python surfaces it as fs.errors.OperationFailed) and the error
// propagates. A broken symlink is an error on both sides too.
//
// os.ReadDir returns entries sorted by name, so parents are created before
// their contents and the traversal is deterministic.
func copyHostDir(fsys FS, hostDir, dst string) error {
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		hostChild := filepath.Join(hostDir, e.Name())
		target := path.Join(dst, e.Name())
		info, err := os.Stat(hostChild)
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := fsys.MkdirAll(target, dirPerm); err != nil {
				return err
			}
			if err := copyHostDir(fsys, hostChild, target); err != nil {
				return err
			}
			continue
		}
		if err := copyHostFile(fsys, hostChild, target); err != nil {
			return err
		}
	}
	return nil
}

func copyHostFile(fsys FS, hostPath, target string) error {
	in, err := os.Open(hostPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	return copyInto(fsys, target, in)
}

// --- shared plumbing -------------------------------------------------------

func joinLines(lines []string) []byte {
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// prepareCreate is the `makedirs(dirname(dst), recreate=True)` prologue shared
// by every create_file_from_* helper.
//
// The parent comes from posixDirname on the RAW dst_path, not from the cleaned
// one, because that is what Python does: os.path.dirname runs before the path
// ever reaches pyfilesystem's normpath. The two disagree exactly when dst_path
// carries a trailing slash, and the difference is observable — verified live on
// both backends:
//
//	create_file_from_string("x", "/a/b/")  makedirs("/a/b") creates the
//	                                       DIRECTORY /a/b, then open("/a/b/",
//	                                       "w") raises FileExpected
//	create_file_from_string("x", "/a/b/")  where /a/b is already a file:
//	                                       makedirs raises DirectoryExpected
//
// Cleaning first would instead create /a and quietly write a FILE at /a/b.
// Both outcomes fall out of this ordering: Create sees the directory it just
// made and returns ErrFileExpected, MkdirAll sees the file and returns
// ErrDirectoryExpected.
func prepareCreate(fsys FS, dstPath string) (string, error) {
	cleaned, err := CleanPath(dstPath)
	if err != nil {
		return "", err
	}
	parent, err := CleanPath(posixDirname(dstPath))
	if err != nil {
		return "", err
	}
	if err := fsys.MkdirAll(parent, dirPerm); err != nil {
		return "", err
	}
	return cleaned, nil
}

// posixDirname is os.path.dirname (posixpath.dirname) on an untouched path:
// everything up to and including the last "/", with trailing slashes stripped
// unless the head is all slashes. Kept byte-for-byte with CPython because the
// create_file_from_* helpers feed its result straight to makedirs.
//
//	"/a/b/" -> "/a/b"    "/a/b" -> "/a"    "a//b" -> "a"
//	"a/"    -> "a"       "/"    -> "/"     "//a"  -> "//"
//	"x.txt" -> ""        ""     -> ""
func posixDirname(p string) string {
	i := strings.LastIndexByte(p, '/') + 1
	head := p[:i]
	if strings.Trim(head, "/") != "" { // CPython: head and head != sep*len(head)
		head = strings.TrimRight(head, "/")
	}
	return head
}

func createBytes(fsys FS, content []byte, dstPath string, nilErr error) error {
	if fsys == nil {
		return nilErr
	}
	cleaned, err := prepareCreate(fsys, dstPath)
	if err != nil {
		return err
	}
	return writeAll(fsys, cleaned, content)
}

func appendBytes(fsys FS, content []byte, dstPath string) error {
	if fsys == nil {
		return ErrNoFilesystem
	}
	cleaned, err := CleanPath(dstPath)
	if err != nil {
		return err
	}
	return writeThrough(func() (io.WriteCloser, error) { return openAppend(fsys, cleaned) }, content)
}

// openAppend is fs.open(p, "a") on an arbitrary FS.
//
// An FS that implements Appender gets a real O_APPEND handle. Anything else
// gets the read-modify-rewrite fallback the Appender doc contract promises:
// same observable result, without the atomicity. It lives here, rather than
// inline in appendBytes, so that subFS.Append can reach it too — a sub view
// always satisfies the Appender assertion, so without this the fallback would
// be unreachable for a Sub of a third-party FS and UpdateFileFrom* would fail
// outright on one.
func openAppend(fsys FS, cleaned string) (io.WriteCloser, error) {
	if a, ok := fsys.(Appender); ok {
		return a.Append(cleaned)
	}
	existing, err := fs.ReadFile(fsys, cleaned)
	if err != nil && !isNotExist(err) {
		return nil, err
	}
	w := &appendFallback{fsys: fsys, name: cleaned}
	w.buf.Write(existing)
	return w, nil
}

// appendFallback buffers the pre-existing content plus everything written and
// rewrites the whole file on Close.
type appendFallback struct {
	fsys   FS
	name   string
	buf    bytes.Buffer
	closed bool
}

func (w *appendFallback) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fs.ErrClosed
	}
	return w.buf.Write(p)
}

func (w *appendFallback) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return writeAll(w.fsys, w.name, w.buf.Bytes())
}

func writeThrough(open func() (io.WriteCloser, error), content []byte) (err error) {
	w, err := open()
	if err != nil {
		return err
	}
	defer func() {
		cerr := w.Close()
		if err == nil {
			err = cerr
		}
	}()
	_, err = w.Write(content)
	return err
}

func copyInto(fsys FS, cleaned string, r io.Reader) (err error) {
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
	_, err = io.Copy(w, r)
	return err
}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
