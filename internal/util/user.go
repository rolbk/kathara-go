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

// ErrNoPasswdDatabase is what the Windows arms of `get_current_user_info` and
// `get_current_user_uid_gid` return. Python spells them `lambda: None` and
// `lambda: (None, None)` (utils.py:265, :228) and every caller of either is
// Unix-only, so the None was never observed; an error keeps it that way
// instead of handing back a plausible-looking uid 0 (NILABILITY.tsv).
var ErrNoPasswdDatabase = errors.New("util: no passwd database on this platform")

// GetCurrentUserName is utils.get_current_user_name (utils.py:235), the string
// that goes in the `user` label of every container, in every Docker network
// name and in every Kubernetes namespace. PORT_SPEC §0.4 forbids changing it.
//
// It is `slug(login + "-" + generate_urlsafe_hash(hostname))`, and the order
// matters: Python hashes the hostname on line 236, *before* it looks the login
// name up, so a passwd lookup that fails cannot be reordered ahead of it.
//
// The hostname half is what makes the name host-specific — two machines with
// the same login get different container names — and it is hashed, not
// slugged, so a hostname of "MY-PC" and one of "my-pc" produce different
// names. The login half is not hashed at all, which is why a login containing
// a dot or an accent reaches [Slug] and comes out folded.
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
//
// The empty string is not a degenerate case to guard against: it hashes to
// 1B2M2Y8AsgTpgAmY7PhCfg, the MD5 of nothing, and Kathará would happily name
// containers with it. Both implementations agree on that, which is the point.
func nodeName() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
