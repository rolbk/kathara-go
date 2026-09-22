package vfs

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
)

// forEachFS runs fn against a fresh Memory() and a fresh OSDir() over t.TempDir().
// Both must behave identically for every FilesystemMixin free function; the
// only sanctioned difference is SysPath.
func forEachFS(t *testing.T, fn func(*testing.T, FS)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { fn(t, Memory()) })
	t.Run("osdir", func(t *testing.T) { fn(t, OSDir(t.TempDir())) })
}

func TestCleanPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty is the root", "", "."},
		{"slash is the root", "/", "."},
		{"dot is the root", ".", "."},
		{"leading slash dropped", "/a/b", "a/b"},
		{"already unrooted", "a/b", "a/b"},
		{"duplicate separators collapsed", "//a///b", "a/b"},
		{"trailing separator dropped", "/a/b/", "a/b"},
		{"dot segments resolved", "/a/./b", "a/b"},
		{"parent segments resolved", "/a/b/../c", "a/c"},
		{"escape clamped at the root", "../../x", "x"},
		{"escape clamped from an unrooted path", "a/../../x", "x"},
		{"backslash is an ordinary byte", `a\b`, `a\b`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CleanPath(tt.in)
			if err != nil {
				t.Fatalf("CleanPath(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("CleanPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTypeAndPath(t *testing.T) {

	t.Run("nil fs", func(t *testing.T) {
		if got := Type(nil); got != "" {
			t.Errorf("Type(nil) = %q, want empty", got)
		}
		if p, ok := Path(nil); ok || p != "" {
			t.Errorf("Path(nil) = %q, %v; want \"\", false", p, ok)
		}
	})
	t.Run("os", func(t *testing.T) {
		dir := t.TempDir()
		fsys := OSDir(dir)
		if got := Type(fsys); got != "os" {
			t.Errorf("Type = %q, want \"os\"", got)
		}
		p, ok := Path(fsys)
		if !ok {
			t.Fatal("Path reported no host path for an OSDir")
		}
		if filepath.Clean(p) != filepath.Clean(dir) {
			t.Errorf("Path = %q, want %q", p, dir)
		}
	})
	t.Run("memory", func(t *testing.T) {
		fsys := Memory()
		if got := Type(fsys); got != "memory" {
			t.Errorf("Type = %q, want \"memory\"", got)
		}
		if p, ok := Path(fsys); ok || p != "" {
			t.Errorf("Path = %q, %v; want \"\", false", p, ok)
		}
	})
	// pyfilesystem's opendir returns a SubFS, so fs_type() is "sub" whatever
	// backs it, while getsyspath still resolves to the real host directory.
	// Verified on both a mem:// and an osfs:// parent:
	//   mem  -> fs_type "sub", fs_path None
	//   osfs -> fs_type "sub", fs_path /tmp/…/pc1
	t.Run("sub over memory", func(t *testing.T) {
		fsys := Memory()
		if err := fsys.MkdirAll("pc1", 0o755); err != nil {
			t.Fatal(err)
		}
		sub, err := Sub(fsys, "pc1")
		if err != nil {
			t.Fatal(err)
		}
		if got := Type(sub); got != "sub" {
			t.Errorf("Type = %q, want \"sub\"", got)
		}
		if p, ok := Path(sub); ok || p != "" {
			t.Errorf("Path = %q, %v; want \"\", false", p, ok)
		}
	})
	t.Run("sub over an os dir", func(t *testing.T) {
		dir := t.TempDir()
		fsys := OSDir(dir)
		if err := fsys.MkdirAll("pc1", 0o755); err != nil {
			t.Fatal(err)
		}
		sub, err := Sub(fsys, "pc1")
		if err != nil {
			t.Fatal(err)
		}
		if got := Type(sub); got != "sub" {
			t.Errorf("Type = %q, want \"sub\"", got)
		}
		p, ok := Path(sub)
		if !ok {
			t.Fatal("Path reported no host path for an OSDir-backed sub")
		}
		if p != filepath.Join(dir, "pc1") {
			t.Errorf("Path = %q, want %q", p, filepath.Join(dir, "pc1"))
		}
	})
}

func TestSysPathResolvesUnderTheRoot(t *testing.T) {
	dir := t.TempDir()
	fsys := OSDir(dir)
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"root", "", dir},
		{"file", "a.txt", filepath.Join(dir, "a.txt")},
		{"rooted path", "/a/b.txt", filepath.Join(dir, "a", "b.txt")},
		{"escape clamped", "../../etc/passwd", filepath.Join(dir, "etc", "passwd")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := fsys.SysPath(tt.in)
			if !ok {
				t.Fatal("SysPath reported no host path")
			}
			if got != tt.want {
				t.Errorf("SysPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCreateDoesNotMakeParents(t *testing.T) {
	// pyfilesystem's fs.open(p, "w") raises ResourceNotFound when the parent
	// is missing (verified P6b via the "a" mode); every FilesystemMixin
	// creator calls makedirs first, which is why the free functions do too.
	forEachFS(t, func(t *testing.T, fsys FS) {
		_, err := fsys.Create("nodir/x.txt")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Create in a missing directory: err = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestCreateOntoDirectory(t *testing.T) {
	// verified P13: create_file_from_string onto a directory raises FileExpected.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := fsys.MkdirAll("d", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := CreateFileFromString(fsys, "x", "d"); !errors.Is(err, ErrFileExpected) {
			t.Errorf("err = %v, want ErrFileExpected", err)
		}
		// verified P13b: the append path raises FileExpected too.
		if err := UpdateFileFromString(fsys, "x", "d"); !errors.Is(err, ErrFileExpected) {
			t.Errorf("update: err = %v, want ErrFileExpected", err)
		}
	})
}

func TestMkdirAllThroughAFile(t *testing.T) {
	// verified MD1: makedirs across an existing file raises DirectoryExpected.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "x", "/a"); err != nil {
			t.Fatal(err)
		}
		if err := CreateFileFromString(fsys, "y", "/a/b.txt"); !errors.Is(err, ErrDirectoryExpected) {
			t.Errorf("err = %v, want ErrDirectoryExpected", err)
		}
	})
}

func TestMkdirAllIsRecreateTrue(t *testing.T) {
	forEachFS(t, func(t *testing.T, fsys FS) {
		for range 2 {
			if err := fsys.MkdirAll("a/b/c", 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
		}
		if !IsDir(fsys, "a/b/c") {
			t.Error("a/b/c is not a directory")
		}
	})
}

func TestExistsAndIsDir(t *testing.T) {
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "x", "/a/b.txt"); err != nil {
			t.Fatal(err)
		}
		tests := []struct {
			name       string
			path       string
			wantExists bool
			wantDir    bool
		}{
			{"file", "/a/b.txt", true, false},
			{"file without the leading slash", "a/b.txt", true, false},
			{"parent directory", "/a", true, true},
			{"root", "/", true, true},
			{"missing", "/nope", false, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := Exists(fsys, tt.path); got != tt.wantExists {
					t.Errorf("Exists(%q) = %v, want %v", tt.path, got, tt.wantExists)
				}
				if got := IsDir(fsys, tt.path); got != tt.wantDir {
					t.Errorf("IsDir(%q) = %v, want %v", tt.path, got, tt.wantDir)
				}
			})
		}
		if Exists(nil, "/a") {
			t.Error("Exists(nil) = true")
		}
	})
}

