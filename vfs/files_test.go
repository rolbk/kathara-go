package vfs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateFileFromString(t *testing.T) {
	// create = makedirs(dirname, recreate=True) + open(dst, "w").
	// The "w" is what makes it a TRUNCATE, never an append (verified P10).
	tests := []struct {
		name    string
		writes  []string
		dstPath string
		want    string
	}{
		{"bare name at the root", []string{"x"}, "test.txt", "x"},
		// verified P7: os.path.dirname("test.txt") is "", and pyfilesystem
		// treats makedirs("") as the root, so this is not an error.
		{"rooted name", []string{"x"}, "/test.txt", "x"},
		// verified P7b: parents are created.
		{"nested parents created", []string{"x"}, "/a/b/c.txt", "x"},
		// verified P10: the second call replaces, not appends.
		{"second call truncates", []string{"aaaaaaaaaa", "b"}, "test.txt", "b"},
		{"empty content", []string{""}, "test.txt", ""},
		{"content is written verbatim", []string{"a\r\nb\x00c"}, "test.txt", "a\r\nb\x00c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				for _, w := range tt.writes {
					if err := CreateFileFromString(fsys, w, tt.dstPath); err != nil {
						t.Fatal(err)
					}
				}
				got, err := ReadFile(fsys, tt.dstPath)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tt.want {
					t.Errorf("content = %q, want %q", got, tt.want)
				}
			})
		})
	}

	t.Run("nil fs uses the create-specific message", func(t *testing.T) {
		err := CreateFileFromString(nil, "x", "p")
		if !errors.Is(err, ErrNoFilesystem) {
			t.Errorf("err = %v, want ErrNoFilesystem", err)
		}
		if err.Error() != MsgNoFilesystemCreate {
			t.Errorf("message = %q, want %q", err.Error(), MsgNoFilesystemCreate)
		}
	})
}

func TestCreateFileFromList(t *testing.T) {
	// writelines(line + '\n' for line in lines): every element gets a
	// terminator, including the last.
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{"single line", []string{"test"}, "test\n"},
		{"several lines", []string{"a", "b"}, "a\nb\n"},
		{"empty list writes nothing", nil, ""},
		{"empty elements still get terminators", []string{"", ""}, "\n\n"},
		{"elements already ending in a newline get another",
			[]string{"a\n"}, "a\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				if err := CreateFileFromList(fsys, tt.lines, "/x/f.txt"); err != nil {
					t.Fatal(err)
				}
				got, err := ReadFile(fsys, "/x/f.txt")
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tt.want {
					t.Errorf("content = %q, want %q", got, tt.want)
				}
			})
		})
	}
	if err := CreateFileFromList(nil, []string{"x"}, "p"); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("nil fs: err = %v, want ErrNoFilesystem", err)
	}
}

func TestUpdateFileFromString(t *testing.T) {
	// update = open(dst, "a"). It APPENDS, and it does NOT makedirs.
	t.Run("appends to an existing file", func(t *testing.T) {
		// verified P11: "aa" then update "bb" gives b"aabb" — no separator.
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CreateFileFromString(fsys, "aa", "t.txt"); err != nil {
				t.Fatal(err)
			}
			if err := UpdateFileFromString(fsys, "bb", "t.txt"); err != nil {
				t.Fatal(err)
			}
			got, err := ReadFile(fsys, "t.txt")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "aabb" {
				t.Errorf("content = %q, want %q", got, "aabb")
			}
		})
	})
	t.Run("creates a missing file in an existing directory", func(t *testing.T) {
		// verified P6: mode "a" creates.
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := UpdateFileFromString(fsys, "hello", "new.txt"); err != nil {
				t.Fatal(err)
			}
			got, err := ReadFile(fsys, "new.txt")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "hello" {
				t.Errorf("content = %q, want %q", got, "hello")
			}
		})
	})
	t.Run("missing parent directory is an error", func(t *testing.T) {
		// verified P6b: fs.errors.ResourceNotFound — update does not makedirs.
		forEachFS(t, func(t *testing.T, fsys FS) {
			err := UpdateFileFromString(fsys, "hello", "/nodir/new.txt")
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("err = %v, want fs.ErrNotExist", err)
			}
		})
	})
	t.Run("nil fs", func(t *testing.T) {
		err := UpdateFileFromString(nil, "x", "p")
		if !errors.Is(err, ErrNoFilesystem) {
			t.Errorf("err = %v, want ErrNoFilesystem", err)
		}
		if err.Error() != MsgNoFilesystem {
			t.Errorf("message = %q, want %q", err.Error(), MsgNoFilesystem)
		}
	})
}

