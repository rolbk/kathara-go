//go:build unix && !linux

package util

import (
	"os"
	"os/user"
	"strconv"
)

// GetCurrentUserHome is utils.get_current_user_home (utils.py:212) on macOS,
// where `exec_by_platform` picks the `default_home` arm (utils.py:217) — the
// same one Windows gets, and not the passwd lookup Linux gets. Under `sudo`
// this therefore returns /root, while the Linux build returns the invoking
// user's home; the asymmetry is Python's (see [GetCurrentUserHome] on Linux).
func GetCurrentUserHome() (string, error) {
	home, ok := os.LookupEnv("HOME")
	if !ok {
		entry, err := user.LookupId(strconv.Itoa(os.Getuid()))
		if err != nil {
			// expanduser gives back the unexpanded "~" when it cannot
			// resolve the user, and so does this.
			return "~", nil
		}
		home = entry.HomeDir
	}

	// userhome.rstrip('/') ... or '/'
	end := len(home)
	for end > 0 && home[end-1] == '/' {
		end--
	}
	if end == 0 {
		return "/", nil
	}

	return home[:end], nil
}
