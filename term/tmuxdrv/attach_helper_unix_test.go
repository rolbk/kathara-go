//go:build !windows

package tmuxdrv

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/creack/pty"
)

// startAttachHelper gives the re-executed test binary a real pseudo-terminal.
// Calling script(1) here would make the test depend on its incompatible BSD
// and util-linux command-line interfaces.
func startAttachHelper(t *testing.T, ctx context.Context, self string, env []string, spec string) *attachHelper {
	t.Helper()

	cmd := exec.CommandContext(ctx, self)
	configureHelperCommand(cmd, env, spec)
	master, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("cannot allocate a pty for the attach test: %v", err)
	}

	// A tmux client continuously writes terminal updates. Drain the master just
	// as script(1) did, so its output cannot fill the pty buffer and stall the
	// client under test.
	go func() { _, _ = io.Copy(io.Discard, master) }()

	h := &attachHelper{cmd: cmd, done: make(chan error, 1)}
	go func() {
		err := cmd.Wait()
		_ = master.Close()
		h.done <- err
	}()

	t.Cleanup(func() {
		if h.exited {
			return
		}
		_ = cmd.Process.Kill()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			t.Errorf("attach helper (%s) did not die after SIGKILL", spec)
		}
	})
	return h
}
