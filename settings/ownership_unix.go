//go:build linux || darwin

package settings

import (
	"os"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// applyOwnership is the `unix_permissions` closure of `Setting.save_to_disk`
// (Setting.py:137): mode 0600, owned by the invoking user.
//
// The identity is the sudo-aware one, which is the whole point: `sudo kathara`
// writes `~/.config/kathara.conf` under the invoking user's home (the Linux
// home lookup follows SUDO_UID too) and must not leave it owned by root, or
// the next unprivileged run cannot rewrite it.
//
// The build tag is `linux || darwin` and not `unix` on purpose. Python dispatches
// through `exec_by_platform`, which names exactly three platforms and returns
// None for anything else — so on, say, FreeBSD neither the chmod nor the chown
// happens, and a Go build for it must not start doing them.
//
// Order is Python's: identity first, then chmod, then chown. It matters when
// the identity lookup fails, because the file is already on disk at that point
// and Python leaves it there with the umask's mode.
func applyOwnership(path string) error {
	uid, gid, err := util.GetCurrentUserUIDGID()
	if err != nil {
		return err
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return kerrors.WrapOS(err, err.Error())
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return kerrors.WrapOS(err, err.Error())
	}
	return nil
}
