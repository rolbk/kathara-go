// This file completes the identity chain hash.go starts: the current user's
// login name, the hostname it is paired with, and the home directory that
// becomes `/hosthome` inside every device.

package util

import (
	"errors"
	"os"
)

// UserInfo is the part of `pwd.struct_passwd` that utils.py reads
// (utils.py:239, :215, :226).
type UserInfo struct {
	// Name is pw_name, the login name.
	Name string
	// UID is pw_uid.
	UID int
	// GID is pw_gid.
	GID int
	// HomeDir is pw_dir.
	HomeDir string
}

var ErrNoPasswdDatabase = errors.New("util: no passwd database on this platform")

func GetCurrentUserName() (string, error) {
	hostname := GenerateURLSafeHash(nodeName())

	login, err := currentUserLogin()
	if err != nil {
		return "", err
	}

	return Slug(login + "-" + hostname), nil
}

// nodeName is `platform.node()` (utils.py:236), which CPython resolves to
// `socket.gethostname()` and which returns the empty string rather than
// raising when the lookup fails (platform._node swallows OSError).
func nodeName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
