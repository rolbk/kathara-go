//go:build windows

package term

import (
	"context"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// OpenExternal is `windows_connect` (`cli/ui/utils.py:160-169`): PowerShell in
// a brand-new console window, running the connect command through the call
// operator.
//
// `CREATE_NEW_CONSOLE` is the whole point — without it the child would share
// this process's console and its output would interleave with the deploy's
// progress bars. It is also why nothing here touches ConPTY: an external
// emulator gets a real OS console, and only the built-in multiplexer's panes
// need a pseudoconsole (docs/port/SPIKES/windows-terminal.md §1).
//
// The `terminal` setting is not read, exactly as Python does not read it:
// `check_terminal`'s Windows arm is `lambda: True` and `windows_connect` names
// PowerShell unconditionally.
func OpenExternal(_ context.Context, r Request) error {
	spec := WindowsSpec(ConnectCommand(r), r.LabPath)
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return cmd.Start()
}
