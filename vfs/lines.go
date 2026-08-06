package vfs

import (
	"regexp"
	"strings"
)

// This file ports FilesystemMixin.write_line_before / write_line_after /
// delete_line. Every rule below was pinned against Kathara 3.8.3 running on
// the real pyfilesystem2 (probe log in docs/port/SPIKES/vfs.md); the three
// non-obvious ones are:
//
//  1. pyfilesystem opens text files with newline="" — universal-newline
//     SPLITTING with no translation. Lines therefore keep their original
//     terminator ("\r\n", "\r" or "\n") when the pattern is matched against
//     them, and only the copy written back is normalised.
//  2. The normalisation is the literal Python
//     `line.replace("\n\r", "\n").replace("\r\n", "\n")`: CRLF collapses to LF,
//     a lone CR is left alone. It runs on EVERY line, so a CRLF file is
//     rewritten as LF even when nothing matched and the function returns 0.
//  3. Python's `$` matches at end-of-string OR just before a trailing newline;
//     Go's `$` (without the m flag) only matches end-of-text. pySearch below
//     restores the Python meaning.

// SplitLines splits b the way Python's io.TextIOWrapper(newline="") does:
// a line ends after "\n", after "\r\n", or after a "\r" not followed by "\n",
// and the terminator stays attached to the line untranslated.
//
//	"a\r\rb"    -> ["a\r", "\r", "b"]
//	"a\r\r\nb"  -> ["a\r", "\r\n", "b"]
//	"a\n\rb"    -> ["a\n", "\r", "b"]
//	""          -> []
func SplitLines(b []byte) []string {
	s := string(b)
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			out = append(out, s[start:i+1])
			start = i + 1
		case '\r':
			end := i + 1
			if end < len(s) && s[end] == '\n' {
				end++
			}
			out = append(out, s[start:end])
			i = end - 1
			start = end
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// normalizeLine is `line.replace("\n\r", "\n").replace("\r\n", "\n")`.
//
// The first replacement is unreachable for a single line (a "\n" can only be
// the final byte of a line, so it can never be followed by "\r"); it is kept
// so the port reads against the Python line-for-line. It is genuinely live in
// utils.convert_win_2_linux, which runs on whole-file content.
func normalizeLine(line string) string {
	return strings.ReplaceAll(strings.ReplaceAll(line, "\n\r", "\n"), "\r\n", "\n")
}

// pySearch is Python's re.search over a line that still carries its terminator.
//
// Go's `$` is \z; Python's is "end of string, or immediately before a newline
// at the end of the string". Retrying the match against the line with exactly
// one trailing "\n" removed reproduces that, and cannot invent matches for
// patterns that do not use an end anchor: the shorter string is a prefix of
// the longer one, so any unanchored match in it also exists in the original.
//
// Pinned against Python (docs/port/SPIKES/vfs.md, probes D1-D6, N7):
//
//	"b$"  vs "b\n"    -> match      (Go alone: no match)
//	"b$"  vs "b\r\n"  -> no match
//	"b$"  vs "b\r"    -> no match
//	"^$"  vs "\n"     -> match
//
// Residual gap, documented in SPIKES/vfs.md §5 and pinned by
// testdata/pysearch_divergent.json. The retry only covers a `$` that the whole
// match ends at. When a `$` sits MID-pattern and what follows it consumes the
// trailing newline, Python matches and this returns false — `re.search("b$\n",
// "b\n")` is True in Python, while under RE2 `b$\n` is unsatisfiable and the
// stripped line has no "\n" left for the pattern to consume. Python's `$` is
// the lookahead `(?=\n?\z)`, which RE2 has no spelling for, so there is no
// faithful emulation; the failure mode is one-sided (a miss, never an invented
// match), which is why the pinning test asserts exactly that. No in-tree
// pattern uses `$` anywhere but in terminal position; this is reachable only
// through a caller-supplied searched_line on the §7 client API.
func pySearch(re *regexp.Regexp, line string) bool {
	if re.MatchString(line) {
		return true
	}
	if strings.HasSuffix(line, "\n") {
		return re.MatchString(line[:len(line)-1])
	}
	return false
}

// WriteLineBefore is FilesystemMixin.write_line_before.
//
// lineToAdd is inserted, followed by "\n" and with no indentation copied from
// the match, before every line whose raw text matches searchedLine — or before
// only the first such line when firstOccurrence is set. It returns the number
// of lines added; no match is 0 and not an error.
//
// Error order matches Python exactly: nil FS first, then regexp compilation,
// then the file access (so a bad pattern reports itself even when the file is
// missing). A missing path yields fs.ErrNotExist, a directory yields
// ErrFileExpected.
func WriteLineBefore(fsys FS, filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	return editLines(fsys, filePath, searchedLine, func(line string, matched bool, out []string) []string {
		if matched {
			out = append(out, lineToAdd+"\n")
		}
		return append(out, normalizeLine(line))
	}, firstOccurrence)
}

// WriteLineAfter is FilesystemMixin.write_line_after.
//
// Same contract as WriteLineBefore, inserting after the match. When the matched
// line carries no terminator (last line of a file that does not end in a
// newline) a "\n" is emitted first so the inserted line starts on its own line.
// That test is made against the RAW line, so a line ending "\r" also gets the
// extra "\n" (probe A2: "a\r\nd\r" + after "d" -> "a\nd\r\nX\n").
func WriteLineAfter(fsys FS, filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	return editLines(fsys, filePath, searchedLine, func(line string, matched bool, out []string) []string {
		out = append(out, normalizeLine(line))
		if matched {
			prefix := ""
			if !strings.HasSuffix(line, "\n") {
				prefix = "\n"
			}
			out = append(out, prefix+lineToAdd+"\n")
		}
		return out
	}, firstOccurrence)
}

// DeleteLine is FilesystemMixin.delete_line: drops every matching line (or only
// the first), returning how many were dropped.
func DeleteLine(fsys FS, filePath, lineToDelete string, firstOccurrence bool) (int, error) {
	return editLines(fsys, filePath, lineToDelete, func(line string, matched bool, out []string) []string {
		if matched {
			return out
		}
		return append(out, normalizeLine(line))
	}, firstOccurrence)
}

// editLines is the read/rewrite skeleton the three helpers share. Python does
// readlines + seek(0) + truncate() + writelines on one "r+" handle; the file is
// rewritten unconditionally, including when nothing matched, which is why a
// CRLF file loses its CRs on a zero-hit call (probe N1).
func editLines(
	fsys FS,
	filePath, pattern string,
	emit func(line string, matched bool, out []string) []string,
	firstOccurrence bool,
) (int, error) {
	if fsys == nil {
		return 0, ErrNoFilesystem
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return 0, err
	}
	cleaned, err := CleanPath(filePath)
	if err != nil {
		return 0, err
	}
	// ReadFile makes the pyfilesystem distinction the tests assert:
	// fs.ErrNotExist for a missing path, ErrFileExpected for a directory.
	content, err := ReadFile(fsys, cleaned)
	if err != nil {
		return 0, err
	}

	n := 0
	var out []string
	for _, line := range SplitLines(content) {
		matched := pySearch(re, line) && (!firstOccurrence || n == 0)
		if matched {
			n++
		}
		out = emit(line, matched, out)
	}
	if err := writeAll(fsys, cleaned, []byte(strings.Join(out, ""))); err != nil {
		return 0, err
	}
	return n, nil
}
