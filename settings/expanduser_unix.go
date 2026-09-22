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
