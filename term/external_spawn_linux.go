package term

import (
	"os"
	"os/exec"
	"syscall"
)

// spawnDetached is `subprocess.Popen(command, cwd=…, start_new_session=True)`.
func spawnDetached(spec Spec) error {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	// Popen with no stdio arguments hands the child the parent's descriptors;
	// os/exec with nil ones hands it /dev/null. The difference is observable:
	// an emulator that refuses to start — the classic `gnome-terminal` "cannot
	// open display" — writes its diagnostic to stderr, and under 3.8.3 the user
	// saw it in the shell they deployed from.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
