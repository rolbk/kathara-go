package util

import "golang.org/x/sys/windows"

var (
	modshell32           = windows.NewLazySystemDLL("shell32.dll")
	procIsUserAnAdmin    = modshell32.NewProc("IsUserAnAdmin")
	procIsUserAnAdminErr = procIsUserAnAdmin.Find()
)

// IsAdmin is utils.is_admin (utils.py:201) on Windows:
// `ctypes.windll.shell32.IsUserAnAdmin() != 0`.
func IsAdmin() (bool, error) {
	if procIsUserAnAdminErr != nil {
		return false, procIsUserAnAdminErr
	}

	// IsUserAnAdmin takes no arguments and sets no last error.
	result, _, _ := procIsUserAnAdmin.Call()

	return result != 0, nil
}
