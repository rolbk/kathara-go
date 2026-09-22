//go:build unix

package util

import (
	"os"
	"os/user"
	"strconv"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// GetCurrentUserInfo is utils.get_current_user_info (utils.py:249), the passwd
// lookup every other identity getter is built on.
func GetCurrentUserInfo() (UserInfo, error) {
	userID := os.Getuid()

	if userID == 0 {
		if realUserID := os.Getenv("SUDO_UID"); realUserID != "" {
			parsed, err := PyInt(realUserID)
			if err != nil {
				failure := PyIntFailure(err, realUserID)
				return UserInfo{}, kerrors.WrapValue(failure, failure.Error())
			}
			userID = parsed
		}
	}

	// `pwd.getpwuid` goes through NSS, so an LDAP or SSSD user resolves.
	// os/user matches that when the binary is built with cgo, and falls back
	// to parsing /etc/passwd when it is not; a CGO_ENABLED=0 build therefore
	// cannot see a directory-server user, and would name that user's
	// containers from a failed lookup instead. Kathará releases are cgo
	// builds for this reason.
	entry, err := user.LookupId(strconv.Itoa(userID))
	if err != nil {
		// pwd.getpwuid raises KeyError for a uid with no entry; it is
		// uncaught at every call site.
		return UserInfo{}, err
	}

	uid, err := strconv.Atoi(entry.Uid)
	if err != nil {
		return UserInfo{}, err
	}
	gid, err := strconv.Atoi(entry.Gid)
	if err != nil {
		return UserInfo{}, err
	}

	return UserInfo{Name: entry.Username, UID: uid, GID: gid, HomeDir: entry.HomeDir}, nil
}

// GetCurrentUserUIDGID is utils.get_current_user_uid_gid (utils.py:223): the
// pair `settings` chowns its config file to after a sudo-ed first run
// (Setting.py:141).
func GetCurrentUserUIDGID() (uid int, gid int, err error) {
	info, err := GetCurrentUserInfo()
	if err != nil {
		return 0, 0, err
	}
	return info.UID, info.GID, nil
}

// currentUserLogin is the Unix arm of the `exec_by_platform` inside
// get_current_user_name (utils.py:238): the sudo-aware pw_name.
func currentUserLogin() (string, error) {
	info, err := GetCurrentUserInfo()
	if err != nil {
		return "", err
	}
	return info.Name, nil
}
