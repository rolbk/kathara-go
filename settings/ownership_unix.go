//go:build linux || darwin

package settings

import (
	"os"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// applyOwnership is the `unix_permissions` closure of `Setting.save_to_disk`
// (Setting.py:137): mode 0600, owned by the invoking user.
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
