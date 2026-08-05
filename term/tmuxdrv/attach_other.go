//go:build !unix

package tmuxdrv

import (
	"os"
	"os/exec"
)

// execAttach runs tmux as a child with the caller's standard streams and waits
// for it. Platforms without execve (Windows) cannot replace the process image;
// a tmux binary is only reachable there through WSL or Cygwin anyway, so this
// path is a courtesy, not the supported terminal backend.
func (d *Driver) execAttach(argv, env []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		code := -1
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return &CommandError{Args: argv, ExitCode: code, Err: err}
	}
	return nil
}
