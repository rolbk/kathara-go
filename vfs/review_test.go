package vfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Regression tests for the compatibility review findings. Every "want" below was
// re-derived against the live Kathara 3.8.3 / pyfilesystem2 stack through
// /root/kathara/pyvenv, on BOTH backends unless a row says otherwise.

// --- create_file_from_* with a trailing slash --------------------------------

func TestCreateWithTrailingSlashMakesADirectory(t *testing.T) {
	// Python computes the parent with os.path.dirname on the RAW dst_path, so
	// dirname("/a/b/") is "/a/b": makedirs creates the DIRECTORY /a/b and the
	// subsequent open("/a/b/", "w") raises FileExpected. Verified live on
	// mem:// and osfs://:
	//   create_file_from_string("x", "/a/b/") -> FileExpected, dirs [/a, /a/b]
	// Cleaning the path first would instead write a FILE at a/b and return nil.
	t.Run("directory is created and the write is refused", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			err := CreateFileFromString(fsys, "x", "/a/b/")
			if !errors.Is(err, ErrFileExpected) {
				t.Errorf("err = %v, want ErrFileExpected", err)
			}
			if !IsDir(fsys, "a/b") {
				t.Error("a/b is not a directory; the raw dirname was not used for makedirs")
			}
			files, err := Files(fsys, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 0 {
				t.Errorf("files = %v, want none", files)
			}
		})
	})
	// Same shape, one level deeper: dirs [/a, /a/b, /a/b/c].
	t.Run("nested trailing slash", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CreateFileFromString(fsys, "x", "/a/b/c/"); !errors.Is(err, ErrFileExpected) {
				t.Errorf("err = %v, want ErrFileExpected", err)
			}
			if !IsDir(fsys, "a/b/c") {
				t.Error("a/b/c is not a directory")
			}
		})
	})
	// With /a/b already a FILE the makedirs is what fails, so the error class
	// flips: DirectoryExpected, path '/a/b' should be a directory.
	t.Run("trailing slash over an existing file", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CreateFileFromString(fsys, "q", "/a/b"); err != nil {
				t.Fatal(err)
			}
			if err := CreateFileFromString(fsys, "x", "/a/b/"); !errors.Is(err, ErrDirectoryExpected) {
				t.Errorf("err = %v, want ErrDirectoryExpected", err)
			}
			assertContent(t, fsys, "a/b", "q")
		})
	})
	// The list flavour shares prepareCreate and behaves identically.
	t.Run("create_file_from_list too", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CreateFileFromList(fsys, []string{"x"}, "/a/b/"); !errors.Is(err, ErrFileExpected) {
				t.Errorf("err = %v, want ErrFileExpected", err)
			}
			if !IsDir(fsys, "a/b") {
				t.Error("a/b is not a directory")
			}
		})
	})
	// update_file_from_string has no makedirs and pyfilesystem normalises the
	// slash away, so "/a/" still appends to the file /a. Verified live:
	// b"aa" + "bb" -> b"aabb".
	t.Run("update ignores the trailing slash", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CreateFileFromString(fsys, "aa", "/a"); err != nil {
				t.Fatal(err)
			}
			if err := UpdateFileFromString(fsys, "bb", "/a/"); err != nil {
				t.Fatal(err)
			}
			assertContent(t, fsys, "a", "aabb")
		})
	})
}

