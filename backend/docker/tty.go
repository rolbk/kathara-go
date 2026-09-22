// What is NOT here is the rendering. Python's `DockerTTYTerminal` and
// `DockerNPipeTerminal` construct a console adapter and run a `TerminalRunner`
// loop from inside the backend; that moves to `term`, which imports this
// package and not the other way round. `connect_tty` therefore returns the
// session instead of blocking on it.

package docker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// startupLogCommand is the command `connect` runs to replay the startup log
// (`DockerMachine.py:703`). The globbed `/var/kathara/*` is why it goes through
// a shell rather than being exec'd directly.
const startupLogCommand = "cat /var/log/shared.log /var/log/startup.log /var/kathara/*"

// Connect is `DockerMachine.connect` (`DockerMachine.py:645`): resolve the
// device, optionally wait for its startup script, optionally replay the log,
// then open an interactive exec.
func (s *machineService) Connect(
	ctx context.Context,
	labHash, machineName, user string,
	opts kathara.ConnectTTYOptions,
) (kathara.TTYSession, error) {
	containers, err := s.getByFilters(ctx, labHash, machineName, user)
	if err != nil {
		return nil, err
	}
	if len(containers) == 0 {
		return nil, kerrors.NewMachineNotRunning(machineName)
	}

	c := containers[len(containers)-1]

	// `shlex.split(container.labels['shell'])` when no override was given, and
	// `shlex.split(shell)` when one was: both go through the splitter, so a
	// shell configured as `/bin/sh -l` is two words either way.
	shellSource := opts.Shell
	if shellSource == "" {
		shellSource = c.Label(labelShell)
	}
	shell, err := ShlexSplit(shellSource)
	if err != nil {
		return nil, err
	}
	// `"Connect to device `%s` with shell: %s" % (machine_name, shell)`
	// (`DockerMachine.py:677`). `shell` is the POST-`shlex.split` list, so it
	// interpolates as the list's repr and not as the configured string.
	slog.Debug("Connect to device `" + machineName + "` with shell: " + util.PythonStrListRepr(shell))

	startupWaited := startupWaitInterrupted
	if opts.Wait.Enabled {
		startupWaited, err = s.waitStartupExecution(ctx, c, opts.Wait.Retries, opts.Wait.Interval)
		if err != nil {
			return nil, err
		}

		if dispatchErr := event.Dispatch(s.manager.dispatcher, event.MachineStartupWaitEnded{}); dispatchErr != nil {
			return nil, dispatchErr
		}

		if startupWaited == startupWaitAPIError {
			return nil, nil
		}
	}

	if opts.Logs && s.manager.settings.PrintStartupLog {
		if err := s.writeStartupLog(ctx, c, shell, startupWaited, opts.LogWriter); err != nil {
			return nil, err
		}
	}

	created, err := s.manager.api.ContainerExecCreate(ctx, c.ID, container.ExecOptions{
		Cmd:          shell,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  true,
		Tty:          true,
		Privileged:   false,
	})
	if err != nil {
		return nil, err
	}

	attached, err := s.manager.api.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: true})
	if err != nil {
		return nil, err
	}

	return &ttySession{
		manager:  s.manager,
		response: attached,
		execID:   created.ID,
		closed:   make(chan struct{}),
	}, nil
}

// writeStartupLog is the `if logs and print_startup_log` block
// (`DockerMachine.py:701-724`).
func (s *machineService) writeStartupLog(ctx context.Context, c *Container, shell []string, startupWaited int, w io.Writer) error {
	if w == nil {
		// Python's `sys.stdout`, which [kathara.ConnectTTYOptions.LogWriter]
		// documents as the nil default.
		w = os.Stdout
	}

	// `startup_command = [item for item in shell]; startup_command.extend(['-c', cat_logs_cmd])`
	// — a copy, so the shell slice the exec below uses is untouched.
	startupCommand := append(append([]string(nil), shell...), "-c", startupLogCommand)

	result, err := s.execRun(ctx, c, execRunOptions{Cmd: startupCommand, Stdout: true})
	if err != nil {
		// The comment at `:702` says "if the command fails it means that the
		// shell is not found", but nothing catches anything: a
		// MachineBinaryError from here propagates out of `connect`.
		return err
	}
	if len(result.Stdout) == 0 {
		return nil
	}

	if _, err := io.WriteString(w, "--- Startup Commands Log\n"); err != nil {
		return err
	}
	if _, err := w.Write(result.Stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "--- End Startup Commands Log\n"); err != nil {
		return err
	}
	if startupWaited == startupWaitInterrupted {
		if _, err := io.WriteString(w, "!!! Executing other commands in background !!!\n"); err != nil {
			return err
		}
	}
	return nil
}

// ttySession is `ITerminalSession` over a hijacked Docker exec — the union of
// `DockerTTYTerminalSession` and `DockerNPipeSession`, which differed only in
// how they read and wrote the transport.
type ttySession struct {
	manager  *Manager
	response types.HijackedResponse
	execID   string

	closeOnce sync.Once
	closed    chan struct{}
}

// Read is `read(n)`. The tty exec is unmultiplexed, so the payload is the
// device's output verbatim.
func (t *ttySession) Read(p []byte) (int, error) {
	n, err := t.response.Reader.Read(p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}

// Write is `write(data)`: keystrokes into the exec's stdin.
func (t *ttySession) Write(p []byte) (int, error) {
	return t.response.Conn.Write(p)
}

// Resize is `resize(cols, rows)` → `exec_resize(id, height=rows, width=cols)`.
func (t *ttySession) Resize(cols, rows uint16) error {
	select {
	case <-t.closed:
		return nil
	default:
	}
	return t.manager.api.ContainerExecResize(context.Background(), t.execID, container.ResizeOptions{
		Height: uint(rows),
		Width:  uint(cols),
	})
}

// Close ends the session. Safe to call more than once, which is what Python's
// `_closed` flag bought and what `TerminalRunner`'s several cleanup arms need.
func (t *ttySession) Close() error {
	t.closeOnce.Do(func() {
		close(t.closed)
		t.response.Close()
	})
	return nil
}
