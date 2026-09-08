package labfile

import (
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// The vector corpus is the proof of the parsers' behaviour and the oracle
// fixture is the proof of the primitives underneath them. What is left for a
// hand-written test is the package's Go SURFACE — the shapes a vector cannot
// see through its JSON serialisation, and the two entry points the corpus does
// not drive at all.

// writeLab materialises a scenario directory and returns its path.
func writeLab(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

// TestCheckExt covers the deferred lab.ext feature, which has no vector: the
// corpus records 3.8.3's behaviour and 3.8.3 parses the file (PORT_SPEC §0.3).
func TestCheckExt(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		if err := CheckExt(writeLab(t, map[string]string{"lab.conf": "pc1[0]=A\n"})); err != nil {
			t.Fatalf("CheckExt on a scenario without lab.ext = %v, want nil", err)
		}
	})

	t.Run("present", func(t *testing.T) {
		err := CheckExt(writeLab(t, map[string]string{"lab.ext": "A enp9s0\n"}))
		if err == nil {
			t.Fatal("CheckExt on a scenario with lab.ext = nil, want FeatureNotAvailable")
		}

		const want = "lab.ext external links are not supported in this release. Use Kathará 3.8.x."
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
		if got := kerrors.Code(err); got != kerrors.CodeFeatureNotAvailable {
			t.Errorf("code = %q, want %q", got, kerrors.CodeFeatureNotAvailable)
		}

		var feature *kerrors.FeatureNotAvailableError
		if !errors.As(err, &feature) || feature.Feature != kerrors.FeatureLabExt {
			t.Errorf("errors.As gave %+v, want the %q feature", feature, kerrors.FeatureLabExt)
		}
	})

	t.Run("empty file still counts", func(t *testing.T) {
		// `os.path.exists` is presence, not content: an empty lab.ext is still
		// a declaration of external links.
		if err := CheckExt(writeLab(t, map[string]string{"lab.ext": ""})); err == nil {
			t.Fatal("CheckExt on an empty lab.ext = nil, want FeatureNotAvailable")
		}
	})
}

// TestParseErrorShape pins the struct ERROR_CODES.md §0.3 freezes. The vectors
// compare the rendered message; the fields are what the JSON envelope reads.
func TestParseErrorShape(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string
		conf    string
		file    string
		line    int
		code    string
		sentine error
	}{
		{
			name:    "unparseable line",
			files:   map[string]string{"lab.conf": "pc1[0]=A\nbroken line\n"},
			conf:    DefaultConfName,
			file:    "lab.conf",
			line:    2,
			code:    kerrors.CodeSyntax,
			sentine: kerrors.ErrSyntax,
		},
		{
			name:    "reserved device name is a ValueError",
			files:   map[string]string{"lab.conf": "shared[0]=A\n"},
			conf:    DefaultConfName,
			file:    "lab.conf",
			line:    1,
			code:    kerrors.CodeValue,
			sentine: kerrors.ErrValue,
		},
		{
			name:    "the conf name travels into the error",
			files:   map[string]string{"custom.conf": "broken\n"},
			conf:    "custom.conf",
			file:    "custom.conf",
			line:    1,
			code:    kerrors.CodeSyntax,
			sentine: kerrors.ErrSyntax,
		},
		{
			name:    "lab.dep",
			files:   map[string]string{"lab.dep": "# fine\npc1 : pc2\n"},
			file:    DepName,
			line:    2,
			code:    kerrors.CodeSyntax,
			sentine: kerrors.ErrSyntax,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := writeLab(t, test.files)

			var err error
			if test.conf == "" {
				_, err = ParseDep(dir)
			} else {
				_, err = ParseLab(dir, test.conf, model.DefaultDefaults())
			}

			var parse *ParseError
			if !errors.As(err, &parse) {
				t.Fatalf("error = %v, want a *ParseError", err)
			}
			if parse.File != test.file || parse.Line != test.line || parse.Code != test.code {
				t.Errorf("ParseError = {File:%q Line:%d Code:%q}, want {File:%q Line:%d Code:%q}",
					parse.File, parse.Line, parse.Code, test.file, test.line, test.code)
			}
			if kerrors.Code(err) != test.code {
				t.Errorf("kerrors.Code = %q, want %q", kerrors.Code(err), test.code)
			}
			if !errors.Is(err, test.sentine) {
				t.Errorf("errors.Is(err, %v) = false", test.sentine)
			}
		})
	}
}

