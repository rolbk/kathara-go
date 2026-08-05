package vfs

import (
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
// fs.WalkDir visits in lexical order, so the resulting FS is identical on
// every run; Python's copy_dir uses scandir order, which is unobservable in
// the result.
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

	// fs.WalkDir visits in lexical order and always reaches a directory before
	// its contents, so parents exist by the time their files are written.
	src := os.DirFS(abs)
	return fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == "." {
			return err
		}
		target := path.Join(dst, p)
		if d.IsDir() {
			return fsys.MkdirAll(target, dirPerm)
		}
		return copyOne(fsys, src, p, target)
	})
}

func copyOne(fsys FS, src fs.FS, srcName, target string) error {
	in, err := src.Open(srcName)
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
func prepareCreate(fsys FS, dstPath string) (string, error) {
	cleaned, err := CleanPath(dstPath)
	if err != nil {
		return "", err
	}
	if err := fsys.MkdirAll(parentDir(cleaned), dirPerm); err != nil {
		return "", err
	}
	return cleaned, nil
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
	if a, ok := fsys.(Appender); ok {
		return writeThrough(func() (io.WriteCloser, error) { return a.Append(cleaned) }, content)
	}
	// Fallback for third-party FS implementations: read-modify-rewrite.
	// Same observable result, without the atomicity of a real O_APPEND.
	existing, err := fs.ReadFile(fsys, cleaned)
	if err != nil && !isNotExist(err) {
		return err
	}
	return writeAll(fsys, cleaned, append(existing, content...))
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
