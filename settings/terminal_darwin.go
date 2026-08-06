//go:build darwin

package settings

import (
	"errors"
	"os"
	"os/exec"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// terminalAvailable is the `check_osx` arm of `Setting.check_terminal`
// (Setting.py:283): `appscript.app(terminal)`, answering False when it raises
// `aem.findapp.ApplicationNotFoundError`.
//
// On macOS the setting is an application *name* — the default is "Terminal"
// and the menu offers "Terminal" and "iTerm" — because the launcher drives the
// app over Apple Events (`cli/ui/utils.py:194-201`), not by executing a file.
// So the check is "does LaunchServices know an app by this name", and
// `os.path.isfile` would answer no for every legal value.
//
// py-appscript reaches LaunchServices through a Carbon binding. The
// cgo-free equivalent is `osascript`'s `path to application`, which performs
// the same LaunchServices lookup by name, bundle identifier or creator code
// and, like `appscript.app`, resolves without launching anything. A value that
// is already a path is checked on disk instead, which is what appscript's
// finder does with one.
//
// Not verified against a live macOS host: no macOS runner was available while
// this was written. It is the one behaviour in this package that a Layer A run
// on the mac runners has to confirm.
func terminalAvailable(terminal string) (bool, error) {
	if strings.ContainsRune(terminal, os.PathSeparator) {
		// An application bundle is a directory; a value naming a file is a
		// value appscript would not resolve either.
		info, err := os.Stat(terminal)
		if err != nil {
			return false, nil
		}
		return info.IsDir(), nil
	}

	script := `path to application "` + escapeAppleScriptString(terminal) + `"`
	cmd := exec.Command("/usr/bin/osascript", "-e", script)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// osascript ran and could not resolve the name: that is
			// ApplicationNotFoundError.
			return false, nil
		}
		// osascript could not be started at all. Reporting that as "install
		// the terminal emulator" would send the user after the wrong problem.
		return false, kerrors.WrapOS(err, err.Error())
	}

	return true, nil
}

// escapeAppleScriptString quotes a value for an AppleScript string literal,
// where only the backslash and the double quote are special.
func escapeAppleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}