func TestUpdateFileFromList(t *testing.T) {
	// verified P12: create ["a","b"] then update ["c"] gives b"a\nb\nc\n".
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromList(fsys, []string{"a", "b"}, "t.txt"); err != nil {
			t.Fatal(err)
		}
		if err := UpdateFileFromList(fsys, []string{"c"}, "t.txt"); err != nil {
			t.Fatal(err)
		}
		got, err := ReadFile(fsys, "t.txt")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "a\nb\nc\n" {
			t.Errorf("content = %q, want %q", got, "a\nb\nc\n")
		}
	})
	if err := UpdateFileFromList(nil, []string{"x"}, "p"); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("nil fs: err = %v, want ErrNoFilesystem", err)
	}
}

func TestCreateFileFromPath(t *testing.T) {
	// Python opens the host file "rb" and uploads it: no CRLF collapse, no BOM
	// strip, no binary sniff (verified CP1 — b"\x00\x01CRLF\r\nend" survives
	// byte for byte). That normalisation belongs to pack_file_for_tar.
	tests := []struct {
		name    string
		content []byte
	}{
		{"binary content survives", []byte{0, 1, 'C', 'R', 'L', 'F', '\r', '\n', 'e'}},
		{"crlf is not collapsed", []byte("a\r\nb\r\n")},
		{"utf-8 BOM is not stripped", []byte("\xef\xbb\xbfBOM here\n")},
		{"empty file", []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "src.bin")
			if err := os.WriteFile(src, tt.content, 0o644); err != nil {
				t.Fatal(err)
			}
			forEachFS(t, func(t *testing.T, fsys FS) {
				if err := CreateFileFromPath(fsys, src, "/sub/out.bin"); err != nil {
					t.Fatal(err)
				}
				got, err := ReadFile(fsys, "/sub/out.bin")
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, tt.content) {
					t.Errorf("content = %q, want %q", got, tt.content)
				}
			})
		})
	}
	t.Run("missing source", func(t *testing.T) {
		// verified CP2: FileNotFoundError from the host open().
		forEachFS(t, func(t *testing.T, fsys FS) {
			err := CreateFileFromPath(fsys, filepath.Join(t.TempDir(), "nope"), "/out")
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("err = %v, want fs.ErrNotExist", err)
			}
		})
	})
	if err := CreateFileFromPath(nil, "/x", "p"); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("nil fs: err = %v, want ErrNoFilesystem", err)
	}
}

func TestCreateFileFromStream(t *testing.T) {
	// verified CS2 (binary branch): the reader is drained verbatim, parents
	// are created first. The Python text branch relies on the host open()'s
	// universal-newline translation, which has no Go equivalent — the caller
	// translates.
	tests := []struct {
		name string
		in   string
	}{
		{"text", "a\nb\n"},
		{"crlf preserved", "a\r\nb\n"},
		{"empty", ""},
		{"binary", "\x00\x01\xff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				if err := CreateFileFromStream(fsys, strings.NewReader(tt.in), "/o/f.txt"); err != nil {
					t.Fatal(err)
				}
				got, err := ReadFile(fsys, "/o/f.txt")
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != tt.in {
					t.Errorf("content = %q, want %q", got, tt.in)
				}
			})
		})
	}
	if err := CreateFileFromStream(nil, strings.NewReader("x"), "p"); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("nil fs: err = %v, want ErrNoFilesystem", err)
	}
}