// TestParseLabFileLevelErrors covers the three OS failures and their wrapping.
// Only the first two have vectors for the alternate conf name.
func TestParseLabFileLevelErrors(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		_, err := ParseLab(t.TempDir(), DefaultConfName, model.DefaultDefaults())
		if err == nil || err.Error() != "No lab.conf in given directory." {
			t.Fatalf("error = %v, want the missing-file message", err)
		}
		if !errors.Is(err, kerrors.ErrOS) {
			t.Error("the missing-file error is not an OSError")
		}
	})

	t.Run("unopenable keeps the cause reachable", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// Opening a directory as a file errors differently on Windows
			// (Python raises PermissionError there, not IsADirectoryError);
			// the expectation is the Linux-oracle shape. Windows runtime is
			// out of 1.0 scope.
			t.Skip("directory-open error shape is the Linux oracle's")
		}
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, DefaultConfName), 0o755); err != nil {
			t.Fatalf("creating the directory: %v", err)
		}

		_, err := ParseLab(dir, DefaultConfName, model.DefaultDefaults())
		if err == nil || err.Error() != "Cannot open lab.conf file." {
			t.Fatalf("error = %v, want the unopenable message", err)
		}
		// Python swallows the original exception; the port keeps it reachable
		// without letting it into the message (kerrors.NewOSCannotOpenConf).
		var pathErr *os.PathError
		if !errors.As(err, &pathErr) {
			t.Error("the underlying filesystem error is not reachable through the chain")
		}
	})

	t.Run("an absolute conf name replaces the directory", func(t *testing.T) {
		// `os.path.join(path, name)` returns name when it is absolute, which
		// filepath.Join does not do. Reachable through `lstart --config`.
		elsewhere := writeLab(t, map[string]string{"other.conf": "pc1[0]=A\n"})
		lab, err := ParseLab(t.TempDir(), filepath.Join(elsewhere, "other.conf"),
			model.DefaultDefaults())
		if err != nil {
			t.Fatalf("ParseLab with an absolute conf name: %v", err)
		}
		if names := lab.MachineNames(); !slices.Equal(names, []string{"pc1"}) {
			t.Errorf("machines = %v, want [pc1]", names)
		}
	})
}

// TestParseLabMetadataEmptyName pins the one LAB_ tri-state the port keeps.
// `LAB_NAME=` names the scenario the empty string, which is NOT the same as an
// unnamed scenario: the hash is computed from "" rather than from the path.
func TestParseLabMetadataEmptyName(t *testing.T) {
	dir := writeLab(t, map[string]string{"lab.conf": "LAB_NAME=\npc1[0]=A\n"})

	lab, err := ParseLab(dir, DefaultConfName, model.DefaultDefaults())
	if err != nil {
		t.Fatalf("ParseLab: %v", err)
	}
	if !lab.HasName() || lab.Name() != "" {
		t.Errorf("HasName/Name = %v/%q, want true/\"\"", lab.HasName(), lab.Name())
	}
	if want := util.GenerateURLSafeHash(""); lab.Hash != want {
		t.Errorf("hash = %q, want the hash of the empty string %q", lab.Hash, want)
	}
}

