package util

import (
	"os"
	"path/filepath"
)

// realPath is `os.path.realpath(filename)` with `strict=False` on Windows,
// i.e. ntpath.realpath, which is a different algorithm from the POSIX one
// rather than the same one with different separators.
//
// Windows resolves a path by opening it and asking the kernel for the
// canonical name (`GetFinalPathNameByHandle`), so there is no component walk
// and no symlink stack. What ntpath adds on top is the non-strict fallback
// (`_getfinalpathname_nonstrict`): when the open fails, it drops the last
// component and retries, until either something resolves — in which case the
// dropped tail is appended to it — or the path runs out. A missing directory
// therefore resolves as far as its existing ancestors and keeps the rest
// verbatim, which is the behaviour the Unix build reaches by ignoring lstat
// errors.
//
// [path/filepath.EvalSymlinks] is the `GetFinalPathNameByHandle` step: it
// opens the path, resolves reparse points, and strips the `\\?\` prefix the
// API returns, which is the same normalisation ntpath does on its own result.
// The walk-up loop around it is this function.
func realPath(filename string) (string, error) {
	// `if not had_prefix and not isabs(path): path = join(cwd, path)` — Abs
	// also applies the normpath ntpath.realpath opens with.
	path, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}

	var tail string
	for {
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			if tail == "" {
				return resolved, nil
			}
			return filepath.Join(resolved, tail), nil
		}

		parent, name := filepath.Split(path)
		parent = trimTrailingSeparator(parent)

		// `if path and not name: return join(path, tail)` — the root, whose
		// own resolution failed and which has no further ancestor.
		if name == "" {
			if tail == "" {
				return path, nil
			}
			return filepath.Join(path, tail), nil
		}

		if tail == "" {
			tail = name
		} else {
			tail = filepath.Join(name, tail)
		}

		if parent == "" {
			return tail, nil
		}
		path = parent
	}
}

// trimTrailingSeparator undoes the separator filepath.Split leaves on the
// directory half, matching ntpath.split, which strips it except on the root.
func trimTrailingSeparator(dir string) string {
	trimmed := dir
	for len(trimmed) > 0 && os.IsPathSeparator(trimmed[len(trimmed)-1]) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	// A bare "C:" or "\\\\host\\share" must keep its separator to stay a root.
	if trimmed == "" || trimmed == filepath.VolumeName(dir) {
		return dir
	}
	return trimmed
}
