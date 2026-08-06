package util

import "golang.org/x/sys/windows"

var (
	modshell32           = windows.NewLazySystemDLL("shell32.dll")
	procIsUserAnAdmin    = modshell32.NewProc("IsUserAnAdmin")
	procIsUserAnAdminErr = procIsUserAnAdmin.Find()
)

// IsAdmin is utils.is_admin (utils.py:201) on Windows:
// `ctypes.windll.shell32.IsUserAnAdmin() != 0`.
//
// The function reports whether the *current token* is elevated and a member of
// the Administrators group, which is not the same question as "is this account
// an administrator": on a UAC-split token it answers no until the process is
// run elevated, which is the behaviour Kathará wants.
//
// Python's ctypes raises when the symbol is missing; the lookup is done once
// here and surfaced as an error rather than a panic.
func IsAdmin() (bool, error) {
	if procIsUserAnAdminErr != nil {
		return false, procIsUserAnAdminErr
	}

	// IsUserAnAdmin takes no arguments and sets no last error.
	result, _, _ := procIsUserAnAdmin.Call()

	return result != 0, nil
}
