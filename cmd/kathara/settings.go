package main

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// newSettingsCmd builds the command.
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
