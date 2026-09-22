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
func OpenExternal(_ context.Context, r Request) error {
	spec := WindowsSpec(ConnectCommand(r), r.LabPath)
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
	return cmd.Start()
}