func TestPosixDirname(t *testing.T) {
	// Byte-for-byte against CPython's posixpath.dirname, which is what
	// create_file_from_* feeds to makedirs.
	tests := []struct{ in, want string }{
		{"/a/b/", "/a/b"},
		{"/a/b", "/a"},
		{"test.txt", ""},
		{"", ""},
		{"/", "/"},
		{"a//b", "a"},
		{"/a/b//", "/a/b"},
		{"a/../b/", "a/../b"},
		{"//a", "//"},
		{"a/", "a"},
	}
	for _, tt := range tests {
		if got := posixDirname(tt.in); got != tt.want {
			t.Errorf("posixDirname(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// --- CopyDirectory follows symlinks ------------------------------------------

func TestCopyDirectoryFollowsSymlinks(t *testing.T) {
	// copy_dir walks an OSFS, and OSFS.scandir classifies entries with
	// os.DirEntry.is_dir(), which follows symlinks. Verified live on both
	// backends: a source with "linkdir -> real/" and "linkfile -> plain.txt"
	// copies through both, giving files
	//   /dst/linkdir/f.txt, /dst/linkfile, /dst/plain.txt, /dst/real/f.txt
	// and dirs /dst, /dst/linkdir, /dst/real.
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "real", "f.txt"), []byte("inreal"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "plain.txt"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(src, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("plain.txt", filepath.Join(src, "linkfile")); err != nil {
		t.Fatal(err)
	}
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CopyDirectory(fsys, src, "/dst"); err != nil {
			t.Fatal(err)
		}
		assertPaths(t, fsys, "dst",
			[]string{"dst/linkdir/f.txt", "dst/linkfile", "dst/plain.txt", "dst/real/f.txt"},
			[]string{"dst/linkdir", "dst/real"})
		assertContent(t, fsys, "dst/linkdir/f.txt", "inreal")
		assertContent(t, fsys, "dst/linkfile", "plain")
	})
}

func TestCopyDirectoryBrokenSymlink(t *testing.T) {
	// Python raises too (ResourceNotFound on mem://, and osfs:// only differs
	// in the partial state it leaves behind). The point is that it is an
	// error, not a silently skipped entry.
	src := t.TempDir()
	if err := os.Symlink("nowhere", filepath.Join(src, "broken")); err != nil {
		t.Fatal(err)
	}
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CopyDirectory(fsys, src, "/dst"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestCopyDirectorySymlinkLoopTerminates(t *testing.T) {
	// "loop -> ." recurses until the host stat hits ELOOP, exactly as Python
	// does (fs.errors.OperationFailed, [Errno 40] Too many levels of symbolic
	// links). The contract under test is that it terminates with an error
	// rather than spinning.
	src := t.TempDir()
	if err := os.Symlink(".", filepath.Join(src, "loop")); err != nil {
		t.Fatal(err)
	}
	fsys := Memory()
	if err := CopyDirectory(fsys, src, "/dst"); err == nil {
		t.Error("a symlink loop copied without error")
	}
}

// --- Remove on the filesystem root -------------------------------------------

func TestRemoveRootIsRefused(t *testing.T) {
	// pyfilesystem refuses on both spellings: removedir("/") is RemoveRootError
	// and remove("/") is ResourceNotFound (mem) / FileExpected (osfs). Before
	// the guard, OSDir.Remove("") deleted the lab's own host directory.
	for _, name := range []string{"", "/", "."} {
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := fsys.Remove(name); !errors.Is(err, ErrRemoveRoot) {
				t.Errorf("Remove(%q) = %v, want ErrRemoveRoot", name, err)
			}
		})
	}
}

func TestOSDirRemoveDoesNotDeleteItsOwnRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lab")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := OSDir(dir).Remove("/"); !errors.Is(err, ErrRemoveRoot) {
		t.Errorf("err = %v, want ErrRemoveRoot", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the host directory was deleted: %v", err)
	}
}

func TestRemoveNonEmptyDirectoryIsDirectoryNotEmpty(t *testing.T) {
	// fs.errors.DirectoryNotEmpty on both backends. OSDir used to surface the
	// raw syscall.ENOTEMPTY, which does not satisfy errors.Is(ErrDirectoryNotEmpty).
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "x", "/d/f.txt"); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Remove("/d"); !errors.Is(err, ErrDirectoryNotEmpty) {
			t.Errorf("err = %v, want ErrDirectoryNotEmpty", err)
		}
	})
}

// --- errno taxonomy ----------------------------------------------------------

func TestReadDirOnAFileIsDirectoryExpected(t *testing.T) {
	// isempty on a file raises fs.errors.DirectoryExpected on BOTH backends.
	// OSDir used to leak syscall.ENOTDIR here.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "x", "f.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := IsEmpty(fsys, "f.txt"); !errors.Is(err, ErrDirectoryExpected) {
			t.Errorf("IsEmpty(file): err = %v, want ErrDirectoryExpected", err)
		}
		if _, err := fsys.(fs.ReadDirFS).ReadDir("f.txt"); !errors.Is(err, ErrDirectoryExpected) {
			t.Errorf("ReadDir(file): err = %v, want ErrDirectoryExpected", err)
		}
	})
}

