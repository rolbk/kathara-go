//go:build unix

package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// whichTree rebuilds the corpus tools/vectorcheck/utils_probe.py's probe_which
// builds: two directories holding the same executable name, a third holding a
// non-executable file, a directory named like a command and a symlink to a
// real executable, plus a nested directory for the relative-command cases.
func whichTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	base := filepath.Join(root, "which")

	for _, name := range []string{"bin1", "bin2", "bin3", "bin1/sub", "bin3/adir"} {
		if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	for _, name := range []string{"bin1/tool", "bin2/tool", "bin1/sub/tool"} {
		if err := os.WriteFile(filepath.Join(base, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(base, "bin3", "noexec"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "bin1", "tool"), filepath.Join(base, "bin3", "linked")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	return root
}

// TestPyWhich is the shutil.which port against the oracle. Four of the rows
// are the reason this implementation exists rather than a call to exec.LookPath: a "."
// PATH entry answers "./tool" and not "tool", a doubled separator survives
// into the answer, and a `..` through a missing directory fails the access
// check instead of being folded away — all three because posixpath.join does
// not normalise and filepath.Join does.
func TestPyWhich(t *testing.T) {
	fixture := loadFixture(t)
	if len(fixture.Which) == 0 {
		t.Fatal("the fixture carries no which vectors")
	}

	root := whichTree(t)
	t.Chdir(filepath.Join(root, "which", "bin1"))

	for _, tc := range fixture.Which {
		name := tc.Cmd + "|"
		if tc.Path != nil {
			name += *tc.Path
		} else {
			name += "<unset>"
		}

		t.Run(name, func(t *testing.T) {
			// t.Setenv arms the restore even for the case that then removes
			// the variable entirely.
			t.Setenv("PATH", "")
			if tc.Path == nil {
				if err := os.Unsetenv("PATH"); err != nil {
					t.Fatalf("unsetting PATH: %v", err)
				}
			} else {
				t.Setenv("PATH", strings.ReplaceAll(*tc.Path, "{ROOT}", root))
			}

			got := pyWhich(strings.ReplaceAll(tc.Cmd, "{ROOT}", root))

			if tc.Output == nil {
				if got != "" {
					t.Errorf("pyWhich(%q) = %q, oracle says None", tc.Cmd, got)
				}
				return
			}
			if want := strings.ReplaceAll(*tc.Output, "{ROOT}", root); got != want {
				t.Errorf("pyWhich(%q) = %q, oracle says %q", tc.Cmd, got, want)
			}
		})
	}
}

// TestPyWhichSearchesUnsetPathByDefault pins the distinction bpo-35755 draws
// and that Go's exec.LookPath collapses: an *unset* PATH falls back to the
// platform default, while a PATH set to the empty string finds nothing.
func TestPyWhichSearchesUnsetPathByDefault(t *testing.T) {
	root := whichTree(t)
	t.Chdir(filepath.Join(root, "which", "bin1"))

	t.Setenv("PATH", "")
	if got := pyWhich("tool"); got != "" {
		t.Errorf("pyWhich with an empty PATH = %q, want no match", got)
	}

	if err := os.Unsetenv("PATH"); err != nil {
		t.Fatalf("unsetting PATH: %v", err)
	}
	// Nothing named "tool" lives in the default path, but "sh" does, and the
	// answer must be joined onto a default entry rather than found in ".".
	if got := pyWhich("sh"); got != "" && !strings.HasPrefix(got, "/") {
		t.Errorf("pyWhich(\"sh\") with PATH unset = %q, want an entry of %q", got, defaultSearchPath)
	}
}
