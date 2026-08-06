// This file binds [pyWhich]'s four platform hooks to the `ntpath` primitives
// and adds the two steps `shutil.which` performs on Windows only: the PATHEXT
// expansion and the current-directory entry in front of PATH.

package util

import (
	"os"
	"strings"
)

// defaultSearchPath is what `shutil.which` searches when PATH is unset.
// `os.confstr` does not exist on Windows, so Python falls through to
// `ntpath.defpath`.
const defaultSearchPath = ".;C:\\bin"

// pySplitPath is `ntpath.split`.
func pySplitPath(p string) (head string, tail string) { return ntSplit(p) }

// pyJoinPath is `ntpath.join` for two components.
func pyJoinPath(a, b string) string { return ntJoin(a, b) }

// pyNormCase is `ntpath.normcase`.
func pyNormCase(p string) string { return ntNormCase(p) }

// whichCurdir is the current-directory entry `shutil.which` puts in front of
// PATH on Windows.
//
// Python asks `NeedCurrentDirectoryForExePath`, which answers "no" only when
// the `NoDefaultCurrentDirectoryInExePath` variable is defined *and* the
// command has no path separator in it. [pyWhich] reaches this only for a
// command with no separator, so the environment variable decides on its own —
// the same reduction Go's own exec.LookPath makes.
func whichCurdir(string) []string {
	if _, defined := os.LookupEnv("NoDefaultCurrentDirectoryInExePath"); defined {
		return nil
	}
	return []string{"."}
}

// winDefaultPathExt is shutil's `_WIN_DEFAULT_PATHEXT`, used when PATHEXT is
// unset or empty.
const winDefaultPathExt = ".COM;.EXE;.BAT;.CMD;.VBS;.JS;.WS;.MSC"

// whichFiles is the PATHEXT expansion: the command with each extension
// appended, and the bare command in front of them when it already carries one
// of those extensions. The bare-command test is what makes `which("cmd.exe")`
// find `cmd.exe` rather than `cmd.exe.COM`.
func whichFiles(cmd string) []string {
	source := os.Getenv("PATHEXT")
	if source == "" {
		source = winDefaultPathExt
	}

	var pathext []string
	for _, ext := range strings.Split(source, ";") {
		if ext == "" {
			continue
		}
		pathext = append(pathext, strings.TrimRight(ext, "."))
	}

	files := make([]string, 0, len(pathext)+1)
	normcmd := pyUpper(cmd)
	for _, ext := range pathext {
		if strings.HasSuffix(normcmd, pyUpper(ext)) {
			files = append(files, cmd)
			break
		}
	}
	for _, ext := range pathext {
		files = append(files, cmd+ext)
	}

	return files
}

// whichAccessCheck is `shutil._access_check(fn, os.F_OK | os.X_OK)`.
// `os.access` on Windows only ever fails for a mode that includes `W_OK`, so
// with these two flags it reduces to "the attributes could be read", i.e. the
// path exists.
func whichAccessCheck(name string) bool {
	info, err := os.Stat(name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}
