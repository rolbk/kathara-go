//go:build unix

package tmuxdrv

import (
	"fmt"
	"os/exec"
	"syscall"
)

// execAttach replaces this process with tmux. It returns only on failure.
func (d *Driver) execAttach(argv, env []string) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("tmuxdrv: cannot find %s: %w", argv[0], err)
	}
	// syscall.Exec keeps the process's file descriptors, so tmux inherits the
	// caller's controlling terminal directly.
	if err := syscall.Exec(bin, argv, env); err != nil {
		return &CommandError{Args: argv, ExitCode: -1, Err: err}
	}
	return nil // unreachable: execve does not return on success
}
