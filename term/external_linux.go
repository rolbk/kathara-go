//go:build linux

package term

import "context"

// OpenExternal is `unix_connect`'s non-TMUX branch: spawn the configured
// emulator with the connect command, in the scenario's directory, in a new
// session.
func OpenExternal(_ context.Context, r Request) error {
	spec, err := LinuxSpec(r.Terminal, ConnectCommand(r), r.LabPath)
	if err != nil {
		return err
	}
	return spawnDetached(spec)
}
