//go:build windows

package settings

import (
	"os"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// expandUser is `ntpath.expanduser`, which is what `os.path.expanduser`
// resolves to on Windows. It shares nothing with the POSIX one but its name.
//
// The differences that matter:
//
//   - Both "\\" and "/" end the user component.
//   - The home comes from `%USERPROFILE%`, or from `%HOMEDRIVE%%HOMEPATH%`
//     when that is unset; with neither, the path is returned unchanged.
//   - `~user` for a *different* user is guessed by swapping the last component
//     of the current user's profile directory, and only when that last
//     component actually is `%USERNAME%`. There is no passwd database to ask,
//     so an unverifiable guess or the unchanged path are the only two answers
//     available, and CPython picks the guess.
//
// The splitting and joining go through the ported `ntpath` primitives rather
// than through `path/filepath`, which is not the same function: `filepath.Base`
// of a `%USERPROFILE%` with a trailing separator answers the last component
// where `ntpath.basename` answers "" — and that empty string is what makes
// CPython bail out of the `~user` guess and return the path unchanged. Join
// and Dir differ too, because both run Clean.
func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}

	i := 1
	for i < len(path) && path[i] != '\\' && path[i] != '/' {
		i++
	}

	var userhome string
	if profile, ok := os.LookupEnv("USERPROFILE"); ok {
		userhome = profile
	} else {
		homepath, ok := os.LookupEnv("HOMEPATH")
		if !ok {
			return path
		}
		userhome = util.NTJoin(os.Getenv("HOMEDRIVE"), homepath)
	}

	if i != 1 {
		targetUser := path[1:i]
		currentUser, ok := os.LookupEnv("USERNAME")
		if !ok {
			// `os.environ.get('USERNAME')` is None, which is equal to neither
			// the target user (a non-empty string here) nor any basename, so
			// both comparisons below fail and CPython returns the path
			// unchanged. An empty `%USERNAME%` is *not* the same value and
			// falls through to the comparisons.
			return path
		}
		if targetUser != currentUser {
			dirname, basename := util.NTSplit(userhome)
			if currentUser != basename {
				return path
			}
			userhome = util.NTJoin(dirname, targetUser)
		}
	}

	return userhome + path[i:]
}
