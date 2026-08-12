//go:build linux

package term

import "context"

// OpenExternal is `unix_connect`'s non-TMUX branch: spawn the configured
// emulator with the connect command, in the scenario's directory, in a new
// session.
//
// The `terminal` value has already been checked by
// `settings.Settings.CheckTerminal` at the call site, exactly as Python checks
// it at the top of `open_machine_terminal` — so a missing `/usr/bin/xterm`
// produces the settings error ("Terminal Emulator `/usr/bin/xterm` not valid!
// Install it before using it.") and never reaches this function.
func OpenExternal(_ context.Context, r Request) error {
	spec, err := LinuxSpec(r.Terminal, ConnectCommand(r), r.LabPath)
	if err != nil {
		return err
	}
	return spawnDetached(spec)
}
