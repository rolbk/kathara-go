// This file holds the platform-independent half of
// utils.check_directory_permissions (utils.py:268): which permissions the
// caller asked about, in which order they are reported, and what they are
// called in the message. The probe itself is access(2) on Unix and a
// CreateFileW attempt on Windows, and lives in perms_unix.go / perms_windows.go.

package util

import "strings"

var (
	permissionFlags  = [3]byte{'r', 'w', 'x'}
	permissionLabels = [3]string{"read (r)", "write (w)", "execute (x)"}
)

// missingPermissions applies the mode scan and the probe order.
func missingPermissions(mode string, permitted func(i int) bool) []string {
	missing := make([]string, 0, len(permissionFlags))

	for i, flag := range permissionFlags {
		if strings.IndexByte(mode, flag) >= 0 && !permitted(i) {
			missing = append(missing, permissionLabels[i])
		}
	}

	return missing
}
