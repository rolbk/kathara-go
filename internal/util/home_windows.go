package util

import (
	"os"
)

// GetCurrentUserHome is utils.get_current_user_home (utils.py:212) on Windows,
// the `default_home` arm (utils.py:217): `os.path.expanduser('~')`, which is
// ntpath.expanduser and not the POSIX one.
//
// `%USERPROFILE%` wins outright — even when it is empty, which is the one case
// where this and the POSIX version disagree about more than separators, since
// ntpath does not right-strip and does not substitute a root. Otherwise
// `%HOMEDRIVE%%HOMEPATH%`, and failing that the literal "~", unexpanded.
//
// The join is [pyJoinPath], `ntpath.join`, and not [path/filepath.Join]:
// filepath runs Clean over the result, which would turn an empty `%HOMEPATH%`
// into "C:." instead of "C:" and would collapse any `..` the variable carries.
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
