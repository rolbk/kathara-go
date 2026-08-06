//go:build !windows

package settings

import (
	"os"
	"os/user"
	"strconv"
	"strings"
)

// expandUser is `posixpath.expanduser`, which is what
// `os.path.expanduser` resolves to off Windows.
//
// It is spelled out rather than replaced by `os.UserHomeDir` because the
// details are load-bearing for the one caller,
// [ValidateDockerConfigJSON]:
//
//   - `$HOME` wins over the passwd entry. Under `sudo`, `$HOME` is root's, so
//     `~/.docker/config.json` reads root's file — the *opposite* of the
//     sudo-aware home this package uses for `kathara.conf` itself. Both
//     behaviours are Python's, and they disagree on purpose only in the sense
//     that nobody noticed.
//   - Every failure returns the path unchanged rather than raising: no HOME
//     and no passwd entry, an unknown `~user`, all of them.
//   - The expansion stops at the first "/", so `~` and `~/x` and `~user/x` are
//     the only forms; a `~` anywhere but at position 0 is not special.
func expandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}

	i := strings.IndexByte(path[1:], '/')
	if i < 0 {
		i = len(path)
	} else {
		i += 1
	}

	var userhome string
	if i == 1 {
		home, ok := os.LookupEnv("HOME")
		if !ok {
			entry, err := user.LookupId(strconv.Itoa(os.Getuid()))
			if err != nil {
				// `except KeyError: return path`.
				return path
			}
			home = entry.HomeDir
		}
		userhome = home
	} else {
		entry, err := user.Lookup(path[1:i])
		if err != nil {
			return path
		}
		userhome = entry.HomeDir
	}

	userhome = strings.TrimRight(userhome, "/")

	// `return (userhome + path[i:]) or root`: a root home of "/" with a bare
	// "~" would otherwise expand to the empty string.
	if expanded := userhome + path[i:]; expanded != "" {
		return expanded
	}
	return "/"
}
