//go:build unix

package util

import "os"

// IsAdmin is utils.is_admin (utils.py:201) on Unix: `os.getuid() == 0`.
//
// It is the *real* uid, not the effective one, and the difference is visible
// on the setuid-root Docker installs Kathará supports: `cmd/kathara` drops
// effective privileges at startup (PrivilegeHandler), after which euid is the
// invoking user's while ruid stays 0 — so an effective-uid check would answer
// "no" in exactly the situation this is asked about. Callers use it to decide
// whether `--all` may list other users' devices (ListCommand.py:57,
// WipeCommand.py:65) and whether a privileged device may start
// (DockerMachine.py:328).
//
// The error is always nil here; it exists because the Windows arm calls into
// shell32 and can fail (PACKAGE_GRAPH.md §4).
func IsAdmin() (bool, error) {
	return os.Getuid() == 0, nil
}
