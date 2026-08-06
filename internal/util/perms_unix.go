//go:build unix

package util

import (
	"os"

	"golang.org/x/sys/unix"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// accessModes are the access(2) masks of [permissionFlags], in the same order.
var accessModes = [3]uint32{unix.R_OK, unix.W_OK, unix.X_OK}

// CheckDirectoryPermissions is utils.check_directory_permissions
// (utils.py:268) on Unix, the pre-flight for a host directory a device is
// about to bind-mount (DockerMachine.py:315, KubernetesMachine.py:393).
//
// It returns the permissions the caller asked for and does *not* have, as the
// strings the error message interpolates, in the order [missingPermissions]
// fixes.
//
// Both failures are Python's, including the inverted name: a path that does
// not exist raises `FileExistsError` (utils.py:282).
//
// `os.access` is [unix.Access], the access(2) syscall, which answers for the
// **real** uid and gid rather than the effective ones. That matters on a
// setuid-root install after `cmd/kathara` drops effective privileges, and it
// means running as root reports nothing missing at all — root always has
// search permission on a directory whatever its mode, so a 0000 directory
// passes every probe.
func CheckDirectoryPermissions(path string, mode string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		// os.path.exists is False for every stat failure, not only ENOENT.
		return nil, kerrors.NewPathNotExist(path)
	}
	if !info.IsDir() {
		return nil, kerrors.NewPathNotDirectory(path)
	}

	return missingPermissions(mode, func(i int) bool {
		return unix.Access(path, accessModes[i]) == nil
	}), nil
}
