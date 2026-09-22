//go:build darwin

package term

import (
	"context"
	"os/exec"
	"strings"
)

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
