// The three differ in *when* the window appears, and that is the only place
// this file is more than a switch:
//   - tmux and external open a window per device, during the deploy, from the
//     `machine_deployed` event — Python's timing, preserved.
//   - the multiplexer is one window for the whole scenario, so the event can
//     only enqueue the device; the window opens once, after the command has
//     finished and emitted its result ([app.runPendingTerminals], called from
//     `runCommand`).
// `--noterminals` and `open_terminals: false` short-circuit ahead of all three
// in [app.openMachineTerminals], so the golden suite — which passes
// `--noterminals` everywhere — never reaches this file at all.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/term"
	"github.com/KatharaFramework/kathara-go/term/tmuxdrv"
)

// openTerminal is `open_machine_terminal(machine)`.
func (a *app) openTerminal(ctx context.Context, machine *model.Machine) error {
	if err := a.settings.CheckTerminal(""); err != nil {
		return err
	}
	terminal := a.settings.Terminal

	a.console.Debug("Opening terminal for device %s.", machine.Name)

	executable, err := util.GetExecutablePath(os.Args[0])
	if err != nil {
		return kerrors.ErrKatharaNotFound
	}

	cwd := ""
	if path, ok := machine.Lab.FSPath(); ok {
		cwd = path
	}
	a.console.Debug("Terminal will open in directory %s.", cwd)

	switch term.ModeFor(terminal) {
	case term.ModeMultiplexer:
		a.enqueueMuxDevice(machine)
		return nil
	case term.ModeTmux:
		return a.openTmuxWindow(ctx, machine, executable, cwd)
	default:
		req := term.Request{
			Terminal:   terminal,
			Executable: executable,
			Machine:    machine.Name,
			VMachine:   !machine.Lab.HasHostPath(),
			LabPath:    cwd,
		}
		if err := term.OpenExternal(ctx, req); err != nil {
			if errors.Is(err, term.ErrExternalUnsupported) {
				return kerrors.New(kerrors.ErrNotSupported,
					"External terminal emulator `"+terminal+"` is not supported on this platform. "+
						"Set `terminal` to "+term.TerminalMultiplexer+", or use --noterminals.")
			}
			return err
		}
		return nil
	}
}

func (a *app) openTmuxWindow(ctx context.Context, machine *model.Machine, executable, cwd string) error {
	vmachine := ""
	if !machine.Lab.HasHostPath() {
		vmachine = "-v"
	}
	connectCommand := strings.Join(nonEmpty(executable, "connect", vmachine, "-l", machine.Name), " ")

	driver := &tmuxdrv.Driver{}
	if err := driver.Available(ctx); err != nil {
		return err
	}
	session := tmuxdrv.SessionName(machine.Lab.Name(), machine.Lab.Hash)
	window := tmuxdrv.Window{
		Name:    machine.Name,
		Command: connectCommand,
		Dir:     cwd,
	}
	if _, err := driver.EnsureSession(ctx, session, window); err != nil {
		return err
	}
	_, err := driver.EnsureWindow(ctx, session, window)
	return err
}

// enqueueMuxDevice records a device for the one multiplexer window this
// command will open at the end.
func (a *app) enqueueMuxDevice(machine *model.Machine) {
	for _, m := range a.muxDevices {
		if m.Name == machine.Name {
			return
		}
	}
	a.muxDevices = append(a.muxDevices, machine)
}

// runPendingTerminals opens the built-in multiplexer over the devices the
// deploy enqueued, and is a no-op for every other mode and for every command
// that opened no terminals.
func (a *app) runPendingTerminals(ctx context.Context) error {
	devices := a.muxDevices
	a.muxDevices = nil
	if len(devices) == 0 {
		return nil
	}
	if !a.interactive() {
		a.console.Debug("Not opening the terminal multiplexer for %d device(s): "+
			"stdin and stdout are not both terminals.", len(devices))
		return nil
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return err
	}

	cfg := term.Config{
		Title:   "kathara: " + devices[0].Lab.Name(),
		Devices: make([]term.Device, 0, len(devices)),
	}
	for _, machine := range devices {
		cfg.Devices = append(cfg.Devices, term.Device{
			Name: machine.Name,
			Open: muxOpener(mgr, machine, a.settings.PrintStartupLog),
		})
	}
	return term.Run(ctx, cfg)
}

func muxOpener(mgr kathara.Manager, machine *model.Machine, logs bool) func(context.Context) (term.Session, error) {
	return func(ctx context.Context) (term.Session, error) {
		var log bytes.Buffer
		opts := kathara.DefaultConnectTTYOptions()
		opts.Logs = logs
		opts.LogWriter = &log
		session, err := mgr.ConnectTTYObj(ctx, machine, opts)
		if err != nil {
			return nil, err
		}
		if session == nil {
			// `startup_waited == 2`: the device disappeared while its startup
			// commands were being probed (`DockerMachine.py:698-699`). Python
			// returns None and the caller has nothing to attach to.
			return nil, kerrors.NewMachineNotRunning(machine.Name)
		}
		return term.PrefixSession(session, crlf(log.Bytes())), nil
	}
}

// crlf turns the CLI's line endings into a terminal's. The startup log is
// written with bare newlines, which a raw screen renders as a staircase
// because nothing moved the cursor back to column zero.
func crlf(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return bytes.ReplaceAll(bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")), []byte("\n"), []byte("\r\n"))
}

// nonEmpty drops the empty `is_vmachine` slot so that the command string does
// not grow a double space. Python's `%s` interpolation leaves one there, which
// `shlex.split` then discards on the `gnome-terminal` path and the shell
// discards on the others; a Go argv has no such forgiveness.
func nonEmpty(parts ...string) []string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