// TestParseDepEmptyVariants pins the nil-versus-empty distinction of RULINGS.md,
// which the Python signature can only express as None-versus-[].
func TestParseDepEmptyVariants(t *testing.T) {
	tests := []struct {
		name     string
		files    map[string]string
		wantNil  bool
		warnings []string
	}{
		{name: "missing", files: map[string]string{}, wantNil: true},
		{
			name:     "zero-byte",
			files:    map[string]string{"lab.dep": ""},
			wantNil:  true,
			warnings: []string{"lab.dep file is empty. Ignoring..."},
		},
		{name: "comments only", files: map[string]string{"lab.dep": "# nothing\n"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			warnings := installWarningCollector(t)

			deps, err := ParseDep(writeLab(t, test.files))
			if err != nil {
				t.Fatalf("ParseDep: %v", err)
			}
			if (deps == nil) != test.wantNil {
				t.Errorf("deps = %#v, wantNil = %v", deps, test.wantNil)
			}
			if len(deps) != 0 {
				t.Errorf("deps = %v, want no entries", deps)
			}

			got := warnings.take()
			if len(got) != len(test.warnings) {
				t.Fatalf("warnings = %v, want %v", got, test.warnings)
			}
			for i, want := range test.warnings {
				if got[i] != want {
					t.Errorf("warning %d = %v, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestParseOptionsOrder pins what the vector JSON cannot see: the result is
// ordered, a repeated key keeps its first position, and the last value wins.
func TestParseOptionsOrder(t *testing.T) {
	options, err := ParseOptions([]string{"mem=64m", "image=kathara/base", "mem=128m"})
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}

	if keys := options.Keys(); !slices.Equal(keys, []string{"mem", "image"}) {
		t.Errorf("keys = %v, want [mem image]", keys)
	}
	if value, _ := options.Get("mem"); value != "128m" {
		t.Errorf("mem = %q, want 128m", value)
	}

	t.Run("nil and empty agree", func(t *testing.T) {
		for _, input := range [][]string{nil, {}} {
			parsed, err := ParseOptions(input)
			if err != nil {
				t.Fatalf("ParseOptions(%v): %v", input, err)
			}
			if parsed == nil || parsed.Len() != 0 {
				t.Errorf("ParseOptions(%v) = %v, want an empty map", input, parsed)
			}
		}
	})

	t.Run("a failure yields nothing", func(t *testing.T) {
		parsed, err := ParseOptions([]string{"mem=64m", "broken"})
		if err == nil {
			t.Fatal("ParseOptions on a malformed value = nil error")
		}
		if parsed != nil {
			t.Errorf("ParseOptions returned %v alongside its error, want nil", parsed)
		}
		if !errors.Is(err, kerrors.ErrValue) {
			t.Error("the option failure is not a ValueError")
		}
	})
}

// TestInterfaceNumberDispatch pins RULINGS.md OQ-14a at the function that
// implements it: only a failed integer parse routes a line to the meta path.
func TestInterfaceNumberDispatch(t *testing.T) {
	tests := []struct {
		arg    string
		number int
		isMeta bool
	}{
		{arg: "0", number: 0},
		{arg: "00", number: 0},
		{arg: "01", number: 1},
		{arg: "0_1", number: 1},  // PEP 515
		{arg: "1_0", number: 10}, // PEP 515
		{arg: "٣", number: 3},    // Arabic-Indic three
		{arg: "_0", isMeta: true},
		{arg: "0_", isMeta: true},
		{arg: "1__0", isMeta: true},
		{arg: "²", isMeta: true}, // superscript two: isdigit, not int()
		{arg: "image", isMeta: true},
		{arg: "0x1", isMeta: true},
		// Beyond a Go int. Still a number, so it stays on the interface path
		// and saturates (DIVERGENCES.md 45).
		{arg: "99999999999999999999", number: math.MaxInt},
	}

	for _, test := range tests {
		number, err := interfaceNumber(test.arg)
		if (err != nil) != test.isMeta {
			t.Errorf("interfaceNumber(%q) err = %v, isMeta = %v", test.arg, err, test.isMeta)
			continue
		}
		if !test.isMeta && number != test.number {
			t.Errorf("interfaceNumber(%q) = %d, want %d", test.arg, number, test.number)
		}
	}

	t.Run("an out-of-range number still fails the sequence check", func(t *testing.T) {
		dir := writeLab(t, map[string]string{"lab.conf": "pc1[99999999999999999999]=A\n"})
		_, err := ParseLab(dir, DefaultConfName, model.DefaultDefaults())
		const want = "Interface `0` missing on device `pc1`."
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})
}

// TestInterfaceNumberSaturationIsObservable pins the residue of DIVERGENCES.md
// 45. A single out-of-range number lands on the same message as Python's, which
// is what the test above asserts; two of them on one device do not, because
// saturation makes distinct numbers equal and makes an exact one unprintable.
// Both cases are oracle-verified against 3.8.3 and both are recorded, so a
// change here is a change to the divergence, not a regression.
func TestInterfaceNumberSaturationIsObservable(t *testing.T) {
	tests := []struct {
		name   string
		conf   string
		want   string
		code   string
		python string
	}{
		{
			name: "two distinct out-of-range numbers collide on one slot",
			conf: "pc1[99999999999999999999]=A\npc1[88888888888888888888]=B\n",
			want: "Interface 9223372036854775807 already set on device `pc1`.",
			code: kerrors.CodeMachineCollisionDomain,
			// Python keeps the two numbers apart, so the parse reaches
			// check_integrity: `NonSequentialMachineInterfaceError`, ``Interface
			// `0` missing on device `pc1`.``
			python: "NonSequentialMachineInterface",
		},
		{
			name: "a repeated out-of-range number reports the saturated value",
			conf: "pc1[99999999999999999999]=A\npc1[99999999999999999999]=B\n",
			want: "Interface 9223372036854775807 already set on device `pc1`.",
			code: kerrors.CodeMachineCollisionDomain,
			// Python agrees on the class and the code and prints all twenty
			// digits: `Interface 99999999999999999999 already set on device
			// `pc1`.`
			python: "MachineCollisionDomain",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := writeLab(t, map[string]string{"lab.conf": test.conf})

			_, err := ParseLab(dir, DefaultConfName, model.DefaultDefaults())
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q (Python: %s)", err, test.want, test.python)
			}
			if got := kerrors.Code(err); got != test.code {
				t.Errorf("code = %q, want %q", got, test.code)
			}
		})
	}
}

// TestFlattenCycleGuard pins the one place the port refuses to follow Python:
// a cyclic graph makes `depgen.flatten` recurse until RecursionError, which in
// Go would be a fatal stack exhaustion (DIVERGENCES.md 46). ParseDep never gets
// here — it calls HasLoop first — but Flatten is exported.
func TestFlattenCycleGuard(t *testing.T) {
	graph := NewDepGraph()
	graph.Set("a", []string{"b"})
	graph.Set("b", []string{"a"})

	if !HasLoop(graph) {
		t.Fatal("HasLoop on a two-node cycle = false")
	}

	// The contract is only that it terminates and names every device once.
	flattened := Flatten(graph)
	sorted := slices.Clone(flattened)
	slices.Sort(sorted)
	if !slices.Equal(sorted, []string{"a", "b"}) {
		t.Errorf("Flatten on a cycle = %v, want a permutation of [a b]", flattened)
	}

	t.Run("empty graph flattens to an empty, non-nil slice", func(t *testing.T) {
		flattened := Flatten(NewDepGraph())
		if flattened == nil || len(flattened) != 0 {
			t.Errorf("Flatten of an empty graph = %#v, want []", flattened)
		}
	})
}

// TestDuplicateMetaWarning pins the warning text and the fact that `exec`
// never produces one, because it appends rather than overwrites.
func TestDuplicateMetaWarning(t *testing.T) {
	warnings := installWarningCollector(t)

	dir := writeLab(t, map[string]string{"lab.conf": strings.Join([]string{
		"pc1[image]=kathara/base",
		"pc1[image]=kathara/frr",
		"pc1[exec]=echo one",
		"pc1[exec]=echo two",
		"",
	}, "\n")})

	if _, err := ParseLab(dir, DefaultConfName, model.DefaultDefaults()); err != nil {
		t.Fatalf("ParseLab: %v", err)
	}

	got := warnings.take()
	want := "In lab.conf - Line 2: Device `pc1` already has a value assigned to meta " +
		"`image`. Previous value has been overwritten with `kathara/frr`."
	if len(got) != 1 || got[0] != want {
		t.Errorf("warnings = %v, want exactly [%q]", got, want)
	}
}

// TestFolderParserSortsNames pins the OQ-15a ruling: Python's glob order is the
// filesystem's, and the port replaces it with a sort. The vector for it is
// marked order-insensitive on purpose, so this is where the ruling is asserted.
func TestFolderParserSortsNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"m_delta", "m_alpha", "m_charlie", "m_bravo", "shared", ".git"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
	}

	lab, err := ParseFolder(dir, model.DefaultDefaults())
	if err != nil {
		t.Fatalf("ParseFolder: %v", err)
	}

	want := []string{"m_alpha", "m_bravo", "m_charlie", "m_delta"}
	if got := lab.MachineNames(); !slices.Equal(got, want) {
		t.Errorf("machines = %v, want %v", got, want)
	}
}

// TestFolderParserFollowsSymlinks pins what `glob("*/")` calls a directory.
// Oracle-verified: a symlink to a directory yields a device under BOTH names,
// while a symlink to a file and a broken symlink yield nothing.
func TestFolderParserFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "target"), 0o755); err != nil {
		t.Fatalf("creating target: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatalf("creating plain: %v", err)
	}
	links := map[string]string{
		"link":   filepath.Join(dir, "target"),
		"flink":  filepath.Join(dir, "plain"),
		"broken": filepath.Join(dir, "nonexistent"),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatalf("linking %s: %v", name, err)
		}
	}

	lab, err := ParseFolder(dir, model.DefaultDefaults())
	if err != nil {
		t.Fatalf("ParseFolder: %v", err)
	}

	want := []string{"link", "target"}
	if got := lab.MachineNames(); !slices.Equal(got, want) {
		t.Errorf("machines = %v, want %v", got, want)
	}
}