func TestRemove(t *testing.T) {
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "x", "/d/f.txt"); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Remove("/d"); err == nil {
			t.Error("Remove on a non-empty directory succeeded")
		}
		if err := fsys.Remove("/d/f.txt"); err != nil {
			t.Fatalf("Remove file: %v", err)
		}
		if Exists(fsys, "/d/f.txt") {
			t.Error("file still present after Remove")
		}
		if err := fsys.Remove("/d"); err != nil {
			t.Fatalf("Remove empty directory: %v", err)
		}
		if err := fsys.Remove("/d"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove missing: err = %v, want fs.ErrNotExist", err)
		}
	})
}

func TestSub(t *testing.T) {
	// fs.opendir, used by model/Machine.py:80 to give a device its own view.
	forEachFS(t, func(t *testing.T, fsys FS) {
		if err := CreateFileFromString(fsys, "hi", "/pc1/etc/f.txt"); err != nil {
			t.Fatal(err)
		}
		sub, err := Sub(fsys, "pc1")
		if err != nil {
			t.Fatal(err)
		}
		got, err := ReadFile(sub, "etc/f.txt")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hi" {
			t.Errorf("content = %q, want %q", got, "hi")
		}
		if err := CreateFileFromString(sub, "new", "/n.txt"); err != nil {
			t.Fatal(err)
		}
		outer, err := ReadFile(fsys, "/pc1/n.txt")
		if err != nil {
			t.Fatal(err)
		}
		if string(outer) != "new" {
			t.Errorf("write through the sub view landed as %q", outer)
		}
		if _, err := Sub(fsys, "missing"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Sub on a missing directory: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := Sub(fsys, "/pc1/etc/f.txt"); !errors.Is(err, ErrDirectoryExpected) {
			t.Errorf("Sub on a file: err = %v, want ErrDirectoryExpected", err)
		}
		if _, err := Sub(nil, "x"); !errors.Is(err, ErrNoFilesystem) {
			t.Errorf("Sub(nil): err = %v, want ErrNoFilesystem", err)
		}
	})
}

func TestNoFilesystemMessages(t *testing.T) {

	if ErrNoFilesystem.Error() != "There is no filesystem associated to this object." {
		t.Errorf("ErrNoFilesystem = %q", ErrNoFilesystem.Error())
	}
	if ErrNoFilesystemCreate.Error() != "Cannot create a file if the filesystem is not set." {
		t.Errorf("ErrNoFilesystemCreate = %q", ErrNoFilesystemCreate.Error())
	}
	// The create-specific variant still answers the general check, so callers
	// need one errors.Is.
	if !errors.Is(ErrNoFilesystemCreate, ErrNoFilesystem) {
		t.Error("ErrNoFilesystemCreate is not an ErrNoFilesystem")
	}
	if errors.Is(ErrNoFilesystem, ErrNoFilesystemCreate) {
		t.Error("ErrNoFilesystem must not be reported as the create variant")
	}
}

func TestIsEmpty(t *testing.T) {
	forEachFS(t, func(t *testing.T, fsys FS) {
		empty, err := IsEmpty(fsys, "")
		if err != nil {
			t.Fatal(err)
		}
		if !empty {
			t.Error("a fresh filesystem is not empty")
		}
		if err := CreateFileFromString(fsys, "x", "f.txt"); err != nil {
			t.Fatal(err)
		}
		empty, err = IsEmpty(fsys, "")
		if err != nil {
			t.Fatal(err)
		}
		if empty {
			t.Error("filesystem reported empty after a write")
		}
		if _, err := IsEmpty(nil, ""); !errors.Is(err, ErrNoFilesystem) {
			t.Errorf("IsEmpty(nil): err = %v, want ErrNoFilesystem", err)
		}
	})
}
