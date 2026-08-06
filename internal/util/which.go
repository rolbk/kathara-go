// This file is the port of `shutil.which`, the PATH search behind
// utils.get_executable_path (utils.py:78).
//
// It is here rather than expressed as [os/exec.LookPath] because the two are
// not the same function. `which`'s result is interpolated straight into a
// shell or `osascript` command line (`cli/ui/utils.py:133`), so the exact
// string matters, and `LookPath` differs from `shutil.which` in what it does
// with a relative PATH entry (it `Clean`s the join, turning "./kathara" into
// "kathara"), in what it does when PATH is unset, and in which uid it tests
// access for on Unix. The platform files supply the four pieces that differ
// between posixpath and ntpath.

package util

import (
	"os"
	"strings"
)

// pyWhich is `shutil.which(cmd)` with the default mode, `os.F_OK | os.X_OK`.
// It returns "" for Python's None.
//
// The structure is Python's, in order:
//
//  1. A cmd with a directory part is looked up in that directory only, and
//     never on PATH — which is how `get_executable_path` can be handed an
//     `argv[0]` of "./kathara" and find it.
//  2. Otherwise PATH decides. An *unset* PATH falls back to the platform
//     default; a PATH set to the empty string finds nothing at all, which is
//     not the same thing (bpo-35755).
//  3. Each directory is tried once — repeats are skipped by normalised name,
//     which on Windows makes the skip case-insensitive.
func pyWhich(cmd string) string {
	dirname, base := pySplitPath(cmd)

	var dirs []string
	if dirname != "" {
		dirs = []string{dirname}
	} else {
		searchPath, ok := os.LookupEnv("PATH")
		if !ok {
			searchPath = defaultSearchPath
		}
		if searchPath == "" {
			return ""
		}
		// `path.split(os.pathsep)`, not filepath.SplitList: Python does not
		// strip the quotes Windows allows around a PATH entry.
		dirs = append(whichCurdir(base), strings.Split(searchPath, string(os.PathListSeparator))...)
	}

	files := whichFiles(base)

	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		norm := pyNormCase(dir)
		if _, repeat := seen[norm]; repeat {
			continue
		}
		seen[norm] = struct{}{}

		for _, file := range files {
			name := pyJoinPath(dir, file)
			if whichAccessCheck(name) {
				return name
			}
		}
	}

	return ""
}
