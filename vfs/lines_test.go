package vfs

import (
	"errors"
	"io/fs"
	"regexp"
	"testing"
)

// Every "verified:" note below records the output of running the real
// FilesystemMixin from Kathara 3.8.3 on pyfilesystem2 through
// /root/kathara/pyvenv; the probe scripts and their transcripts are summarised
// in docs/port/SPIKES/vfs.md.

func TestSplitLines(t *testing.T) {
	// pyfilesystem opens text files with newline="": universal-newline
	// SPLITTING, no translation. Verified by reading each byte string back
	// through fs.open(...,"r").readlines() on a mem:// filesystem.
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty file yields no lines", "", nil},
		// verified: readlines(b"\n") == ['\n']
		{"lone newline", "\n", []string{"\n"}},
		// verified: readlines(b"a\n\n") == ['a\n', '\n']
		{"blank line kept", "a\n\n", []string{"a\n", "\n"}},
		{"no trailing terminator", "a\nb", []string{"a\n", "b"}},
		// verified: readlines(b"a\r\rb") == ['a\r', '\r', 'b']
		{"bare CR terminates a line", "a\r\rb", []string{"a\r", "\r", "b"}},
		// verified: readlines(b"a\r\r\nb") == ['a\r', '\r\n', 'b']
		{"CRLF is one terminator", "a\r\r\nb", []string{"a\r", "\r\n", "b"}},
		// verified: readlines(b"a\n\rb") == ['a\n', '\r', 'b']
		{"LF then CR are two terminators", "a\n\rb", []string{"a\n", "\r", "b"}},
		// verified: readlines(b"a\r\n\r\nb") == ['a\r\n', '\r\n', 'b']
		{"consecutive CRLF", "a\r\n\r\nb", []string{"a\r\n", "\r\n", "b"}},
		{"CRLF throughout", "a\r\nb\r\nd\r\n", []string{"a\r\n", "b\r\n", "d\r\n"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitLines([]byte(tt.in))
			if len(got) != len(tt.want) {
				t.Fatalf("SplitLines(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("SplitLines(%q) = %q, want %q", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestPySearch(t *testing.T) {
	// Python's `$` matches at end-of-string OR immediately before a trailing
	// newline; Go's `$` is \z. Each row was run through write_line_before on
	// the corresponding file to observe whether the line matched.
	tests := []struct {
		name    string
		pattern string
		line    string
		want    bool
	}{
		{"plain substring", "b", "b\n", true},
		{"no match", "z", "b\n", false},
		// verified D3: write_line_before(b"a\nb\nd\n", "X", "b$") added 1 line
		{"dollar matches before a trailing LF", "b$", "b\n", true},
		// verified D1: the same call on b"a\r\nb\r\nd\r\n" added 0
		{"dollar does not match before CRLF", "b$", "b\r\n", false},
		// verified D2: the same call on b"a\rb\rd\r" added 0
		{"dollar does not match before a bare CR", "b$", "b\r", false},
		// verified D4: on b"a\nb" (no terminator) it added 1
		{"dollar matches at true end of string", "b$", "b", true},
		// verified D5: "^.$" on b"a\nb\n" added 2
		{"anchored single char", "^.$", "a\n", true},
		// verified D6: "b\\n$" on b"a\nb\n" added 1
		{"explicit newline before dollar", `b\n$`, "b\n", true},
		// verified N7: "^$" on a file containing just b"\n" added 1
		{"empty-line anchor", "^$", "\n", true},
		// verified P9: delete_line(b"a\nb\nd", "b\n") deleted 1
		{"pattern containing the terminator", `b\n`, "b\n", true},
		// verified P3b: empty pattern matched all 3 lines
		{"empty pattern matches everything", "", "a\n", true},
		{"caret is not multiline", "^b", "a\nb", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re := regexp.MustCompile(tt.pattern)
			if got := pySearch(re, tt.line); got != tt.want {
				t.Errorf("pySearch(%q, %q) = %v, want %v", tt.pattern, tt.line, got, tt.want)
			}
		})
	}
}

type lineCase struct {
	name    string
	content string
	add     string // unused by DeleteLine
	pattern string
	first   bool
	wantN   int
	want    string
}

var writeLineBeforeCases = []lineCase{
	// The six vectors from tests/model/filesystem_mixin_test.py.
	{"insert before the only match", "a\nb\nd", "c", "d", false, 1, "a\nb\nc\nd"},
	{"regex hits two lines", "a\nb1\nd\nb2", "c", "b[1-2]", false, 2, "a\nc\nb1\nd\nc\nb2"},
	{"two identical matches", "a\nb\nd\nd", "c", "d", false, 2, "a\nb\nc\nd\nc\nd"},
	{"inserted line equals the pattern, no loop", "a\nb\nd\nd", "d", "d", false, 2, "a\nb\nd\nd\nd\nd"},
	{"first occurrence only", "a\nb\nd\nd", "z", "d", true, 1, "a\nb\nz\nd\nd"},
	{"regex, first occurrence only", "a\nb1\nd\nb2", "c", "b[1-2]", true, 1, "a\nc\nb1\nd\nb2"},
	// test_write_line_before_with_indentation: the match is found anywhere
	// in the raw line (re.search, not a stripped re.match) and the inserted
	// line carries no indentation.
	{"indentation is not copied", "\ta\n\t\tb\n\t\t\td", "c", "d", false, 1, "\ta\n\t\tb\nc\n\t\t\td"},
	// verified P1: returns 0 and leaves an all-LF file byte-identical.
	{"no match leaves an LF file alone", "a\nb\nd", "c", "z", false, 0, "a\nb\nd"},
	// verified N1: a CRLF file is still rewritten, normalised, on 0 hits.
	{"no match still normalises CRLF", "a\r\nb\r\n", "X", "zzz", false, 0, "a\nb\n"},
	// verified P2: matching line keeps its position, all lines lose the CR.
	{"CRLF file collapses to LF", "a\r\nb\r\nd\r\n", "c", "b", false, 1, "a\nc\nb\nd\n"},
	// verified P2d: a bare CR is NOT normalised, only CRLF is.
	{"bare CR survives normalisation", "a\rb\rd\r", "c", "b", false, 1, "a\rc\nb\rd\r"},
	// verified N4: "\n\r" spans a line boundary, so the first replace in
	// the Python source can never fire here.
	{"LF followed by CR is untouched", "a\n\rb\n", "X", "zzz", false, 0, "a\n\rb\n"},
	// verified P4: inserting before a terminator-less last line leaves it
	// terminator-less.
	{"match on a last line with no newline", "a\nb\nd", "c", "d", false, 1, "a\nb\nc\nd"},
	// verified P3b
	{"empty pattern matches every line", "a\nb\nd", "c", "", false, 3, "c\na\nc\nb\nc\nd"},
	// verified P3
	{"empty file", "", "c", "", false, 0, ""},
	// verified N7
	{"file that is only a newline", "\n", "X", "^$", false, 1, "X\n\n"},
	// verified N5: line_to_add is written verbatim plus one "\n"; embedded
	// newlines are not escaped or split.
	{"added line containing a newline", "a\nb\n", "X\nY", "b", false, 1, "a\nX\nY\nb\n"},
	// verified D3 / D1: the $ anchor, through the public function.
	{"dollar anchor on LF lines", "a\nb\nd\n", "X", "b$", false, 1, "a\nX\nb\nd\n"},
	{"dollar anchor on CRLF lines", "a\r\nb\r\nd\r\n", "X", "b$", false, 0, "a\nb\nd\n"},
}

func TestWriteLineBefore(t *testing.T) {
	runLineCases(t, writeLineBeforeCases, func(fsys FS, c lineCase) (int, error) {
		return WriteLineBefore(fsys, "test.txt", c.add, c.pattern, c.first)
	})
}

var writeLineAfterCases = []lineCase{
	{"insert after the only match", "a\nb\nd", "c", "b", false, 1, "a\nb\nc\nd"},
	{"regex hits two lines", "a\nb1\nd\nb2", "c", "b[1-2]", false, 2, "a\nb1\nc\nd\nb2\nc\n"},
	{"two identical matches", "a\nb\nd\nd", "c", "d", false, 2, "a\nb\nd\nc\nd\nc\n"},
	// test_write_line_after_possible_loop: adding "b" after "b" adds once
	// per ORIGINAL line, so the file grows by exactly one line.
	{"inserted line equals the pattern, no loop", "a\nb\nd\nd", "b", "b", false, 1, "a\nb\nb\nd\nd"},
	{"first occurrence only", "a\nb\nd\nd", "z", "d", true, 1, "a\nb\nd\nz\nd"},
	{"regex, first occurrence only", "a\nb1\nd\nb2", "c", "b[1-2]", true, 1, "a\nb1\nc\nd\nb2"},
	{"indentation is not copied", "\ta\n\t\tb\n\t\t\td", "c", "b", false, 1, "\ta\n\t\tb\nc\n\t\t\td"},
	{"no match", "a\nb\nd", "c", "z", false, 0, "a\nb\nd"},
	// test_write_line_after_end_line_with_no_return, verified P5: the
	// matched last line gains the "\n" it lacked, then the new line lands.
	{"match on a last line with no newline", "\ta\n\t\tb\n\t\t\td", "c", "d", false, 1, "\ta\n\t\tb\n\t\t\td\nc\n"},
	// verified A2: the endswith("\n") test looks at the RAW line, so a
	// line ending in a bare CR is treated as unterminated.
	{"last line ending in a bare CR", "a\r\nd\r", "X", "d", false, 1, "a\nd\r\nX\n"},
	// verified A1: a CRLF-terminated match needs no extra "\n".
	{"CRLF-terminated match", "a\r\nd\r\n", "X", "d", false, 1, "a\nd\nX\n"},
	// verified P2c
	{"CRLF file collapses to LF", "a\r\nb\r\nd\r\n", "c", "b", false, 1, "a\nb\nc\nd\n"},
	// verified N3
	{"no match on a CR-only file changes nothing", "a\rb\r", "X", "zzz", false, 0, "a\rb\r"},
	{"empty file", "", "X", "a", false, 0, ""},
}

func TestWriteLineAfter(t *testing.T) {
	runLineCases(t, writeLineAfterCases, func(fsys FS, c lineCase) (int, error) {
		return WriteLineAfter(fsys, "test.txt", c.add, c.pattern, c.first)
	})
}

var deleteLineCases = []lineCase{
	{"delete the only match", "a\nb\nd", "", "b", false, 1, "a\nd"},
	{"regex deletes two lines", "a\nb1\nd\nb2", "", "b[1-2]", false, 2, "a\nd\n"},
	{"two identical matches", "a\nb\nb\nd", "", "b", false, 2, "a\nd"},
	{"first occurrence only", "a\nb\nb\nd", "", "b", true, 1, "a\nb\nd"},
	{"regex, first occurrence only", "a\nb1\nd\nb2", "", "b[1-2]", true, 1, "a\nd\nb2"},
	{"indentation does not block the match", "\ta\n\t\tb\n\t\t\td", "", "b", false, 1, "\ta\n\t\t\td"},
	{"no match", "a\nb\nd", "", "z", false, 0, "a\nb\nd"},
	// verified P5b: deleting the terminator-less last line leaves the file
	// ending in the previous line's newline.
	{"delete a last line with no newline", "a\nb\nd", "", "d", false, 1, "a\nb\n"},
	// verified P2b
	{"CRLF file collapses to LF", "a\r\nb\r\nd\r\n", "", "b", false, 1, "a\nd\n"},
	// verified N2
	{"no match still normalises CRLF", "a\r\nb\r\n", "", "zzz", false, 0, "a\nb\n"},
	// verified P8b
	{"caret-dollar anchors", "a\nb\nd", "", "^b$", false, 1, "a\nd"},
	// verified P9
	{"pattern containing the terminator", "a\nb\nd", "", `b\n`, false, 1, "a\nd"},
	{"empty pattern deletes everything", "a\nb\n", "", "", false, 2, ""},
	{"empty file", "", "", "a", false, 0, ""},
}

func TestDeleteLine(t *testing.T) {
	runLineCases(t, deleteLineCases, func(fsys FS, c lineCase) (int, error) {
		return DeleteLine(fsys, "test.txt", c.pattern, c.first)
	})
}

// runLineCases replays each vector on both FS implementations: the same bytes
// must come out of mem:// and osfs://.
func runLineCases(t *testing.T, cases []lineCase, call func(FS, lineCase) (int, error)) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				if err := CreateFileFromString(fsys, c.content, "test.txt"); err != nil {
					t.Fatal(err)
				}
				n, err := call(fsys, c)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if n != c.wantN {
					t.Errorf("count = %d, want %d", n, c.wantN)
				}
				got, err := ReadFile(fsys, "test.txt")
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != c.want {
					t.Errorf("content = %q, want %q", got, c.want)
				}
			})
		})
	}
}

