package util

import (
	"os"
	"path/filepath"
)

// realPath is `os.path.realpath(filename)` with `strict=False` on Windows,
// i.e. ntpath.realpath, which is a different algorithm from the POSIX one
// rather than the same one with different separators.
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
