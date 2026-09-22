package vfs

import (
	"regexp"
	"strings"
)

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
func normalizeLine(line string) string {
	return strings.ReplaceAll(strings.ReplaceAll(line, "\n\r", "\n"), "\r\n", "\n")
}

// pySearch is Python's re.search over a line that still carries its terminator.
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
func WriteLineBefore(fsys FS, filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	return editLines(fsys, filePath, searchedLine, func(line string, matched bool, out []string) []string {
		if matched {
			out = append(out, lineToAdd+"\n")
		}
		return append(out, normalizeLine(line))
	}, firstOccurrence)
}

// WriteLineAfter is FilesystemMixin.write_line_after.
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