// TestFolderParserGlobsTheScenarioPath pins the consequence of building the
// pattern by concatenation: the scenario path is part of it, so a `[`, `]`, `*`
// or `?` in the path is MATCHED rather than looked up.
//
// Oracle-verified against 3.8.3: with `lab1/devb` next to `lab[1]/deva`,
// `FolderParser.parse(".../lab[1]")` returns the single device `devb` — the
// character class matched the sibling directory and the bracketed one was never
// listed — while the scenario's filesystem stays rooted at the literal path.
func TestFolderParserGlobsTheScenarioPath(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{"lab[1]/deva", "lab1/devb", "lab1/shared"} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	bracketed := filepath.Join(base, "lab[1]")
	lab, err := ParseFolder(bracketed, model.DefaultDefaults())
	if err != nil {
		t.Fatalf("ParseFolder: %v", err)
	}
	if got := lab.MachineNames(); !slices.Equal(got, []string{"devb"}) {
		t.Errorf("machines = %v, want [devb] — the sibling `lab1`, not `lab[1]`", got)
	}
	if path, ok := lab.FSPath(); !ok || path != bracketed {
		t.Errorf("scenario filesystem = %q, want the literal %q", path, bracketed)
	}

	t.Run("a path with no metacharacter is still looked up literally", func(t *testing.T) {
		lab, err := ParseFolder(filepath.Join(base, "lab1"), model.DefaultDefaults())
		if err != nil {
			t.Fatalf("ParseFolder: %v", err)
		}
		if got := lab.MachineNames(); !slices.Equal(got, []string{"devb"}) {
			t.Errorf("machines = %v, want [devb]", got)
		}
	})

	t.Run("a star harvests every sibling", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("NTFS cannot hold a directory literally named `lab*`")
		}
		// The scenario directory has to exist under its literal name or the
		// `Lab` constructor fails first — `open_fs("osfs://…")` does no globbing
		// and reports ``root path '…' does not exist``, which is what both
		// implementations answer for a path that is only a pattern
		// (oracle-verified).
		for _, dir := range []string{"lab*/deva", "labx/devc"} {
			if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
				t.Fatalf("creating %s: %v", dir, err)
			}
		}

		lab, err := ParseFolder(filepath.Join(base, "lab*"), model.DefaultDefaults())
		if err != nil {
			t.Fatalf("ParseFolder: %v", err)
		}
		// `lab*/deva`, `lab1/devb` and `labx/devc`, with `lab[1]/deva` folded
		// onto the first `deva` and `lab1/shared` skipped as reserved.
		if got := lab.MachineNames(); !slices.Equal(got, []string{"deva", "devb", "devc"}) {
			t.Errorf("machines = %v, want [deva devb devc]", got)
		}
	})
}

