// This file is the transport half of the terminal rebuild (PORT_SPEC §0.2 #2,
// PACKAGE_GRAPH.md D-5): `DockerMachine.connect`'s exec-and-hijack, plus the
// `ITerminalSession` the two `terminal/session/` classes implemented.
//
// What is NOT here is the rendering. Python's `DockerTTYTerminal` and
// `DockerNPipeTerminal` construct a console adapter and run a `TerminalRunner`
// loop from inside the backend; that moves to `term`, which imports this
// package and not the other way round. `connect_tty` therefore returns the
// session instead of blocking on it.
//
// One file, not the `tty_unix.go` / `tty_windows.go` pair PACKAGE_GRAPH.md §4
// lists. The split existed because Python needed `os.read` on a Unix fd and
// `win32file.ReadFile` on a Windows named pipe, reaching into docker-py
// privates (`handler._response`, `handler._handle.handle`) to get at either.
// The Go SDK hands back a `types.HijackedResponse` holding a `net.Conn` on both
// platforms — it uses go-winio for the npipe itself — so there is one
// implementation and no platform code to split. Recorded in
// PROPOSED-DIVERGENCES.md.

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
//
// # The `startup_waited == 2` early return
//
// When the startup probe hits an API error the wait answers 2 and `connect`
// RETURNS — no exec, no error (`:698-699`). `exec` raises MachineNotRunning for
// the same 2 (docker-backend.md gotcha 17). The asymmetry is undocumented and
// preserved: a nil session with a nil error is that return, and
// [kathara.Manager.ConnectTTY] spells out that a user keypress is NOT this case
// — that sets 0 or 1 and the shell still opens.
//
// # The log block
//
// It is gated twice: by the caller's `logs` and by `Setting.print_startup_log`
// (`:701`). The exec that produces it asks for stdout only, so a device whose
// log files do not exist prints nothing rather than the shell's complaint. The
// trailing "executing other commands in background" line appears only when
// `startup_waited` is 0 — i.e. the user broke out of the wait — which is why
// the flag is threaded this far.
//
// Python writes the block to `sys.stdout` from inside the backend; the port
// writes it to the caller's writer, because stream assignment belongs to the
// CLI (JSON_CLI_CONTRACT.md §1.3). Python also decodes the bytes with
// `chardet`; the bytes are written through unchanged here, which is the same
// result for UTF-8 and ASCII logs and avoids a charset-detection dependency for
// a debug dump. DIVERGENCES.md records it.
//
// Errors: [kerrors.ErrMachineNotRunning] when nothing matches,
// [kerrors.ErrValue] from the shell's shlex split, the daemon's own.
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
	// `containers.pop()` — the last match (ORDERING.tsv row 10).
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
//
// It is safe for the one concurrency pattern [kathara.TTYSession] requires: one
// goroutine in Read while another calls Write and Resize. The hijacked
// connection is a `net.Conn`, which is safe for one reader and one writer, and
// Resize is an independent HTTP request.
type ttySession struct {
	manager  *Manager
	response types.HijackedResponse
	execID   string

	closeOnce sync.Once
	closed    chan struct{}
}

// Read is `read(n)`. The tty exec is unmultiplexed, so the payload is the
// device's output verbatim.
//
// Python's two implementations both answer `b""` at end of session and their
// two pumps disagree about what that means (analysis/manager-foundation.md §7
// gotcha 14). The disagreement does not survive: end of session is io.EOF, as
// [kathara.TTYSession.Read] requires.
func (t *ttySession) Read(p []byte) (int, error) {
	n, err := t.response.Reader.Read(p)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}
	return n, err
}

// Write is `write(data)`: keystrokes into the exec's stdin.
//
// The Python sessions swallow writes after close (`if self._closed: return`)
// and report success; this returns the connection's own error instead, which a
// `term` write loop needs in order to stop.
func (t *ttySession) Write(p []byte) (int, error) {
	return t.response.Conn.Write(p)
}

// Resize is `resize(cols, rows)` → `exec_resize(id, height=rows, width=cols)`.
//
// Columns first, as [kathara.TTYSession.Resize] insists, and swapped into the
// SDK's height/width at the last moment — this is the one axis swap the port
// cannot afford to get wrong.
//
// A resize after close is dropped rather than sent: Python's `if self._closed:
// return` guards it, and without the guard a SIGWINCH racing the shell's exit
// would put a spurious API error on the terminal's error path.
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
