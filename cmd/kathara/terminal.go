// This file is `cli/ui/utils.open_machine_terminal` — the half of
// `HandleMachineTerminal` that spawns a window — under the PORT_SPEC §3.3
// rebuild.
//
// §3.3 replaces the four external-emulator adapters with three modes in
// priority order: a built-in multiplexer (the default), tmux driven through its
// documented CLI, and the ported external adapters as opt-in. Only the tmux
// backend exists today, and it is the one Python spelled with a vendored
// `libtmux` that "has shipped broken".
//
// # What Phase 6 has to fill
//
// Selecting a terminal this build cannot drive is an error naming the mode, not
// a silent fallback (PORT_SPEC §0.4). The two that error are:
//
//  1. **The built-in multiplexer** (`terminal: MULTIPLEXER`, and the eventual
//     default of §3.3 item 1): one window, one pane per device, bubbletea over
//     `term.Pty` — `creack/pty` on Unix, ConPTY on Windows. It needs device
//     switching, scrollback, resize, copy and a detach that leaves the
//     containers running. `term` currently carries only the local-PTY seam that
//     the Phase 2 spike left (docs/port/SPIKES/windows-terminal.md).
//  2. **The external emulators** (`terminal:` set to anything else —
//     `/usr/bin/xterm`, `gnome-terminal`, `Terminal`, `iTerm`): §3.3 item 3
//     marks them faithful-port territory, i.e. the three closures of
//     `cli/ui/utils.py:137-204`, including the `gnome-terminal --` special
//     case, the PowerShell `CREATE_NEW_CONSOLE` path and the two AppleScript
//     drivers. They belong in `term/external_{linux,darwin,windows}.go`
//     (PACKAGE_GRAPH.md row `cli/ui/utils.py`).
//
// Until then, `--noterminals` — and `open_terminals: false`, its settings twin
// — are complete paths, and `terminal: TMUX` works.

package main

import (
	"context"
	"os"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/term/tmuxdrv"
)

// terminalTmux is the special `terminal` value that selects the tmux backend
// rather than naming a program (`setting/Setting.py:288`).
const terminalTmux = "TMUX"

// openTerminal is `open_machine_terminal(machine)`.
//
// The command it runs is Python's, verbatim:
// `"%s connect %s -l %s" % (executable, is_vmachine, machine.name)` where
// `is_vmachine` is `-v` for a scenario with no host path and empty otherwise
// (`cli/ui/utils.py:132-133`). The `-l` there is `--logs`, not `--list`.
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

	vmachine := ""
	if !machine.Lab.HasHostPath() {
		vmachine = "-v"
	}
	connectCommand := strings.Join(nonEmpty(executable, "connect", vmachine, "-l", machine.Name), " ")

	cwd := ""
	if path, ok := machine.Lab.FSPath(); ok {
		cwd = path
	}
	a.console.Debug("Terminal will open in directory %s.", cwd)

	if terminal != terminalTmux {
		return unsupportedTerminal(terminal)
	}

	driver := &tmuxdrv.Driver{}
	if err := driver.Available(ctx); err != nil {
		return err
	}
	session := tmuxdrv.SessionName(machine.Lab.Name(), machine.Lab.Hash)
	_, err = driver.EnsureSession(ctx, session, tmuxdrv.Window{
		Name:    machine.Name,
		Command: connectCommand,
		Dir:     cwd,
	})
	if err != nil {
		return err
	}
	_, err = driver.EnsureWindow(ctx, session, tmuxdrv.Window{
		Name:    machine.Name,
		Command: connectCommand,
		Dir:     cwd,
	})
	return err
}

// unsupportedTerminal names the deferred mode, so that a user who set
// `terminal: /usr/bin/xterm` is told which of the two Phase 6 items they are
// waiting for rather than being told "not supported".
//
// The code is `NotSupported` and not `FeatureNotAvailable`: ERROR_CODES.md §5
// closes the `feature` token set at `lab.ext`, `linfo`, `stats-sampling` and
// `webhooks`, and §9.1 freezes it, so a terminal mode cannot mint a fifth.
func unsupportedTerminal(terminal string) error {
	if terminal == "" {
		return kerrors.New(kerrors.ErrNotSupported,
			"The built-in terminal multiplexer is not supported in this release. "+
				"Set `terminal` to TMUX, or use --noterminals.")
	}
	return kerrors.New(kerrors.ErrNotSupported,
		"External terminal emulator `"+terminal+"` is not supported in this release. "+
			"Set `terminal` to TMUX, or use --noterminals.")
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
