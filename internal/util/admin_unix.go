//go:build unix

package util

import "os"

// IsAdmin is utils.is_admin (utils.py:201) on Unix: `os.getuid() == 0`.
func IsAdmin() (bool, error) {
	return os.Getuid() == 0, nil
}