// hostTree builds a directory tree on the host for the CopyDirectory vectors.
func hostTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mkdir := func(p string) {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, c string) {
		if err := os.WriteFile(filepath.Join(root, p), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("a/b")
	mkdir("empty")
	write("top.txt", "top")
	write("a/x.txt", "x")
	write("a/b/y.bin", "\x00y")
	return root
}

func TestCopyDirectory(t *testing.T) {
	// verified CD1: copy_dir merges into an existing destination, overwrites
	// colliding files, leaves unrelated files alone, and reproduces empty
	// directories. Files: /dst/a/b/y.bin, /dst/a/x.txt, /dst/keep.txt,
	// /dst/top.txt. Dirs: /dst, /dst/a, /dst/a/b, /dst/empty.
	// /dst/top.txt reads b"top", i.e. the source overwrote the pre-existing
	// "PRE" content.
	t.Run("merge into an existing destination", func(t *testing.T) {
		src := hostTree(t)
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CreateFileFromString(fsys, "PRE", "/dst/top.txt"); err != nil {
				t.Fatal(err)
			}
			if err := CreateFileFromString(fsys, "KEEP", "/dst/keep.txt"); err != nil {
				t.Fatal(err)
			}
			if err := CopyDirectory(fsys, src, "/dst"); err != nil {
				t.Fatal(err)
			}
			assertPaths(t, fsys, "dst",
				[]string{"dst/a/b/y.bin", "dst/a/x.txt", "dst/keep.txt", "dst/top.txt"},
				[]string{"dst/a", "dst/a/b", "dst/empty"})
			assertContent(t, fsys, "dst/top.txt", "top")
			assertContent(t, fsys, "dst/keep.txt", "KEEP")
			assertContent(t, fsys, "dst/a/b/y.bin", "\x00y")
		})
	})
	// verified CD2: a missing destination is created.
	t.Run("destination created when missing", func(t *testing.T) {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "t.txt"), []byte("t"), 0o644); err != nil {
			t.Fatal(err)
		}
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CopyDirectory(fsys, src, "/newdst"); err != nil {
				t.Fatal(err)
			}
			assertPaths(t, fsys, "newdst", []string{"newdst/t.txt"}, nil)
		})
	})
	// verified CD3: fs.errors.CreateFailed — open_fs on a missing root.
	t.Run("missing source is an error", func(t *testing.T) {
		forEachFS(t, func(t *testing.T, fsys FS) {
			err := CopyDirectory(fsys, filepath.Join(t.TempDir(), "nope"), "/dst")
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("err = %v, want fs.ErrNotExist", err)
			}
		})
	})
	t.Run("source that is a file", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "f.txt")
		if err := os.WriteFile(src, []byte("f"), 0o644); err != nil {
			t.Fatal(err)
		}
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CopyDirectory(fsys, src, "/dst"); !errors.Is(err, ErrDirectoryExpected) {
				t.Errorf("err = %v, want ErrDirectoryExpected", err)
			}
		})
	})
	t.Run("copy to the root", func(t *testing.T) {
		src := hostTree(t)
		forEachFS(t, func(t *testing.T, fsys FS) {
			if err := CopyDirectory(fsys, src, ""); err != nil {
				t.Fatal(err)
			}
			assertPaths(t, fsys, "",
				[]string{"a/b/y.bin", "a/x.txt", "top.txt"},
				[]string{"a", "a/b", "empty"})
		})
	})
	if err := CopyDirectory(nil, "/x", "p"); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("nil fs: err = %v, want ErrNoFilesystem", err)
	}
}

func assertPaths(t *testing.T, fsys FS, root string, wantFiles, wantDirs []string) {
	t.Helper()
	files, err := Files(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(files, wantFiles) {
		t.Errorf("files = %v, want %v", files, wantFiles)
	}
	dirs, err := Dirs(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	if !equalStrings(dirs, wantDirs) {
		t.Errorf("dirs = %v, want %v", dirs, wantDirs)
	}
}

func assertContent(t *testing.T, fsys FS, path, want string) {
	t.Helper()
	got, err := ReadFile(fsys, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
