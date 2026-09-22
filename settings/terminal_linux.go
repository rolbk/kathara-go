//go:build linux

package settings

import (
	"os"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// terminalAvailable is the `check_unix` arm of `Setting.check_terminal`
// (Setting.py:280): `os.path.isfile(terminal) and os.access(terminal, os.X_OK)`.
func terminalAvailable(terminal string) (bool, error) {
	info, err := os.Stat(terminal)
	if err != nil {
		// `os.path.isfile` is False for every stat failure.
		return false, nil
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	return util.AccessXOK(terminal), nil
}
