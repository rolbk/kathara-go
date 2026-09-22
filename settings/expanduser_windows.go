//go:build windows

package settings

import (
	"os"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// expandUser is `ntpath.expanduser`, which is what `os.path.expanduser`
// resolves to on Windows. It shares nothing with the POSIX one but its name.
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
