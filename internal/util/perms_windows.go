package util

import (
	"os"

	"golang.org/x/sys/windows"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// accessRights are the CreateFileW access masks of [permissionFlags], in the
// same order (utils.py:310-312).
var accessRights = [3]uint32{windows.GENERIC_READ, windows.GENERIC_WRITE, windows.GENERIC_EXECUTE}

// CheckDirectoryPermissions is utils.check_directory_permissions
// (utils.py:268) on Windows.
//
// Windows has no access(2), and an NTFS ACL does not reduce to three bits, so
// Python does not try: it *opens* the directory once per access right and
// treats a failed open as a missing permission (utils.py:338-384). This does
// the same, with the same masks, the same FILE_SHARE_READ|FILE_SHARE_WRITE and
// the same OPEN_EXISTING.
//
// FILE_FLAG_BACKUP_SEMANTICS is what makes it work at all: without it
// CreateFileW refuses to open a directory.
func CheckDirectoryPermissions(path string, mode string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, kerrors.NewPathNotExist(path)
	}
	if !info.IsDir() {
		return nil, kerrors.NewPathNotDirectory(path)
	}

	return missingPermissions(mode, func(i int) bool {
		return canOpenDirectory(path, accessRights[i])
	}), nil
}

// canOpenDirectory is one iteration of the CreateFileW probe. A path that
// cannot be encoded as UTF-16 could not have been opened either, so it counts
// as a refusal rather than an error of its own.
func canOpenDirectory(path string, access uint32) bool {
	wide, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}

	handle, err := windows.CreateFile(
		wide,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return false
	}

	// Python closes the handle on the success arm.
	_ = windows.CloseHandle(handle)

	return true
}
