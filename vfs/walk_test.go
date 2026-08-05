package vfs

import (
	"errors"
	"io/fs"
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
