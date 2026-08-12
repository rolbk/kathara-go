// This file is `cli/command/SettingsCommand.py`, rebuilt: the command word, the
// no-TTY gate, and the bubbletea program that replaces the 1,295-line vendored
// curses menu (PORT_SPEC §0.2 #1, §3.2 item 3). The form itself is
// settings_tui.go; PACKAGE_GRAPH.md D-4 keeps both out of `settings/` so that
// package stays TTY-free.

package main

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// newSettingsCmd builds the command.
//
// The Python command "defines no parser and never parses argv": `kathara
// settings -h` silently ignores the `-h` and opens the menu, and there is no
// argparse exit-2 path at all (CLI_SURFACE.md M-5, §12). That is reproduced by
// [commandSpec.NoParser], which skips the whole parse-and-validate block — the
// help flag is never consulted because argv is never read. It is also why
// `settings` registers no `--format`: JSON_CLI_CONTRACT.md §1.1 gives it
// `human` only.
func newSettingsCmd(a *app) *commandSpec {
	cmd := newParser("settings")
	return &commandSpec{
		Name:     "settings",
		Cmd:      cmd,
		NoParser: true,
		Run: func(_ context.Context, a *app, _, _ []string) (int, error) {
			return runSettings(a)
		},
	}
}

// runSettings opens the form.
//
// Without a terminal it refuses and names the scriptable alternative. The
// curses menu had no such branch — it drove `curses` at a pipe and died in the
// library — and §3.2 item 2 exists precisely so that there *is* an answer to
// give here.
func runSettings(a *app) (int, error) {
	if !a.console.TTY {
		return 1, kerrors.New(kerrors.ErrInvocation,
			"`kathara settings` needs a terminal. Use `kathara config get|set|list|reset` instead.")
	}

	// The form edits a copy and the copy replaces the live settings only once
	// a save has reached the disk, so quitting without saving leaves both the
	// file and the running process on the old values. Copying by value is
	// enough: every writer in `settings` replaces a pointer field rather than
	// writing through one, so the two structs share no mutable state.
	working := *a.settings
	model := newSettingsModel(&working, managerChoices(), func(s *settings.Settings) error {
		if err := s.Save(a.settingsDir); err != nil {
			return err
		}
		*a.settings = *s
		return nil
	})

	// PORT_SPEC §12.6, terminal state restoration: `Program.Run` restores the
	// terminal on every exit path it owns — a normal quit, an error, a panic
	// (it recovers, restores, and returns the panic as an
	// `ErrProgramKilled: ErrProgramPanic` error rather than re-raising it), and
	// SIGINT/SIGTERM, for which it installs its own handler. Nothing below this
	// line may return without going through it, which is why the program is run
	// inline rather than handed to a goroutine.
	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithInput(a.stdin),
		tea.WithOutput(a.console.Out),
	)
	final, err := program.Run()
	if err != nil {
		return 1, err
	}

	if m, ok := final.(*settingsModel); ok && m.err != nil {
		return 1, m.err
	}
	return 0, nil
}

// managerChoices is `Kathara.get_available_managers_name()` as the form's
// `manager_type` menu: the `AVAILABLE_MANAGERS` vocabulary, labelled with the
// registry's formatted names.
//
// The vocabulary comes from `settings` and not from the registry, because the
// two differ in a `nok8s` build (PORT_SPEC §0.2 #8) and the *setting* keeps its
// full vocabulary there — `manager_type: kubernetes` is still a legal file
// value that such a build then refuses to run. A backend this build does not
// carry is listed under its bare name.
func managerChoices() []settingsChoice {
	labels := map[string]string{}
	for _, info := range backendRegistry().Available() {
		labels[info.Name] = info.FormattedName
	}

	names := settings.AvailableManagers()
	out := make([]settingsChoice, 0, len(names))
	for _, name := range names {
		label := labels[name]
		if label == "" {
			label = name
		}
		out = append(out, settingsChoice{label: label, value: name})
	}
	return out
}
