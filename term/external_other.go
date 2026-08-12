//go:build !linux && !darwin && !windows

package term

import "context"

// OpenExternal on a platform Python's `exec_by_platform` does not name.
//
// Python falls off the end of the dispatch and returns None — the terminal
// silently never opens. `settings.terminalAvailable` already refuses every
// emulator value on such a platform for the same reason, so this arm is
// reachable only if that check is bypassed; it reports rather than pretends.
func OpenExternal(context.Context, Request) error {
	return ErrExternalUnsupported
}
