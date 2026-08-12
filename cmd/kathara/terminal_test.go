package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
	"github.com/KatharaFramework/kathara-go/term"
)

// newTestMachine builds a device in a scenario with a real directory, which is
// what `machine.lab.has_host_path()` answers on and therefore what decides the
// `-v` in the spawned connect command.
func newTestMachine(t *testing.T, a *testApp, name string) *model.Machine {
	t.Helper()
	lab := model.NewLab("demo", a.defaults())
	machine, err := lab.GetOrNewMachine(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

// TestTerminalTokensMatchTerm keeps the two spellings of the reserved
// `terminal` values in step. `settings` cannot import `term` (PACKAGE_GRAPH.md
// §5 holds it to the standard library), so this package — the one that imports
// both — is where the constants are pinned equal.
func TestTerminalTokensMatchTerm(t *testing.T) {
	if settings.TerminalTMUX != term.TerminalTMUX {
		t.Errorf("TMUX token drift: settings %q, term %q", settings.TerminalTMUX, term.TerminalTMUX)
	}
	if settings.TerminalMultiplexer != term.TerminalMultiplexer {
		t.Errorf("MULTIPLEXER token drift: settings %q, term %q",
			settings.TerminalMultiplexer, term.TerminalMultiplexer)
	}
	// Both reserved values must pass the settings check, or the default
	// configuration would fail its own startup validation.
	s := settings.Defaults()
	for _, v := range []string{settings.TerminalTMUX, settings.TerminalMultiplexer} {
		if err := s.CheckTerminal(v); err != nil {
			t.Errorf("CheckTerminal(%q) = %v, want nil", v, err)
		}
	}
}

// TestOpenTerminalDispatchesOnTheSetting is the §3.3 mode selection: one key,
// three integrations.
func TestOpenTerminalDispatchesOnTheSetting(t *testing.T) {
	t.Run("MULTIPLEXER enqueues instead of spawning", func(t *testing.T) {
		a := newTestApp(t)
		a.settings.Terminal = term.TerminalMultiplexer
		machine := newTestMachine(t, a, "pc1")

		if err := a.openTerminal(t.Context(), machine); err != nil {
			t.Fatalf("openTerminal: %v", err)
		}
		if len(a.muxDevices) != 1 || a.muxDevices[0] != machine {
			t.Fatalf("device not enqueued for the multiplexer: %v", a.muxDevices)
		}
	})

	t.Run("an emulator path takes the external adapter", func(t *testing.T) {
		a := newTestApp(t)
		// A terminal that exists and is executable, so `CheckTerminal` passes
		// and the adapter is actually reached; /bin/true then exits at once.
		a.settings.Terminal = "/bin/true"
		machine := newTestMachine(t, a, "pc1")

		if err := a.openTerminal(t.Context(), machine); err != nil {
			t.Fatalf("openTerminal: %v", err)
		}
		if len(a.muxDevices) != 0 {
			t.Error("the external path enqueued a multiplexer device")
		}
	})

	t.Run("a missing emulator gives Python's settings error", func(t *testing.T) {
		a := newTestApp(t)
		a.settings.Terminal = "/nonexistent/xterm"
		machine := newTestMachine(t, a, "pc1")

		err := a.openTerminal(t.Context(), machine)
		if err == nil {
			t.Fatal("a missing emulator was accepted")
		}
		// `check_terminal` runs first in `open_machine_terminal`, so this is
		// the error Python raises, not an exec failure.
		want := "Terminal Emulator `/nonexistent/xterm` not valid! Install it before using it."
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
		if kerrors.Code(err) != kerrors.CodeSettings {
			t.Errorf("code = %q, want %q", kerrors.Code(err), kerrors.CodeSettings)
		}
	})
}

// TestMuxDevicesAreDeduplicated is `num_terms` under the multiplexer: the event
// fires `get_num_terms()` times, and the multiplexer shows one tab per device
// — the same collapse tmux has always had, where the second `EnsureWindow`
// finds the window the first one made (SPIKES/tmux.md §6).
func TestMuxDevicesAreDeduplicated(t *testing.T) {
	a := newTestApp(t)
	a.settings.Terminal = term.TerminalMultiplexer
	a.settings.OpenTerminals = true
	// The real opener, not newTestApp's stub: the dispatch under test is the
	// one inside it.
	a.terminalOpener = a.openTerminal
	a.registerEvents()

	lab := model.NewLab("demo", a.defaults())
	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatal(err)
	}
	machine.Meta.NumTerms = model.Int(3)

	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Machine: machine}); err != nil {
		t.Fatal(err)
	}
	if len(a.muxDevices) != 1 {
		t.Fatalf("num_terms=3 enqueued %d tabs, want 1", len(a.muxDevices))
	}

	// A second device is a second tab, in deploy order.
	other, err := lab.GetOrNewMachine("pc2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Machine: other}); err != nil {
		t.Fatal(err)
	}
	if len(a.muxDevices) != 2 || a.muxDevices[0].Name != "pc1" || a.muxDevices[1].Name != "pc2" {
		t.Fatalf("tab order = %v, want [pc1 pc2]", names(a.muxDevices))
	}
}

