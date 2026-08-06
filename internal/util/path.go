// This file covers the two path helpers of utils.py: `get_absolute_path`,
// which every path-taking command runs its lab directory through, and
// `get_executable_path`, which the terminal spawner uses to build a command
// line that re-invokes Kathará.

package util

import (
	"errors"
	"os"
)

// GetAbsolutePath is utils.get_absolute_path (utils.py:60).
//
//	abs_path = os.path.realpath(path)
//	return abs_path if not os.path.islink(abs_path) else os.readlink(abs_path)
//
// The second line is the surprising one, and it is ported as written
// (DIVERGENCES.md). `realpath` has already resolved every symlink it could,
// so the only way its result is still a symlink is that resolution stopped —
// which happens for exactly one input class, a symlink loop, where CPython's
// non-strict `realpath` gives up and hands back the path unchanged. The
// `readlink` then returns the link's *target as stored*, which for a
// relative-target loop is a relative path: a lab directory called `loop1`
// pointing at `loop2` pointing back at `loop1` makes `kathara lstart -d loop1`
// resolve to the bare string "loop2". The function's contract is "return an
// absolute path" and this is the one input for which it does not.
//
// Nothing else takes the branch. A dangling symlink resolves through to its
// (non-existent) target and is not a link; a chain of links resolves to the
// end; a missing path is returned as-is.
func GetAbsolutePath(path string) (string, error) {
	absPath, err := realPath(path)
	if err != nil {
		return "", err
	}

	// os.path.islink is False for every lstat failure, not only ENOENT.
	info, err := os.Lstat(absPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return absPath, nil
	}

	// os.readlink raises OSError here and no call site catches it.
	return os.Readlink(absPath)
}

// ErrExecutableNotFound is what utils.get_executable_path returns as None
// (utils.py:82): the Kathará executable is neither a file at the given path
// nor anywhere on PATH. Its only caller turns the None into a
// FileNotFoundError (cli/ui/utils.py:127, NILABILITY.tsv).
var ErrExecutableNotFound = errors.New("util: executable not found")

// GetExecutablePath is utils.get_executable_path (utils.py:65): the command
// line that re-invokes this program, ready to be embedded in the argument of
// an external terminal emulator.
//
// The returned string is **quoted** — `"/usr/local/bin/kathara"`, with the
// double quotes as part of the value — because every caller concatenates it
// into a shell or `osascript` command (PACKAGE_GRAPH.md §4, `external_*.go`).
// The quoting is naive: a path containing a double quote produces a broken
// command line in Python and does so here too.
//
// Two branches, in Python's order:
//
//  1. If [GetAbsolutePath] of the argument is an existing regular file, that
//     is the answer. Note what is *not* checked — the execute bit. A readable
//     non-executable file at the given path wins over a perfectly good
//     `kathara` on PATH.
//  2. Otherwise `shutil.which` of the *original* argument, unresolved
//     ([pyWhich], which is a port of it rather than [os/exec.LookPath]).
//
// Python has a third case between them: when the path ends in `kathara.py` it
// prefixes `sys.executable`, so a source checkout re-invokes itself through
// the interpreter. A Go binary has no interpreter to name and cannot be a
// `.py` file, so the branch is dropped rather than emulated.
func GetExecutablePath(execPath string) (string, error) {
	execAbsPath, err := GetAbsolutePath(execPath)
	if err != nil {
		return "", err
	}

	// os.path.exists(p) and os.path.isfile(p): one stat, following symlinks,
	// false on any error. A directory fails the second test and falls
	// through to the PATH search.
	if info, statErr := os.Stat(execAbsPath); statErr == nil && info.Mode().IsRegular() {
		return `"` + execAbsPath + `"`, nil
	}

	whichPath := pyWhich(execPath)
	if whichPath == "" {
		return "", ErrExecutableNotFound
	}

	return `"` + whichPath + `"`, nil
}
