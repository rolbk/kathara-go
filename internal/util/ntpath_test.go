package util

import "testing"

// TestNTPathPrimitives checks the ntpath port against vectors recorded from
// the oracle's own `ntpath`. The module is pure Python and imports on any
// platform, so the Windows build's path rules are covered by a run on Linux —
// which is the only place this suite ever runs.
func TestNTPathPrimitives(t *testing.T) {
	fixture := loadFixture(t)

	if len(fixture.NTPath.SplitRoot) == 0 || len(fixture.NTPath.Join) == 0 {
		t.Fatal("the fixture carries no ntpath vectors")
	}

	t.Run("splitroot", func(t *testing.T) {
		for _, tc := range fixture.NTPath.SplitRoot {
			if len(tc.Output) != 3 {
				t.Fatalf("splitroot(%q): fixture records %d fields, want 3", tc.Input, len(tc.Output))
			}
			drive, root, tail := ntSplitRoot(tc.Input)
			if drive != tc.Output[0] || root != tc.Output[1] || tail != tc.Output[2] {
				t.Errorf("ntSplitRoot(%q) = (%q, %q, %q), oracle says (%q, %q, %q)",
					tc.Input, drive, root, tail, tc.Output[0], tc.Output[1], tc.Output[2])
			}
		}
	})

	t.Run("split", func(t *testing.T) {
		for _, tc := range fixture.NTPath.Split {
			if len(tc.Output) != 2 {
				t.Fatalf("split(%q): fixture records %d fields, want 2", tc.Input, len(tc.Output))
			}
			head, tail := ntSplit(tc.Input)
			if head != tc.Output[0] || tail != tc.Output[1] {
				t.Errorf("ntSplit(%q) = (%q, %q), oracle says (%q, %q)",
					tc.Input, head, tail, tc.Output[0], tc.Output[1])
			}
		}
	})

	t.Run("join", func(t *testing.T) {
		for _, tc := range fixture.NTPath.Join {
			if got := ntJoin(tc.A, tc.B); got != tc.Output {
				t.Errorf("ntJoin(%q, %q) = %q, oracle says %q", tc.A, tc.B, got, tc.Output)
			}
		}
	})

	t.Run("normcase", func(t *testing.T) {
		for _, tc := range fixture.NTPath.NormCase {
			if got := ntNormCase(tc.Input); got != tc.Output {
				t.Errorf("ntNormCase(%q) = %q, oracle says %q", tc.Input, got, tc.Output)
			}
		}
	})
}

// TestNTJoinDoesNotClean pins the property [GetCurrentUserHome] depends on and
// that path/filepath cannot provide: ntpath.join never normalises, so an empty
// `%HOMEPATH%` leaves the bare drive rather than becoming "C:.".
func TestNTJoinDoesNotClean(t *testing.T) {
	for _, tc := range []struct{ a, b, want string }{
		{"C:", "", "C:"},
		{"C:", "Users\\me\\", "C:Users\\me\\"},
		{"C:", "a\\..\\b", "C:a\\..\\b"},
		{"C:\\", "a\\..\\b", "C:\\a\\..\\b"},
	} {
		if got := ntJoin(tc.a, tc.b); got != tc.want {
			t.Errorf("ntJoin(%q, %q) = %q, want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestPyLowerIsTheFullMapping pins the two mappings that separate Python's
// `str.lower()` from [strings.ToLower] and that reach frozen message text
// through [StrToBool] and [architectureOf].
func TestPyLowerIsTheFullMapping(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\u0130", "i\u0307"},  // İ expands to two code points
		{"\u0391\u03a3", "ας"}, // a final sigma is ς
		{"\u03a3\u0391", "σα"}, // a non-final one is σ
		{"TRUE", "true"},
		{"", ""},
	} {
		if got := pyLower(tc.in); got != tc.want {
			t.Errorf("pyLower(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// The error message quotes the folded value, so the expansion is visible.
	if _, err := StrToBool("\u0130"); err == nil || err.Error() != "Invalid truth value `i\u0307`." {
		t.Errorf("StrToBool(\"İ\") error = %v", err)
	}
}

// TestPyUpperIsTheFullMapping is the mirror of the above for `str.upper()`,
// which `shutil.which` runs the command and every PATHEXT entry through on
// Windows. The first three expand rather than map one code point to one, which
// is exactly what [strings.ToUpper] cannot do.
func TestPyUpperIsTheFullMapping(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ß", "SS"}, // the sharp s
		{"ﬁ", "FI"}, // the fi ligature
		{"ŉ", "ʼN"}, // the deprecated n preceded by apostrophe
		{".exe", ".EXE"},
		{"", ""},
	} {
		if got := pyUpper(tc.in); got != tc.want {
			t.Errorf("pyUpper(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
