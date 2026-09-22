//go:build unix

package util

import "golang.org/x/sys/unix"

// AccessXOK is `os.access(path, os.X_OK)`.
func AccessXOK(path string) bool {
	return unix.Access(path, unix.X_OK) == nil
}
