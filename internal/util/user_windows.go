package util

import "os"

// pythonMessage is an error whose text is copied verbatim from CPython. It is
// a distinct type rather than errors.New so the capitalised, unpunctuated
// original does not have to be reworded to satisfy ST1005 (`vfs` does the same
// for the messages it reproduces).
type pythonMessage string

func (e pythonMessage) Error() string { return string(e) }

// ErrNoUsername is the OSError getpass.getuser raises when no environment
// variable names the user and there is no passwd database to fall back on,
// which on Windows is always.
const ErrNoUsername = pythonMessage("No username set in the environment")

// GetCurrentUserInfo is the Windows arm of utils.get_current_user_info
// (utils.py:265), which Python writes as `lambda: None`. There is no passwd
// database to read.
func GetCurrentUserInfo() (UserInfo, error) {
	return UserInfo{}, ErrNoPasswdDatabase
}

// GetCurrentUserUIDGID is the Windows arm of utils.get_current_user_uid_gid
// (utils.py:228), Python's `lambda: (None, None)`. The only caller chowns a
// file (Setting.py:141) and is Unix-only.
func GetCurrentUserUIDGID() (uid int, gid int, err error) {
	return 0, 0, ErrNoPasswdDatabase
}

// currentUserLogin is the Windows arm of the `exec_by_platform` inside
// get_current_user_name (utils.py:242): `getpass.getuser()`.
func currentUserLogin() (string, error) {
	for _, name := range []string{"LOGNAME", "USER", "LNAME", "USERNAME"} {
		if value := os.Getenv(name); value != "" {
			return value, nil
		}
	}
	return "", ErrNoUsername
}
