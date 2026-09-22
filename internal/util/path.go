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

var ErrExecutableNotFound = errors.New("util: executable not found")

// GetExecutablePath is utils.get_executable_path (utils.py:65): the command
// line that re-invokes this program, ready to be embedded in the argument of
// an external terminal emulator.
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
