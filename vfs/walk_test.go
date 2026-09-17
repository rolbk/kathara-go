package vfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"syscall"
	"testing"
)

func TestWalkFilesAndDirs(t *testing.T) {
	tests := []struct {
		name      string
		root      string
		wantFiles []string
		wantDirs  []string
	}{
		{
			name:      "whole tree from the root",
			root:      "",
			wantFiles: []string{"a/b/deep.txt", "a/x.txt", "top.txt"},
			wantDirs:  []string{"a", "a/b", "empty"},
		},
		{
			name:      "subtree",
			root:      "a",
			wantFiles: []string{"a/b/deep.txt", "a/x.txt"},
			wantDirs:  []string{"a/b"},
		},
		{
			name:      "leaf directory",
			root:      "a/b",
			wantFiles: []string{"a/b/deep.txt"},
			wantDirs:  nil,
		},
		{
			name:      "empty directory",
			root:      "empty",
			wantFiles: nil,
			wantDirs:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				seedTree(t, fsys)
				assertPaths(t, fsys, tt.root, tt.wantFiles, tt.wantDirs)
			})
		})
	}
}

func TestWalkOrderIsDeterministic(t *testing.T) {
	// ReadDir is name-sorted on both implementations, so the visit order is
	// the same every run and the same on mem:// and osfs://. Python's
	// fs.walk uses scandir order, which is filesystem-dependent; nothing in
	// the port may depend on it (see the FolderParser ruling in
	// PROPOSED-DIVERGENCES.md).
	want := []string{".", "a", "a/b", "a/b/deep.txt", "a/x.txt", "empty", "top.txt"}
	forEachFS(t, func(t *testing.T, fsys FS) {
		seedTree(t, fsys)
		for range 3 {
			var got []string
			err := Walk(fsys, "", func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				got = append(got, p)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !equalStrings(got, want) {
				t.Fatalf("walk order = %v, want %v", got, want)
			}
		}
	})
}

func TestWalkErrors(t *testing.T) {
	if err := Walk(nil, "", func(string, fs.DirEntry, error) error { return nil }); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("Walk(nil): err = %v, want ErrNoFilesystem", err)
	}
	forEachFS(t, func(t *testing.T, fsys FS) {
		seedTree(t, fsys)
		if _, err := Files(fsys, "missing"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Files on a missing root: err = %v, want fs.ErrNotExist", err)
		}
	})
	if _, err := Files(nil, ""); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("Files(nil): err = %v, want ErrNoFilesystem", err)
	}
	if _, err := Dirs(nil, ""); !errors.Is(err, ErrNoFilesystem) {
		t.Errorf("Dirs(nil): err = %v, want ErrNoFilesystem", err)
	}
}

func TestReadDirIsSorted(t *testing.T) {
	forEachFS(t, func(t *testing.T, fsys FS) {
		for _, n := range []string{"z.txt", "a.txt", "M.txt"} {
			if err := CreateFileFromString(fsys, "x", n); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		if !equalStrings(got, []string{"M.txt", "a.txt", "z.txt"}) {
			t.Errorf("ReadDir = %v, want sorted", got)
		}
	})
}

func seedTree(t *testing.T, fsys FS) {
	t.Helper()
	for path, content := range map[string]string{
		"top.txt":      "top",
		"a/x.txt":      "x",
		"a/b/deep.txt": "deep",
	} {
		if err := CreateFileFromString(fsys, content, path); err != nil {
			t.Fatal(err)
		}
	}
	if err := fsys.MkdirAll("empty", 0o755); err != nil {
		t.Fatal(err)
	}
}

// symlinkFS builds a host directory holding the shapes that separate a
// following walk from a non-following one, and returns it as an osfs:// FS
// together with its host path.
func symlinkFS(t *testing.T) (FS, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Creating a symlink on Windows needs either developer mode or
		// SeCreateSymbolicLinkPrivilege, and Windows runtime is out of 1.0
		// scope (PORT_SPEC §0.3).
		t.Skip("symlinks are not creatable unprivileged on Windows")
	}
	root := t.TempDir()
	fsys := OSDir(root)
	if err := CreateFileFromString(fsys, "inner", "real/f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := CreateFileFromString(fsys, "plain", "plain.txt"); err != nil {
		t.Fatal(err)
	}
	return fsys, root
}

func symlink(t *testing.T, target, linkPath string) {
	t.Helper()
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("symlink %s -> %s: %v", linkPath, target, err)
	}
}

// walkedEntry is one visit, reduced to what the classification decides.
type walkedEntry struct {
	Name  string
	IsDir bool
}

