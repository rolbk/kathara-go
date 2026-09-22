//go:build unix

package util

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// permsDirModes is the set of directory modes the probe creates
// (utils_probe.py's probe_perms), keyed the way the fixture names them.
var permsDirModes = map[string]fs.FileMode{
	"d_000": 0o000,
	"d_100": 0o100,
	"d_200": 0o200,
	"d_400": 0o400,
	"d_500": 0o500,
	"d_700": 0o700,
	"d_755": 0o755,
}

func permsTree(t *testing.T) string {
	t.Helper()

	base := filepath.Join(t.TempDir(), "perms")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	for name, mode := range permsDirModes {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		// Restore before t.TempDir's own cleanup runs, or a 0000 directory
		// cannot be removed by a non-root test.
		t.Cleanup(func() {
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Errorf("restoring %s: %v", dir, err)
			}
		})
	}

	if err := os.WriteFile(filepath.Join(base, "afile"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	return base
}

func TestCheckDirectoryPermissions(t *testing.T) {
	fixture := loadFixture(t)

	// access(2) answers for the caller's own credentials, so the recorded
	// results only describe this host if it is the same kind of user. Root in
	// particular is missing nothing, ever.
	if os.Geteuid() != fixture.Permissions.Euid {
		t.Skipf("fixture was recorded as euid %d, this test runs as %d",
			fixture.Permissions.Euid, os.Geteuid())
	}

	base := permsTree(t)

	for _, tc := range fixture.Permissions.Cases {
		t.Run(tc.Dir+"/"+tc.Mode, func(t *testing.T) {
			path := filepath.Join(base, tc.Dir)
			switch tc.Dir {
			case "file":
				path = filepath.Join(base, "afile")
			case "missing":
				path = filepath.Join(base, "nope")
			}

			missing, err := CheckDirectoryPermissions(path, tc.Mode)

			if tc.Error != "" {
				if err == nil {
					t.Fatalf("CheckDirectoryPermissions(%q, %q) = %v, oracle raised %s",
						path, tc.Mode, missing, tc.Error)
				}
				// The recorded message embeds the probe's own root.
				wantSuffix := strings.TrimPrefix(pythonError(tc.Error), "Path `")
				wantSuffix = wantSuffix[strings.Index(wantSuffix, "` "):]
				if !strings.HasSuffix(err.Error(), wantSuffix) {
					t.Errorf("error = %q, oracle says %q", err, pythonError(tc.Error))
				}
				if !strings.HasPrefix(err.Error(), "Path `"+path+"`") {
					t.Errorf("error = %q, want it to name %q", err, path)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckDirectoryPermissions(%q, %q): %v", path, tc.Mode, err)
			}
			if !equalStrings(missing, tc.Missing) {
				t.Errorf("CheckDirectoryPermissions(%q, %q) = %v, oracle says %v",
					path, tc.Mode, missing, tc.Missing)
			}
		})
	}
}

// TestCheckDirectoryPermissionsErrors pins the two failures and their codes,
// including the inverted name Python gives the first one: a path that does
// *not* exist raises FileExistsError.
func TestCheckDirectoryPermissionsErrors(t *testing.T) {
	base := permsTree(t)

	missingPath := filepath.Join(base, "nope")
	_, err := CheckDirectoryPermissions(missingPath, "ro")
	if err == nil {
		t.Fatal("a missing path must be an error")
	}
	if err.Error() != "Path `"+missingPath+"` does not exist." {
		t.Errorf("error = %q", err)
	}
	if !errors.Is(err, kerrors.ErrFileExists) {
		t.Error("error is not classified FileExists")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Error("error must wrap fs.ErrNotExist despite the FileExists name")
	}

	filePath := filepath.Join(base, "afile")
	_, err = CheckDirectoryPermissions(filePath, "ro")
	if err == nil {
		t.Fatal("a regular file must be an error")
	}
	if err.Error() != "Path `"+filePath+"` must be a directory." {
		t.Errorf("error = %q", err)
	}
	if !errors.Is(err, kerrors.ErrNotADirectory) {
		t.Error("error is not classified NotADirectory")
	}
}

// TestCheckDirectoryPermissionsUnprivileged is the oracle coverage the section
// above cannot give. The probe re-runs its whole matrix in a child that has
// dropped to uid 65534, which owns nothing in the tree and is therefore
// genuinely missing permissions; those rows pin the report order and the label
// strings against Python instead of against this port's own unit test.
func TestCheckDirectoryPermissionsUnprivileged(t *testing.T) {
	fixture := loadFixture(t)
	if fixture.PermsUnprivileged == nil {
		t.Skip("the fixture was recorded by a probe that could not drop privileges")
	}
	if len(fixture.PermsUnprivileged.Cases) == 0 {
		t.Fatal("the unprivileged section carries no cases")
	}
	if fixture.PermsUnprivileged.Euid == 0 {
		t.Fatalf("the unprivileged section was recorded as euid 0")
	}

	// The bits access(2) consults for a user who is neither owner nor group,
	// in the r, w, x order of permissionFlags.
	otherBits := [3]int{0o004, 0o002, 0o001}

	sawMissing := false
	for _, tc := range fixture.PermsUnprivileged.Cases {
		t.Run(tc.Dir+"/"+tc.Mode, func(t *testing.T) {
			if tc.Error != "" {
				t.Fatalf("fixture records an unexpected error: %s", tc.Error)
			}

			got := missingPermissions(tc.Mode, func(i int) bool {
				return tc.DirMode&otherBits[i] != 0
			})
			if !equalStrings(got, tc.Missing) {
				t.Errorf("missingPermissions(%q) on mode %#o = %v, oracle says %v",
					tc.Mode, tc.DirMode, got, tc.Missing)
			}
		})
		if len(tc.Missing) > 0 {
			sawMissing = true
		}
	}

	if !sawMissing {
		t.Error("no recorded row is missing anything; the section proves nothing")
	}
}

// TestMissingPermissionsOrderAndModeScan pins the two things the error message
// depends on and that the root-euid fixture section cannot show, because root
// is never missing anything: the report order is read, write, execute however
// the mode string spells them, and `mode` is scanned for characters rather
// than parsed, so "ro" asks for read alone.
func TestMissingPermissionsOrderAndModeScan(t *testing.T) {
	denyAll := func(int) bool { return false }

	for _, tc := range []struct {
		mode string
		want []string
	}{
		{"rwx", []string{"read (r)", "write (w)", "execute (x)"}},
		{"xwr", []string{"read (r)", "write (w)", "execute (x)"}},
		{"ro", []string{"read (r)"}},
		{"rw", []string{"read (r)", "write (w)"}},
		{"wx", []string{"write (w)", "execute (x)"}},
		{"rx", []string{"read (r)", "execute (x)"}},
		{"w", []string{"write (w)"}},
		{"", nil},
		{"o", nil},
	} {
		got := missingPermissions(tc.mode, denyAll)
		if !equalStrings(got, tc.want) {
			t.Errorf("missingPermissions(%q, denyAll) = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// TestMissingPermissionsProbesOnlyWhatWasAsked pins that a letter absent from
// the mode is never probed: on Windows each probe is a file open, and probing
// for write on a read-only mount would be a visible side effect.
func TestMissingPermissionsProbesOnlyWhatWasAsked(t *testing.T) {
	var probed []int
	record := func(i int) bool {
		probed = append(probed, i)
		return true
	}

	missingPermissions("rx", record)

	if len(probed) != 2 || probed[0] != 0 || probed[1] != 2 {
		t.Errorf("probed %v, want the read and execute indices in order", probed)
	}
}

// TestCheckDirectoryPermissionsReturnsNonNil pins that the "nothing missing"
// answer is an empty slice and not nil, matching Python's `[]`: the value is
// joined into a message and may be serialised.
func TestCheckDirectoryPermissionsReturnsNonNil(t *testing.T) {
	base := permsTree(t)

	missing, err := CheckDirectoryPermissions(filepath.Join(base, "d_755"), "rwx")
	if err != nil {
		t.Fatalf("CheckDirectoryPermissions: %v", err)
	}
	if missing == nil {
		t.Error("missing is nil, want an empty slice")
	}
}
