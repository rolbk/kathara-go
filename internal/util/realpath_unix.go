//go:build !windows

package util

import (
	"os"
	"strings"
	"syscall"
)

// realpathFrame is one entry of the work stack of [realPath]. CPython pushes a
// bare `None` where this carries `resolved`, to mark that the next pop is a
// symlink path whose target has just been fully resolved.
type realpathFrame struct {
	name     string
	resolved bool
}

// realPath is `os.path.realpath(filename)` with `strict=False`, i.e.
// posixpath.realpath as CPython 3.13 implements it.
func realPath(filename string) (string, error) {
	const sep = "/"

	parts := strings.Split(filename, sep)
	rest := make([]realpathFrame, 0, len(parts)+8)
	for i := len(parts) - 1; i >= 0; i-- {
		rest = append(rest, realpathFrame{name: parts[i]})
	}

	// The number of real components still to process. It is not len(rest):
	// the resolved-symlink markers do not count, and the loop must stop as
	// soon as the last real component is consumed even if markers remain.
	partCount := len(parts)

	// The resolved prefix, absolute throughout.
	var path string
	if strings.HasPrefix(filename, sep) {
		path = sep
	} else {
		cwd, err := getcwd()
		if err != nil {
			return "", err
		}
		path = cwd
	}

	seen := make(map[string]string)

	for partCount > 0 {
		frame := rest[len(rest)-1]
		rest = rest[:len(rest)-1]

		if frame.resolved {
			// The entry below the marker is the symlink whose target the
			// intervening iterations just finished resolving.
			link := rest[len(rest)-1]
			rest = rest[:len(rest)-1]
			seen[link.name] = path
			continue
		}

		partCount--

		name := frame.name
		if name == "" || name == "." {
			continue
		}
		if name == ".." {
			if i := strings.LastIndex(path, sep); i > 0 {
				path = path[:i]
			} else {
				path = sep
			}
			continue
		}

		newPath := path + sep + name
		if path == sep {
			newPath = path + name
		}

		info, err := os.Lstat(newPath)
		if err != nil {
			// Non-strict mode ignores every OSError and takes the component
			// at face value, which is how a missing path resolves.
			path = newPath
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			path = newPath
			continue
		}
		if cached, ok := seen[newPath]; ok {
			if cached != "" {
				path = cached
				continue
			}
			// Still being resolved: a symlink loop. Non-strict resolution
			// stops here and keeps the link path itself.
			path = newPath
			continue
		}

		target, err := os.Readlink(newPath)
		if err != nil {
			path = newPath
			continue
		}

		if strings.HasPrefix(target, sep) {
			path = sep
		}

		seen[newPath] = ""
		rest = append(rest, realpathFrame{name: newPath})
		rest = append(rest, realpathFrame{resolved: true})

		targetParts := strings.Split(target, sep)
		for i := len(targetParts) - 1; i >= 0; i-- {
			rest = append(rest, realpathFrame{name: targetParts[i]})
		}
		partCount += len(targetParts)
	}

	return path, nil
}

// getcwd is `os.getcwd()`, the getcwd(2) result and nothing else.
func getcwd() (string, error) {
	if syscall.ImplementsGetwd {
		if dir, err := syscall.Getwd(); err == nil {
			return dir, nil
		}
	}
	return os.Getwd()
}