func walkedWith(t *testing.T, walk func(FS, string, fs.WalkDirFunc) error, fsys FS) ([]walkedEntry, error) {
	t.Helper()
	var got []walkedEntry
	err := walk(fsys, "", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		got = append(got, walkedEntry{Name: name, IsDir: entry.IsDir()})
		return nil
	})
	return got, err
}

// TestWalkFollowDereferencesSymlinkedDirectory is the pyfilesystem
// classification: `OSFS._scandir` types each entry with
// `os.DirEntry.is_dir()`, which follows. Oracle: on this tree
// `Walker().dirs()` is `['/linkdir', '/real']` and `copy_fs` produces
// `/linkdir/f.txt` next to `/real/f.txt`.
func TestWalkFollowDereferencesSymlinkedDirectory(t *testing.T) {
	fsys, root := symlinkFS(t)
	symlink(t, "real", filepath.Join(root, "linkdir"))
	symlink(t, "plain.txt", filepath.Join(root, "linkfile"))

	got, err := walkedWith(t, WalkFollow, fsys)
	if err != nil {
		t.Fatalf("WalkFollow: %v", err)
	}
	want := []walkedEntry{
		{".", true},
		{"linkdir", true},
		{"linkdir/f.txt", false},
		{"linkfile", false},
		{"plain.txt", false},
		{"real", true},
		{"real/f.txt", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WalkFollow = %v, want %v", got, want)
	}

	// The contrast that makes the helper necessary: the entry-type walk calls
	// the link a plain file and never descends.
	plain, err := walkedWith(t, Walk, fsys)
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if slices.Contains(plain, walkedEntry{"linkdir", true}) {
		t.Errorf("Walk followed the symlink: %v", plain)
	}

	// Files/Dirs are fs.walk.files/fs.walk.dirs and follow too.
	files, err := Files(fsys, "")
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if wantFiles := []string{"linkdir/f.txt", "linkfile", "plain.txt", "real/f.txt"}; !equalStrings(files, wantFiles) {
		t.Errorf("Files = %v, want %v", files, wantFiles)
	}
	dirs, err := Dirs(fsys, "")
	if err != nil {
		t.Fatalf("Dirs: %v", err)
	}
	if wantDirs := []string{"linkdir", "real"}; !equalStrings(dirs, wantDirs) {
		t.Errorf("Dirs = %v, want %v", dirs, wantDirs)
	}
}

// TestWalkFollowBrokenSymlinkIsAFile is `DirEntry.is_dir()` returning False for
// a dangling link rather than raising: the walker lists it among the files and
// the failure arrives only when something opens it. Oracle: `Walker().files()`
// on this tree is `['/ok.txt', '/dangling']` and `copy_fs` then dies with
// `ResourceNotFound: resource '/dangling' not found`.
func TestWalkFollowBrokenSymlinkIsAFile(t *testing.T) {
	fsys, root := symlinkFS(t)
	symlink(t, "nowhere", filepath.Join(root, "dangling"))

	got, err := walkedWith(t, WalkFollow, fsys)
	if err != nil {
		t.Fatalf("WalkFollow: %v", err)
	}
	if !slices.Contains(got, walkedEntry{"dangling", false}) {
		t.Errorf("WalkFollow = %v, want the dangling link listed as a file", got)
	}
	if _, err := ReadFile(fsys, "dangling"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(dangling) = %v, want fs.ErrNotExist", err)
	}
}

// TestWalkFollowSymlinkLoopFailsWithELOOP pins the absence of cycle detection,
// which Python has none of either: the stat that classifies the entry
// eventually refuses the path with ELOOP and the walk aborts, exactly as
// `DirEntry.is_dir()` raising `OSError: [Errno 40]` aborts pyfilesystem's
// (verified live: `fs.errors.OperationFailed, [Errno 40] Too many levels of
// symbolic links`).
func TestWalkFollowSymlinkLoopFailsWithELOOP(t *testing.T) {
	t.Run("mutual", func(t *testing.T) {
		fsys, root := symlinkFS(t)
		symlink(t, "b", filepath.Join(root, "a"))
		symlink(t, "a", filepath.Join(root, "b"))

		if _, err := walkedWith(t, WalkFollow, fsys); !errors.Is(err, syscall.ELOOP) {
			t.Errorf("WalkFollow over a mutual loop = %v, want ELOOP", err)
		}
	})

	t.Run("self", func(t *testing.T) {
		fsys, root := symlinkFS(t)
		symlink(t, ".", filepath.Join(root, "self"))

		if _, err := walkedWith(t, WalkFollow, fsys); !errors.Is(err, syscall.ELOOP) {
			t.Errorf("WalkFollow over a self-loop = %v, want ELOOP", err)
		}
	})
}