func TestPathComponentThatIsAFile(t *testing.T) {
	// With a file at /afile, every one of these raises ResourceNotFound on
	// both mem:// and osfs:// (verified live): open("r"|"w"|"a"), getinfo,
	// readbytes, update_file_from_string, write_line_before, delete_line.
	// Go must therefore report fs.ErrNotExist, not ENOTDIR and not
	// DirectoryExpected.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "data", "afile"); err != nil {
			t.Fatal(err)
		}
		if err := UpdateFileFromString(fsys, "x", "/afile/child"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("UpdateFileFromString: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := WriteLineBefore(fsys, "/afile/child", "X", "b", false); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("WriteLineBefore: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := DeleteLine(fsys, "/afile/child", "b", false); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("DeleteLine: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := ReadFile(fsys, "/afile/child"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := fs.Stat(fsys, "afile/child"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := fsys.Create("afile/child"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Create: err = %v, want fs.ErrNotExist", err)
		}
		// makedirs is the one directory-flavoured operation here, and
		// pyfilesystem's DIR_ERRORS maps ENOTDIR to DirectoryExpected — which
		// is what create_file_from_string("y", "/afile/b.txt") reports
		// (probe MD1) on both backends.
		if err := fsys.MkdirAll("afile/child", 0o755); !errors.Is(err, ErrDirectoryExpected) {
			t.Errorf("MkdirAll: err = %v, want ErrDirectoryExpected", err)
		}
		if err := CopyDirectory(fsys, t.TempDir(), "/afile/child"); !errors.Is(err, ErrDirectoryExpected) {
			t.Errorf("CopyDirectory: err = %v, want ErrDirectoryExpected", err)
		}
	})
}

// --- memFS entries exist from open time --------------------------------------

func TestMemoryCreateIsVisibleBeforeClose(t *testing.T) {
	// pyfilesystem creates and truncates the entry at open time. Verified live
	// on both backends: right after fs.open(p, "w") the file exists, reads
	// back b"" and lists in its parent; removedir on that parent then fails
	// with DirectoryNotEmpty.
	fsys := Memory()
	if err := CreateFileFromString(fsys, "AAAAA", "/a/f.txt"); err != nil {
		t.Fatal(err)
	}
	w, err := fsys.Create("a/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !Exists(fsys, "a/f.txt") {
		t.Error("the entry is not visible between open and close")
	}
	assertContent(t, fsys, "a/f.txt", "")
	entries, err := fs.ReadDir(fsys, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("ReadDir(a) = %d entries, want 1", len(entries))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCloseCannotResurrectUnderARemovedParent(t *testing.T) {
	// Sequence that used to leave an orphan: Create -> Remove(parent) -> Close
	// produced a node with no parent, reachable by Exists and ReadFile but
	// invisible to ReadDir and Walk. Python refuses the Remove instead
	// (DirectoryNotEmpty), because the entry already exists at open time.
	fsys := Memory()
	if err := fsys.MkdirAll("a", 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsys.Create("a/b")
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove("a"); !errors.Is(err, ErrDirectoryNotEmpty) {
		t.Errorf("Remove(a) = %v, want ErrDirectoryNotEmpty", err)
	}
	if _, err := w.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := Files(fsys, "")
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(files, []string{"a/b"}) {
		t.Errorf("Files = %v, want [a/b]; Exists says %v", files, Exists(fsys, "a/b"))
	}
}

func TestMemoryAppendCreatesTheEntryAtOpenTime(t *testing.T) {
	fsys := Memory()
	if err := fsys.MkdirAll("a", 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsys.(Appender).Append("a/b")
	if err != nil {
		t.Fatal(err)
	}
	if !Exists(fsys, "a/b") {
		t.Error("append did not create the entry at open time")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// --- Sub ---------------------------------------------------------------------

func TestSubOfTheRootIsStillASubFS(t *testing.T) {
	// pyfilesystem's opendir("") and opendir("/") both return a SubFS, so
	// fs_type() is "sub" (verified live on mem:// and osfs://), while
	// fs_path() still resolves to the parent's host directory.
	for _, dir := range []string{"", "/", "."} {
		forEachFS(t, func(t *testing.T, fsys FS) {
			sub, err := Sub(fsys, dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := Type(sub); got != "sub" {
				t.Errorf("Sub(%q) Type = %q, want \"sub\"", dir, got)
			}
			// The view still reaches the whole parent.
			if err := CreateFileFromString(sub, "x", "/f.txt"); err != nil {
				t.Fatal(err)
			}
			assertContent(t, fsys, "f.txt", "x")
			empty, err := IsEmpty(sub, "")
			if err != nil {
				t.Fatal(err)
			}
			if empty {
				t.Error("IsEmpty on the root view says empty after a write")
			}
		})
	}
}

func TestSubOfTheRootKeepsSysPath(t *testing.T) {
	dir := t.TempDir()
	sub, err := Sub(OSDir(dir), "/")
	if err != nil {
		t.Fatal(err)
	}
	p, ok := Path(sub)
	if !ok || p != filepath.Clean(dir) {
		t.Errorf("Path = %q, %v; want %q, true", p, ok, dir)
	}
	if _, ok := Path(Memory()); ok {
		t.Error("Memory reported a host path")
	}
}

func TestSubRejectsInvalidIOFSPaths(t *testing.T) {
	// io/fs requires Open to reject anything fs.ValidPath rejects. memFS and
	// osDir always did; subFS forgave rooted names, so the same package
	// answered a generic io/fs consumer three different ways.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := fsys.MkdirAll("pc1/etc", 0o755); err != nil {
			t.Fatal(err)
		}
		sub, err := Sub(fsys, "pc1")
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"/etc", "", "./etc", "etc/", "../etc"} {
			if _, err := sub.Open(bad); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Open(%q) = %v, want fs.ErrInvalid", bad, err)
			}
			if _, err := sub.(fs.StatFS).Stat(bad); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("Stat(%q) = %v, want fs.ErrInvalid", bad, err)
			}
			if _, err := sub.(fs.ReadDirFS).ReadDir(bad); !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("ReadDir(%q) = %v, want fs.ErrInvalid", bad, err)
			}
		}
		// The write side keeps taking pyfilesystem-style paths.
		if err := CreateFileFromString(sub, "x", "/etc/f.txt"); err != nil {
			t.Fatal(err)
		}
		assertContent(t, fsys, "pc1/etc/f.txt", "x")
	})
}

