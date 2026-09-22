//go:build unix

// This file is the posixpath half of what [pyWhich] needs: `split`, `join`,
// `normcase` and the `_access_check` predicate, each spelled the way CPython
// spells it rather than the way path/filepath does. The difference that
// matters is that posixpath never normalises — `join(".", "kathara")` is
// "./kathara" and `join("/a/../b", "kathara")` is "/a/../b/kathara" — while
// every filepath helper runs Clean.

package util

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// defaultSearchPath is what `shutil.which` searches when PATH is *unset*:
// `os.confstr("CS_PATH")`, falling back to `posixpath.defpath`. Both are
// "/bin:/usr/bin" on Linux (measured against the oracle); macOS's confstr
// additionally lists the two sbin directories, so a Kathará installed there
// and invoked with no PATH at all would be found by Python and not here.
const defaultSearchPath = "/bin:/usr/bin"

// pySplitPath is `posixpath.split`: everything up to the last separator, with
// the trailing separators stripped off the head unless the head is nothing but
// separators (the root, which keeps its slash).
func pySplitPath(p string) (head string, tail string) {
	i := strings.LastIndexByte(p, '/') + 1
	head, tail = p[:i], p[i:]

	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head, tail
}

// pyJoinPath is `posixpath.join` for two components.
func pyJoinPath(a, b string) string {
	switch {
	case strings.HasPrefix(b, "/"):
		return b
	case a == "" || strings.HasSuffix(a, "/"):
		return a + b
	default:
		return a + "/" + b
	}
}

// pyNormCase is `posixpath.normcase`, which returns the path unchanged.
func pyNormCase(p string) string {
	return p
}

// whichCurdir is the current-directory entry `shutil.which` prepends to PATH.
// Only the Windows branch has one.
func whichCurdir(string) []string {
	return nil
}

// whichFiles is the list of names tried in each directory. Unix has no
// PATHEXT, so it is the command itself.
func whichFiles(cmd string) []string {
	return []string{cmd}
}

// whichAccessCheck is `shutil._access_check(fn, os.F_OK | os.X_OK)`:
// `os.path.exists(fn) and os.access(fn, mode) and not os.path.isdir(fn)`.
func whichAccessCheck(name string) bool {
	info, err := os.Stat(name)
	if err != nil {
		// os.path.exists is False for every failure, including the
		// ValueError an embedded NUL raises.
		return false
	}
	if unix.Access(name, unix.F_OK|unix.X_OK) != nil {
		return false
	}
	return !info.IsDir()
}
