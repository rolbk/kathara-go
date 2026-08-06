//go:build linux

package settings

import (
	"os"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// terminalAvailable is the `check_unix` arm of `Setting.check_terminal`
// (Setting.py:280): `os.path.isfile(terminal) and os.access(terminal, os.X_OK)`.
//
// The setting is a path here, not a name, and it is not looked up on PATH —
// the default is the absolute `/usr/bin/xterm` and the settings screen offers
// absolute paths. A bare "xterm" is therefore invalid however installed the
// emulator is, which is Python's answer too.
//
// `os.access` is access(2), which answers for the **real** uid rather than the
// effective one; that is the same choice `internal/util` documents for the
// PATH search, and it matters on a setgid install. It comes from
// [util.AccessXOK] rather than from `golang.org/x/sys/unix` directly because
// PACKAGE_GRAPH.md §5 holds this package to the standard library and puts the
// x/sys grant in `internal/util`.
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