func TestLineFunctionErrors(t *testing.T) {
	// Error order is Python's: the nil-FS guard, then re.compile, then the
	// file access. A bad pattern therefore reports itself even when the file
	// does not exist (verified R1: re.PatternError before any I/O).
	type call struct {
		name string
		fn   func(FS, string, string) (int, error)
	}
	calls := []call{
		{"WriteLineBefore", func(f FS, p, pat string) (int, error) { return WriteLineBefore(f, p, "x", pat, false) }},
		{"WriteLineAfter", func(f FS, p, pat string) (int, error) { return WriteLineAfter(f, p, "x", pat, false) }},
		{"DeleteLine", func(f FS, p, pat string) (int, error) { return DeleteLine(f, p, pat, false) }},
	}
	for _, c := range calls {
		t.Run(c.name+"/nil fs", func(t *testing.T) {
			n, err := c.fn(nil, "test.txt", "b")
			if !errors.Is(err, ErrNoFilesystem) {
				t.Errorf("err = %v, want ErrNoFilesystem", err)
			}
			if err != nil && err.Error() != MsgNoFilesystem {
				t.Errorf("message = %q, want %q", err.Error(), MsgNoFilesystem)
			}
			if n != 0 {
				t.Errorf("count = %d, want 0", n)
			}
		})
		t.Run(c.name+"/missing file", func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				_, err := c.fn(fsys, "test.txt", "b")
				if !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("err = %v, want fs.ErrNotExist", err)
				}
			})
		})
		t.Run(c.name+"/path is a directory", func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				if err := fsys.MkdirAll("test", 0o755); err != nil {
					t.Fatal(err)
				}
				_, err := c.fn(fsys, "test", "b")
				if !errors.Is(err, ErrFileExpected) {
					t.Errorf("err = %v, want ErrFileExpected", err)
				}
			})
		})
		t.Run(c.name+"/invalid regex beats a missing file", func(t *testing.T) {
			forEachFS(t, func(t *testing.T, fsys FS) {
				_, err := c.fn(fsys, "missing.txt", "[")
				if err == nil {
					t.Fatal("expected a regexp compile error")
				}
				if errors.Is(err, fs.ErrNotExist) {
					t.Errorf("err = %v, want the regexp error, not not-exist", err)
				}
			})
		})
	}
}
