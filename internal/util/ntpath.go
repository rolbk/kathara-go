// This file is the `ntpath` primitives the Windows build needs: `splitroot`,
// `split`, `join` and `normcase`. path/filepath cannot stand in for any of
// them — `filepath.Join` runs Clean, so it turns ntpath's `join("C:", "")` =
// "C:" into "C:." and collapses the `..` segments ntpath leaves alone, and
// `filepath.Split` keeps the separator on the head that ntpath strips.
// They are compiled on every platform, not only on Windows, so the vectors
// recorded from the oracle's `ntpath` (a pure-Python module that imports
// anywhere) can be checked by the test suite on the host that runs it.

package util

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// ntSeps are the two characters ntpath accepts as separators.
const ntSeps = "\\/"

// pyUpper is Python's `str.upper()`, the mirror of [pyLower]: the full case
// mapping, where [strings.ToUpper] is the simple one. `shutil.which` folds the
// command and each PATHEXT entry through it before comparing them. Swept
// against the oracle over the whole code space, no difference.
func pyUpper(s string) string {
	return cases.Upper(language.Und).String(s)
}

// ntSplitRoot is `ntpath.splitroot`: the drive (or UNC share), the root
// separator if the path has one, and the rest. The other two are built on it.
func ntSplitRoot(p string) (drive, root, tail string) {
	normp := strings.ReplaceAll(p, "/", "\\")

	switch {
	case strings.HasPrefix(normp, "\\"):
		if !strings.HasPrefix(normp, "\\\\") {
			// A rooted relative path, e.g. `\Windows`.
			return "", p[:1], p[1:]
		}

		// A UNC share (`\\server\share`) or a device path (`\\.\device`),
		// either of which may carry the `\\?\UNC\` prefix.
		start := 2
		if len(normp) >= 8 && strings.EqualFold(normp[:8], "\\\\?\\UNC\\") {
			start = 8
		}
		index := strings.IndexByte(normp[start:], '\\')
		if index < 0 {
			return p, "", ""
		}
		index += start
		index2 := strings.IndexByte(normp[index+1:], '\\')
		if index2 < 0 {
			return p, "", ""
		}
		index2 += index + 1
		return p[:index2], p[index2 : index2+1], p[index2+1:]

	case len(normp) >= 2 && normp[1] == ':':
		if len(normp) >= 3 && normp[2] == '\\' {
			// An absolute drive-letter path, e.g. `X:\Windows`.
			return p[:2], p[2:3], p[3:]
		}
		// A path relative to a drive's current directory, e.g. `X:Windows`.
		return p[:2], "", p[2:]

	default:
		return "", "", p
	}
}

// ntSplit is `ntpath.split`: everything up to the last separator, with the
// separators stripped off the head, and the root kept in front of it.
func ntSplit(p string) (head string, tail string) {
	drive, root, rest := ntSplitRoot(p)

	i := len(rest)
	for i > 0 && !strings.ContainsRune(ntSeps, rune(rest[i-1])) {
		i--
	}
	head, tail = rest[:i], rest[i:]

	return drive + root + strings.TrimRight(head, ntSeps), tail
}

// ntJoin is `ntpath.join` for two components. The drive handling is the whole
// reason it cannot be `a + "\\" + b`: a second component naming a different
// drive discards the first one entirely, and a rooted second component keeps
// the first one's drive only if it has none of its own.
func ntJoin(a, b string) string {
	resultDrive, resultRoot, resultPath := ntSplitRoot(a)
	pDrive, pRoot, pPath := ntSplitRoot(b)

	differentDrives := pDrive != "" && pDrive != resultDrive && !strings.EqualFold(pDrive, resultDrive)

	switch {
	case pRoot != "":
		// The second path is absolute.
		if pDrive != "" || resultDrive == "" {
			resultDrive = pDrive
		}
		resultRoot, resultPath = pRoot, pPath

	case differentDrives:
		// Different drives: the first path is ignored entirely.
		resultDrive, resultRoot, resultPath = pDrive, pRoot, pPath

	default:
		if pDrive != "" && pDrive != resultDrive {
			// The same drive spelled in a different case.
			resultDrive = pDrive
		}
		// The second path is relative to the first.
		if resultPath != "" && !strings.ContainsRune(ntSeps, rune(resultPath[len(resultPath)-1])) {
			resultPath += "\\"
		}
		resultPath += pPath
	}

	// A UNC prefix with no root of its own still needs a separator before a
	// non-empty tail; a drive letter, which ends in a colon, does not.
	if resultPath != "" && resultRoot == "" && resultDrive != "" &&
		!strings.ContainsRune(":"+ntSeps, rune(resultDrive[len(resultDrive)-1])) {
		return resultDrive + "\\" + resultPath
	}
	return resultDrive + resultRoot + resultPath
}

// ntNormCase is `ntpath.normcase`: forward slashes become backslashes and the
// result is lower-cased. Python calls `LCMapStringEx` with the invariant
// locale, which is Unicode's *simple* lower-case mapping — [strings.ToLower],
// not [pyLower]. Its only use is the duplicate-directory set inside [pyWhich],
// so it never reaches a returned string.
func ntNormCase(p string) string {
	if p == "" {
		return p
	}
	return strings.ToLower(strings.ReplaceAll(p, "/", "\\"))
}
