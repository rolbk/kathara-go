//go:build unix

package util

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// probeTree rebuilds, byte for byte, the symlink corpus tools/vectorcheck/
// utils_probe.py builds before it records the realpath vectors (its
// build_tree). The layout matters — dangling links, a two-node loop, a
// self-link, relative and absolute targets, a link to a link — because each
// one exercises a different branch of posixpath.realpath.
func probeTree(t *testing.T) (root string, parent string) {
	t.Helper()

	root, err := os.MkdirTemp("/tmp", "kathara-utils-probe-")
	if err != nil {
		t.Skipf("cannot create the probe root under /tmp: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("cleaning up %s: %v", root, err)
		}
	})

	parent = filepath.Dir(root)
	if parent != "/tmp" || filepath.Dir(parent) != "/" {
		t.Skipf("probe root %s is not two levels deep; the recorded vectors assume it is", root)
	}
	// A symlinked /tmp (as on macOS) would resolve away and every expected
	// path would move with it.
	if resolved, err := realPath(root); err != nil || resolved != root {
		t.Skipf("probe root %s does not resolve to itself (%s, %v)", root, resolved, err)
	}

	mkdirAll := func(elem ...string) {
		if err := os.MkdirAll(filepath.Join(append([]string{root}, elem...)...), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	writeFile := func(content string, elem ...string) {
		if err := os.WriteFile(filepath.Join(append([]string{root}, elem...)...), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	symlink := func(target string, elem ...string) {
		if err := os.Symlink(target, filepath.Join(append([]string{root}, elem...)...)); err != nil {
			t.Fatalf("symlink: %v", err)
		}
	}

	mkdirAll("real", "sub")
	writeFile("x", "real", "file.txt")
	writeFile("y", "real", "sub", "deep.txt")
	symlink("real", "link_dir")
	symlink("real/file.txt", "link_file")
	symlink(filepath.Join(root, "real"), "abs_link")
	symlink("nowhere", "dangling")
	symlink("loop2", "loop1")
	symlink("loop1", "loop2")
	symlink("self", "self")
	symlink("../real/sub", "real", "up_link")
	symlink("link_dir", "link_to_link")
	mkdirAll("nested", "a", "b")
	symlink("../../real", "nested", "a", "to_real")

	return root, parent
}

// expandRoot substitutes the placeholders the probe writes into the fixture
// for the paths of this run's tree.
func expandRoot(s, root, parent string) string {
	s = strings.ReplaceAll(s, "{ROOTPARENT}", parent)
	return strings.ReplaceAll(s, "{ROOT}", root)
}

func TestRealPathAndGetAbsolutePath(t *testing.T) {
	fixture := loadFixture(t)
	root, parent := probeTree(t)

	// The probe ran its relative cases from inside the tree.
	t.Chdir(root)

	for _, tc := range fixture.Realpath {
		t.Run(tc.Input, func(t *testing.T) {
			if tc.Error != "" {
				t.Fatalf("fixture records an unexpected error for %q: %s", tc.Input, tc.Error)
			}

			input := expandRoot(tc.Input, root, parent)
			wantReal := expandRoot(tc.Realpath, root, parent)
			wantOut := expandRoot(tc.Output, root, parent)

			gotReal, err := realPath(input)
			if err != nil {
				t.Fatalf("realPath(%q): %v", input, err)
			}
			if gotReal != wantReal {
				t.Errorf("realPath(%q) = %q, oracle says %q", input, gotReal, wantReal)
			}

			gotOut, err := GetAbsolutePath(input)
			if err != nil {
				t.Fatalf("GetAbsolutePath(%q): %v", input, err)
			}
			if gotOut != wantOut {
				t.Errorf("GetAbsolutePath(%q) = %q, oracle says %q", input, gotOut, wantOut)
			}
		})
	}
}

// TestGetAbsolutePathSymlinkLoopReturnsRelative pins compatibility behavior for
// [GetAbsolutePath]: for a symlink loop the function returns the link's raw
// target, which for a relative target is not an absolute path at all. Every
// other input class returns an absolute one.
func TestGetAbsolutePathSymlinkLoopReturnsRelative(t *testing.T) {
	root, _ := probeTree(t)

	got, err := GetAbsolutePath(filepath.Join(root, "loop1"))
	if err != nil {
		t.Fatalf("GetAbsolutePath: %v", err)
	}
	if got != "loop2" {
		t.Errorf("GetAbsolutePath of a loop = %q, want the raw target %q", got, "loop2")
	}

	got, err = GetAbsolutePath(filepath.Join(root, "self"))
	if err != nil {
		t.Fatalf("GetAbsolutePath: %v", err)
	}
	if got != "self" {
		t.Errorf("GetAbsolutePath of a self-link = %q, want %q", got, "self")
	}

	// A link that is not part of a loop resolves through, so the branch is
	// unreachable for it.
	for _, name := range []string{"link_dir", "link_file", "abs_link", "link_to_link", "dangling"} {
		got, err := GetAbsolutePath(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("GetAbsolutePath(%s): %v", name, err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("GetAbsolutePath(%s) = %q, which is not absolute", name, got)
		}
	}
}

// TestRealPathSurvivesMissingPaths pins the property filepath.EvalSymlinks
// does not have and that every `kathara lstart -d /nope` depends on: a path
// that does not exist resolves rather than erroring.
func TestRealPathSurvivesMissingPaths(t *testing.T) {
	root, _ := probeTree(t)

	for _, input := range []string{
		filepath.Join(root, "missing"),
		filepath.Join(root, "missing", "deeper", "still"),
		filepath.Join(root, "dangling", "under", "a", "dangling", "link"),
	} {
		got, err := realPath(input)
		if err != nil {
			t.Fatalf("realPath(%q): %v", input, err)
		}
		if !filepath.IsAbs(got) {
			t.Errorf("realPath(%q) = %q, which is not absolute", input, got)
		}
	}

	if _, err := filepath.EvalSymlinks(filepath.Join(root, "missing")); err == nil {
		t.Error("filepath.EvalSymlinks now tolerates a missing path; realPath may be replaceable")
	}
}

// TestRealPathDotDotIsLexicalOnTheResolvedPrefix pins the ordering that makes
// the algorithm a component walk rather than a normalise-then-resolve: `..`
// applies to where the link *points*, not to the link's own parent.
func TestRealPathDotDotIsLexicalOnTheResolvedPrefix(t *testing.T) {
	root, _ := probeTree(t)

	// link_dir -> real, so link_dir/../real/file.txt walks up out of `real`
	// (to the root) and back down, rather than up out of the root.
	got, err := realPath(filepath.Join(root, "link_dir", "..", "real", "file.txt"))
	if err != nil {
		t.Fatalf("realPath: %v", err)
	}
	if want := filepath.Join(root, "real", "file.txt"); got != want {
		t.Errorf("realPath = %q, want %q", got, want)
	}
}

func TestGetExecutablePath(t *testing.T) {
	fixture := loadFixture(t)
	root, parent := probeTree(t)

	exe := filepath.Join(root, "kathara-bin")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Chdir(root)

	for _, tc := range fixture.ExecutablePath {
		t.Run(tc.Input, func(t *testing.T) {
			input := expandRoot(tc.Input, root, parent)

			got, err := GetExecutablePath(input)

			if tc.Output == nil {
				if !errors.Is(err, ErrExecutableNotFound) {
					t.Fatalf("GetExecutablePath(%q) = (%q, %v), oracle says None", input, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetExecutablePath(%q): %v", input, err)
			}

			want := expandRoot(*tc.Output, root, parent)
			if want == *tc.Output && !strings.Contains(*tc.Output, "{ROOT") {
				// A PATH lookup: the recorded absolute path is this host's
				// only if it still has the same tool in the same place.
				unquoted := strings.Trim(want, `"`)
				if _, statErr := os.Stat(unquoted); statErr != nil {
					if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
						t.Fatalf("GetExecutablePath(%q) = %q, which is not quoted", input, got)
					}
					t.Skipf("oracle found %s, which does not exist here; got %s", want, got)
				}
			}

			if got != want {
				t.Errorf("GetExecutablePath(%q) = %q, oracle says %q", input, got, want)
			}
		})
	}
}

// TestGetExecutablePathQuotesAndIgnoresTheExecuteBit pins the two properties
// its callers depend on: the result carries its own double quotes because it
// is concatenated into a shell command line, and the first branch tests only
// "is a regular file", so a non-executable file at the given path wins over a
// working binary on PATH.
func TestGetExecutablePathQuotesAndIgnoresTheExecuteBit(t *testing.T) {
	root, _ := probeTree(t)

	plain := filepath.Join(root, "plain.txt")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := GetExecutablePath(plain)
	if err != nil {
		t.Fatalf("GetExecutablePath: %v", err)
	}
	if got != `"`+plain+`"` {
		t.Errorf("GetExecutablePath(%q) = %q, want it quoted", plain, got)
	}

	// A directory is not a regular file and falls through to the PATH search,
	// which cannot match an absolute path to a directory either.
	if _, err := GetExecutablePath(filepath.Join(root, "real")); !errors.Is(err, ErrExecutableNotFound) {
		t.Errorf("a directory must not resolve: %v", err)
	}
}
