// This file holds the platform-independent half of
// utils.check_directory_permissions (utils.py:268): which permissions the
// caller asked about, in which order they are reported, and what they are
// called in the message. The probe itself is access(2) on Unix and a
// CreateFileW attempt on Windows, and lives in perms_unix.go / perms_windows.go.

package util

import "strings"

// permissionFlags are the characters `mode` is scanned for, and
// permissionLabels the strings the error message interpolates for each. The
// order of both is read, write, execute, which the message text depends on
// (ORDERING.tsv, utils.py:289-296 and :338-384 — Python spells it twice, once
// per platform, in the same order).
var (
	permissionFlags  = [3]byte{'r', 'w', 'x'}
	permissionLabels = [3]string{"read (r)", "write (w)", "execute (x)"}
)

// missingPermissions applies the mode scan and the probe order.
//
// `mode` is scanned for characters, not parsed: Python writes `'r' in mode`,
// so the Docker convention "ro" requests read alone (the 'o' means nothing to
// this function), "rw" requests read and write, and an unrecognised mode
// string quietly requests whichever of the three letters it happens to
// contain. permitted is called only for the letters that are present, and its
// argument indexes [permissionFlags].
//
// The result is empty rather than nil when nothing is missing, matching
// Python's `[]`.
func missingPermissions(mode string, permitted func(i int) bool) []string {
	missing := make([]string, 0, len(permissionFlags))

	for i, flag := range permissionFlags {
		if strings.IndexByte(mode, flag) >= 0 && !permitted(i) {
			missing = append(missing, permissionLabels[i])
		}
	}

	return missing
}