// noAppender wraps an FS and hides its Appender implementation, standing in for
// a third-party FS.
type noAppender struct{ FS }

func TestAppendFallbackSurvivesSub(t *testing.T) {
	// vfs.go documents that an FS without Appender gets a read-modify-rewrite
	// fallback "with the same observable result". A subFS always satisfies the
	// Appender assertion appendBytes makes, so the fallback used to be
	// unreachable through a Sub and UpdateFileFrom* failed with ErrInvalid.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := fsys.MkdirAll("pc1", 0o755); err != nil {
			t.Fatal(err)
		}
		plain := noAppender{fsys}
		if _, ok := any(plain).(Appender); ok {
			t.Fatal("noAppender still implements Appender")
		}
		sub, err := Sub(plain, "pc1")
		if err != nil {
			t.Fatal(err)
		}
		if err := CreateFileFromString(sub, "aa", "t.txt"); err != nil {
			t.Fatal(err)
		}
		if err := UpdateFileFromString(sub, "bb", "t.txt"); err != nil {
			t.Fatalf("UpdateFileFromString through a Sub of a non-Appender: %v", err)
		}
		assertContent(t, fsys, "pc1/t.txt", "aabb")
		// The direct (non-Sub) fallback keeps working too, including the
		// "creates a missing file" case.
		if err := UpdateFileFromString(plain, "hello", "new.txt"); err != nil {
			t.Fatal(err)
		}
		assertContent(t, fsys, "new.txt", "hello")
		if err := UpdateFileFromString(plain, "/nodir/x", "/nodir/x"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("missing parent through the fallback: err = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestAppendFallbackWriterIsIdempotentOnClose(t *testing.T) {
	fsys := Memory()
	if err := CreateFileFromString(fsys, "aa", "t.txt"); err != nil {
		t.Fatal(err)
	}
	w, err := openAppend(noAppender{fsys}, "t.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("bb")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	assertContent(t, fsys, "t.txt", "aabb")
	if _, err := w.Write([]byte("cc")); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Write after Close = %v, want fs.ErrClosed", err)
	}
	var _ io.WriteCloser = w
}

// --- the pinned RE2-vs-re residual gap ---------------------------------------

func TestPySearchDivergentCorpus(t *testing.T) {
	// testdata/pysearch_divergent.json was produced BY CPython 3.13 and is
	// concentrated on patterns whose `$` is NOT terminal — a class the 522-row
	// matrix contains none of, which is why it reported 0 disagreements.
	var rows []struct {
		Pattern string `json:"pattern"`
		Line    string `json:"line"`
		Match   bool   `json:"match"`
	}
	loadOracle(t, "testdata/pysearch_divergent.json", &rows)
	if len(rows) == 0 {
		t.Fatal("empty divergent corpus")
	}

	cache := map[string]*regexp.Regexp{}
	misses := 0
	for _, r := range rows {
		re, ok := cache[r.Pattern]
		if !ok {
			var err error
			re, err = regexp.Compile(r.Pattern)
			if err != nil {
				t.Fatalf("pattern %q does not compile under RE2: %v", r.Pattern, err)
			}
			cache[r.Pattern] = re
		}
		got := pySearch(re, r.Line)
		switch {
		case got == r.Match:
		case got && !r.Match:
			t.Errorf("pySearch(%q, %q) invented a match Python does not make", r.Pattern, r.Line)
		default:
			misses++
		}
	}
	if misses == 0 {
		t.Error("no row diverges any more: fold the closed mid-pattern $ gap into pysearch_matrix.json")
	}
	t.Logf("%d rows, %d known one-sided misses (mid-pattern $)", len(rows), misses)
}

func TestWriteLineBeforeMidPatternDollarMisses(t *testing.T) {
	// The concrete reachable consequence, spelled out so the divergence is
	// legible: Python returns 1 and inserts, Go returns 0 and only rewrites.
	// Python (live, both backends):
	//   write_line_before("/f", "X", "b$\n") on b"a\nb\nd\n"
	//     -> 1, b"a\nX\nb\nd\n"
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "a\nb\nd\n", "/f"); err != nil {
			t.Fatal(err)
		}
		n, err := WriteLineBefore(fsys, "/f", "X", "b$\n", false)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("n = %d; the documented gap says 0 here (Python says 1)", n)
		}
		assertContent(t, fsys, "f", "a\nb\nd\n")
	})
}

