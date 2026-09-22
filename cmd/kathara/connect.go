package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/term"
	xterm "golang.org/x/term"
)

type connectFlags struct {
	directory string
	vmachine  bool
	shell     string
	logs      bool
}

func newConnectCmd(a *app) *commandSpec {
	cmd := newParser("connect")
	f := &connectFlags{}
	flags := cmd.Flags()
	flags.StringVarP(&f.directory, "directory", "d", "",
		"Specify the folder containing the network scenario.")
	cmd.meta("directory", "DIRECTORY")
	flags.BoolVarP(&f.vmachine, "vmachine", "v", false,
		"The device has been started with vstart command.")
	cmd.exclusiveGroup("directory", "vmachine")
	// M-4: the manual documents `--command <SHELL>`; the code is `--shell`.
	flags.StringVar(&f.shell, "shell", "", "Shell that should be used inside the device.")
	cmd.meta("shell", "SHELL")
	flags.BoolVarP(&f.logs, "logs", "l", false,
		"Print device startup logs before launching the shell.")
	cmd.pos("DEVICE_NAME", nargsOne, "Name of the device to connect to.")

	return &commandSpec{
		Name: "connect",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			return runConnect(ctx, a, f, positional)
		},
	}
}

// runConnect is `ConnectCommand.run`.
func runConnect(ctx context.Context, a *app, f *connectFlags, positional []string) (int, error) {
	machineName := positional[0]

	ref, err := a.execLabRef(f.directory, f.vmachine, "", "")
	if err != nil {
		return 1, err
	}
	// The debug line is Python's `lab.hash`, and for `-v` that is the hash of
	// `Lab("kathara_vlab")` — the reference the manager gets is by name, but
	// the log record is not.
	hash := ref.Hash
	if hash == "" && f.vmachine {
		hash = a.newVlab().Hash
	}
	a.console.Debug("Executing `connect` command with hash `%s`...", hash)

	mgr, err := a.manager(ctx)
	if err != nil {
		return 1, err
	}

	opts := kathara.DefaultConnectTTYOptions()
	opts.Shell = f.shell
	opts.Logs = f.logs
	opts.LogWriter = a.console.Out

	if term.ModeFor(a.settings.Terminal) == term.ModeMultiplexer && a.interactive() {
		return runConnectMux(ctx, mgr, machineName, ref, opts)
	}

	session, err := mgr.ConnectTTY(ctx, machineName, ref, opts)
	if err != nil {
		return 1, err
	}
	if session == nil {
		// `startup_waited == 2`: the device disappeared while its startup
		// commands were being probed, and Python returns None without raising
		// (`DockerMachine.py:698-699`). There is nothing to attach to, and
		// nothing to report either.
		return 0, nil
	}
	defer func() { _ = session.Close() }()

	if err := attachTTY(ctx, session, a); err != nil {
		return 1, err
	}
	return 0, nil
}

// runConnectMux attaches a single device inside the built-in multiplexer.
func runConnectMux(
	ctx context.Context,
	mgr kathara.Manager,
	machineName string,
	ref kathara.LabRef,
	opts kathara.ConnectTTYOptions,
) (int, error) {
	var log bytes.Buffer
	opts.LogWriter = &log

	session, err := mgr.ConnectTTY(ctx, machineName, ref, opts)
	if err != nil {
		return 1, err
	}
	if session == nil {
		// `startup_waited == 2`: the device disappeared while its startup
		// commands were being probed, as in the raw path above.
		return 0, nil
	}
	// The multiplexer closes it too; Close is idempotent, and this is what
	// covers a program that fails before the pane ever takes ownership.
	defer func() { _ = session.Close() }()

	opened := term.PrefixSession(session, crlf(log.Bytes()))
	cfg := term.Config{
		Title: "kathara: " + machineName,
		Devices: []term.Device{{
			Name: machineName,
			Open: func(context.Context) (term.Session, error) { return opened, nil },
		}},
	}
	if err := term.Run(ctx, cfg); err != nil {
		return 1, err
	}
	return 0, nil
}

// interactive is the question every bubbletea path asks first, through the
// [app.isTTY] hook a test can replace.
func (a *app) interactive() bool {
	if a.isTTY == nil {
		return a.isInteractiveTTY()
	}
	return a.isTTY()
}

// isInteractiveTTY reports whether both ends of the console are real
// terminals, which is what the multiplexer needs and what a piped or
// redirected invocation is not. It is the production value of [app.isTTY].
func (a *app) isInteractiveTTY() bool {
	stdin, ok := a.stdin.(*os.File)
	if !ok || !xterm.IsTerminal(int(stdin.Fd())) {
		return false
	}
	stdout, ok := a.console.Out.(*os.File)
	return ok && xterm.IsTerminal(int(stdout.Fd()))
}

// errStdinNotATerminal is `UnixConsoleAdapter.enter_raw` failing on a stdin
// that is not a terminal, carried into this implementation's taxonomy.
var errStdinNotATerminal = fmt.Errorf("stdin is not a terminal: %w", syscall.ENOTTY)

// attachTTY runs the terminal loop: put the local console in raw mode, pump
// bytes both ways, and forward window-size changes.
func attachTTY(ctx context.Context, session kathara.TTYSession, a *app) error {
	stdin, stdinIsFile := a.stdin.(*os.File)
	stdout, stdoutIsFile := a.console.Out.(*os.File)

	// Python's first act, and its first chance to fail: see
	// [errStdinNotATerminal]. It is deliberately *before* everything else, so
	// that a redirected `kathara connect` returns instead of parking on a
	// session neither side will ever end. Only stdin decides — Python's
	// `enter_raw` touches nothing else, and a terminal stdin with a redirected
	// stdout still attaches, minus the resize forwarding below.
	if !stdinIsFile || !xterm.IsTerminal(int(stdin.Fd())) {
		return errStdinNotATerminal
	}
	state, err := xterm.MakeRaw(int(stdin.Fd()))
	if err != nil {
		return err
	}
	defer func() { _ = xterm.Restore(int(stdin.Fd()), state) }()

	if stdoutIsFile && xterm.IsTerminal(int(stdout.Fd())) {
		if cols, rows, err := xterm.GetSize(int(stdout.Fd())); err == nil {
			_ = session.Resize(uint16(cols), uint16(rows))
		}
		stopResize := watchResize(stdout, session)
		defer stopResize()
	}

	// One goroutine per direction, which is the whole of what Python's
	// `TerminalRunner` needed a thread pump and an fd-readiness loop for
	// (`kathara.TTYSession`'s doc comment).
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(a.console.Out, readerFunc(session.Read))
		done <- err
	}()
	go func() {
		_, _ = io.Copy(writerFunc(session.Write), a.stdin)
	}()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	case <-ctx.Done():

		return nil
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