// TestNoTerminalsLeavesNothingPending: `--noterminals` is checked before the
// mode is, so the multiplexer never even collects a device. This is the path
// the whole golden suite runs on.
func TestNoTerminalsLeavesNothingPending(t *testing.T) {
	a := newTestApp(t)
	a.settings.Terminal = term.TerminalMultiplexer
	a.settings.OpenTerminals = false
	a.terminalOpener = a.openTerminal
	a.registerEvents()

	machine := newTestMachine(t, a, "pc1")
	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Machine: machine}); err != nil {
		t.Fatal(err)
	}
	if len(a.muxDevices) != 0 {
		t.Fatalf("--noterminals enqueued %d devices", len(a.muxDevices))
	}
	// And with nothing pending, the post-command hook is a no-op that never
	// touches the manager — which in this test would fail if it did.
	if err := a.runPendingTerminals(t.Context()); err != nil {
		t.Errorf("runPendingTerminals with nothing pending = %v", err)
	}
}

// TestRunPendingTerminalsNeedsAManager: a pending device does reach the
// backend, which is what makes a pane's transport the same one `kathara
// connect` uses.
func TestRunPendingTerminalsNeedsAManager(t *testing.T) {
	a := newTestApp(t)
	a.isTTY = func() bool { return true }
	a.muxDevices = []*model.Machine{newTestMachine(t, a, "pc1")}

	err := a.runPendingTerminals(t.Context())
	if !errors.Is(err, errUnexpectedManager) {
		t.Errorf("runPendingTerminals = %v, want the manager error", err)
	}
	// The queue is drained even on failure, so a later command in the same
	// process does not inherit it.
	if len(a.muxDevices) != 0 {
		t.Error("the pending queue survived a failed run")
	}
}

// TestRunPendingTerminalsSkipsANonTerminal is DIVERGENCES.md item 117 on the
// deploy path: `kathara lstart | tee log` under MULTIPLEXER — the stock Windows
// configuration — must not put alternate-screen frames in the pipe or read the
// user's redirected stdin in raw mode. Python spawned OS windows here and
// touched neither stream.
func TestRunPendingTerminalsSkipsANonTerminal(t *testing.T) {
	a := newTestApp(t)
	a.settings.Terminal = term.TerminalMultiplexer
	a.muxDevices = []*model.Machine{newTestMachine(t, a, "pc1")}

	// newTestApp's manager factory fails, so reaching the backend at all would
	// surface as an error here.
	if err := a.runPendingTerminals(t.Context()); err != nil {
		t.Fatalf("runPendingTerminals without a terminal = %v, want a silent skip", err)
	}
	if len(a.muxDevices) != 0 {
		t.Error("the pending queue survived the skip")
	}
	if got := a.stdoutString(); got != "" {
		t.Errorf("the skipped multiplexer wrote to stdout: %q", got)
	}
}

// TestCRLFConversion pins the startup-log fix: a pane is a raw screen, and a
// bare newline there is a staircase, not a new line.
func TestCRLFConversion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"one\ntwo", "one\r\ntwo"},
		{"already\r\nthere", "already\r\nthere"},
		{"trailing\n", "trailing\r\n"},
	} {
		if got := string(crlf([]byte(tc.in))); got != tc.want {
			t.Errorf("crlf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func names(machines []*model.Machine) []string {
	out := make([]string, 0, len(machines))
	for _, m := range machines {
		out = append(out, m.Name)
	}
	return out
}
