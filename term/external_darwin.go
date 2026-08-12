//go:build darwin

package term

import (
	"context"
	"os/exec"
	"strings"
)

// OpenExternal is `osx_connect`'s non-TMUX branch, with `osascript` standing
// in for `appscript` (PORT_SPEC §6).
//
// appscript's calls are synchronous Apple Events, so this waits for osascript
// rather than detaching: an application that refused to open a window reports
// it here, where Python's `appscript` would have raised.
//
// A `terminal` value that is neither "Terminal" nor "iTerm" is a silent no-op,
// which is what Python's if/elif chain does with no else. It is reachable —
// `check_terminal` on macOS only asks LaunchServices whether the *name*
// resolves — and it is recorded in DIVERGENCES.md rather than repaired.
func OpenExternal(ctx context.Context, r Request) error {
	osxCommand := DarwinCommand(ConnectCommand(r), r.LabPath)
	spec, ok := DarwinSpec(r.Terminal, osxCommand)
	if !ok {
		return nil
	}

	cmd := exec.CommandContext(ctx, spec.Path, spec.Args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		// osascript's own diagnostic is far more useful than "exit status 1";
		// it names the application and the AppleScript error.
		return &externalError{Spec: spec, Output: msg, Err: err}
	}
	return nil
}

// externalError carries osascript's diagnostic alongside the exit status.
type externalError struct {
	Spec   Spec
	Output string
	Err    error
}

func (e *externalError) Error() string {
	return e.Spec.Path + ": " + e.Output
}

func (e *externalError) Unwrap() error { return e.Err }