// --- the back-reference divergence found by the compatibility review ----------------

func TestBackReferenceIsClampedNotRefused(t *testing.T) {
	// pyfilesystem's normpath refuses any ".." that escapes the root:
	//   normpath("/../../x")  -> IllegalBackReference
	//   normpath("..")        -> IllegalBackReference
	//   normpath("a/../..")   -> IllegalBackReference
	// so create_file_from_string("x", "/../../esc.txt") writes NOTHING and
	// raises, on both mem:// and osfs:// (verified live on both backends).
	// CleanPath clamps instead, so the same call succeeds and creates the file
	// at the root under a DIFFERENT name than the caller asked for. Neither
	// behaviour can escape the filesystem root, so this is a silent
	// wrong-target write rather than a containment hole.
	// This test pins the current behavior so it cannot drift silently.
	t.Run("CleanPath clamps where pyfilesystem raises", func(t *testing.T) {
		for _, in := range []string{"/../../x", "..", "a/../..", "/..", "../../x"} {
			got, err := CleanPath(in)
			if err != nil {
				t.Fatalf("CleanPath(%q) = %v; the divergence is that it does NOT error", in, err)
			}
			if got == "" {
				t.Fatalf("CleanPath(%q) returned an empty path", in)
			}
		}
	})
	t.Run("the write lands at the root instead of erroring", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			// Python: IllegalBackReference, no file created.
			if err := CreateFileFromString(fsys, "x", "/../../esc.txt"); err != nil {
				t.Fatalf("err = %v; documented divergence says nil here", err)
			}
			assertContent(t, fsys, "esc.txt", "x")
			// Containment still holds: nothing escaped the root.
			files, err := Files(fsys, "")
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != 1 || files[0] != "esc.txt" {
				t.Errorf("files = %v, want exactly [esc.txt] — nothing may escape the root", files)
			}
		})
	})
}

func TestCopyDirectoryMissingSourceErrorClass(t *testing.T) {
	forEachFS(t, func(t *testing.T, fsys FS) {
		err := CopyDirectory(fsys, filepath.Join(t.TempDir(), "nothere"), "/dst")
		if err == nil {
			t.Fatal("want an error for a missing source directory")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("err = %v, want fs.ErrNotExist (Python: CreateFailed)", err)
		}
	})
}