// TestMachineFoldersExpandsEveryComponent pins the shape of the expansion: it
// is `glob`'s, so EVERY component of the path is a pattern, a literal component
// in between two of them is looked up rather than listed, and a relative path
// resolves against the working directory.
//
// It drives `machineFolders` rather than [ParseFolder] because a path that is
// only a pattern has no directory for the `Lab` constructor to open. All three
// expectations are `glob`'s, measured on 3.8.3.
func TestMachineFoldersExpandsEveryComponent(t *testing.T) {
	base := t.TempDir()
	for _, dir := range []string{
		"p1/lab1/devb", "p1/lab1/.hidden", "p2/lab1/devd", "p2/other/devc",
	} {
		if err := os.MkdirAll(filepath.Join(base, dir), 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}

	want := []string{"devb", "devd"}
	if got := machineFolders(filepath.Join(base, "p*", "lab1")); !slices.Equal(got, want) {
		t.Errorf("machineFolders(.../p*/lab1) = %v, want %v", got, want)
	}

	t.Run("a relative path resolves against the working directory", func(t *testing.T) {
		t.Chdir(base)
		for _, pattern := range []string{"p*/lab1", "p?/lab1", "p[12]/lab1"} {
			if got := machineFolders(pattern); !slices.Equal(got, want) {
				t.Errorf("machineFolders(%q) = %v, want %v", pattern, got, want)
			}
		}
	})
}

// TestGlobPatternMatching pins the `fnmatch` translation the path expansion
// rests on, at the places CPython and RE2 disagree about a class. Every
// expectation is `fnmatch.filter`'s, measured on the corpus interpreter.
func TestGlobPatternMatching(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The expectations replay CPython's POSIX fnmatch; on Windows both
		// CPython and this port normcase through ntpath (lowercase,
		// separator flip), so the POSIX-shaped table cannot apply.
		// TODO(windows-runtime): record a Windows oracle table post-1.0.
		t.Skip("expectations are the POSIX fnmatch oracle's")
	}
	names := []string{"a", "b", "z", "ab", "]", "[", "-", "^", "!", `\`, "a-b", "lab1", "lab[1]"}

	tests := []struct {
		pattern string
		want    []string
	}{
		{pattern: "*", want: names},
		{pattern: "?", want: []string{"a", "b", "z", "]", "[", "-", "^", "!", `\`}},
		{pattern: "[a]", want: []string{"a"}},
		// `!` negates and `^` is a member — the opposite of RE2 on both counts.
		{pattern: "[!a]", want: []string{"b", "z", "]", "[", "-", "^", "!", `\`}},
		{pattern: "[^a]", want: []string{"a", "^"}},
		// A `]` right after the bracket is a member, not the end of the class.
		{pattern: "[]]", want: []string{"]"}},
		{pattern: "[!]]", want: []string{"a", "b", "z", "[", "-", "^", "!", `\`}},
		// An inverted range matches nothing rather than being an error.
		{pattern: "[z-a]", want: nil},
		{pattern: "[a-]", want: []string{"a", "-"}},
		{pattern: "[--/]", want: []string{"-"}},
		{pattern: "[[]", want: []string{"["}},
		{pattern: `[\\]`, want: []string{`\`}},
		// An unclosed class is a literal `[`.
		{pattern: "[ab", want: nil},
		// RE2 would read `[:alpha:]` as a POSIX class; CPython does not, so the
		// pattern is a one-character class followed by a literal `]`.
		{pattern: "[[:alpha:]]", want: nil},
		{pattern: "lab[1]", want: []string{"lab1"}},
		{pattern: "lab*", want: []string{"lab1", "lab[1]"}},
		{pattern: "l?b1", want: []string{"lab1"}},
		{pattern: "a*b", want: []string{"ab", "a-b"}},
	}

	for _, test := range tests {
		match := newGlobMatcher(test.pattern)
		got := []string{}
		for _, name := range names {
			if match(name) {
				got = append(got, name)
			}
		}
		if !slices.Equal(got, test.want) && !(len(got) == 0 && len(test.want) == 0) {
			t.Errorf("filter(%q) = %v, want %v", test.pattern, got, test.want)
		}
	}

	t.Run("the leading-dot rule is glob's and keys on the pattern", func(t *testing.T) {
		entries := []string{".git", "pc1"}
		if got := globMatchNames(entries, "*"); !slices.Equal(got, []string{"pc1"}) {
			t.Errorf("`*` matched %v, want [pc1]", got)
		}
		if got := globMatchNames(entries, ".*"); !slices.Equal(got, []string{".git"}) {
			t.Errorf("`.*` matched %v, want [.git]", got)
		}
	})
}

// TestLoggingGoesThroughTheDefaultLogger guards the plumbing every warning
// assertion above depends on: the parsers must not hold their own logger.
func TestLoggingGoesThroughTheDefaultLogger(t *testing.T) {
	warnings := installWarningCollector(t)
	slog.Warn("probe")

	if got := warnings.take(); len(got) != 1 || got[0] != "probe" {
		t.Fatalf("the collector recorded %v, want [probe]", got)
	}
}
