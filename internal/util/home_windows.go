package util

import (
	"os"
)

// GetCurrentUserHome is utils.get_current_user_home (utils.py:212) on Windows,
// the `default_home` arm (utils.py:217): `os.path.expanduser('~')`, which is
// ntpath.expanduser and not the POSIX one.
func GetCurrentUserHome() (string, error) {
	if profile, ok := os.LookupEnv("USERPROFILE"); ok {
		return profile, nil
	}

	homePath, ok := os.LookupEnv("HOMEPATH")
	if !ok {
		return "~", nil
	}

	return pyJoinPath(os.Getenv("HOMEDRIVE"), homePath), nil
}
