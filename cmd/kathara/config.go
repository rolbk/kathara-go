// This file is PORT_SPEC §3.2 items 2 and 3: the scriptable `kathara config`
// and the `kathara settings` screen that replaces the vendored curses menu.
//
// Both go through `settings/`'s own validators, which is §3.2 item 4 — "so
// `config set` and the TUI cannot disagree". Neither of them re-implements a
// check.

package main

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// newConfigCmd is the non-interactive settings interface (PORT_SPEC §3.2 item
// 2). It has no Python original; JSON_CLI_CONTRACT.md §3.12 pins its four
// envelope shapes.
func newConfigCmd(a *app) *commandSpec {
	cmd := newParser("config")
	cmd.Short = "Read and write Kathara settings"
	cmd.Long = cmd.Short
	registerFormat(cmd, false)
	cmd.pos("get|set|list|reset", nargsZeroOrMore, "The settings operation to perform.")

	return &commandSpec{
		Name: "config",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			return runConfig2(a, positional)
		},
	}
}

func runConfig2(a *app, positional []string) (int, error) {
	if len(positional) == 0 {
		return 2, errUsage("the following arguments are required: get|set|list|reset")
	}
	switch positional[0] {
	case "get":
		if len(positional) != 2 {
			return 2, errUsage("config get takes exactly one KEY")
		}
		value, err := a.settings.Get(positional[1])
		if err != nil {
			return 1, err
		}
		a.console.Print(fmt.Sprintf("%s = %s", positional[1], formatSettingValue(value)))
		a.console.Emit(cliout.SettingsGetResult{Key: positional[1], Value: value})
		return 0, nil

	case "set":
		if len(positional) != 3 {
			return 2, errUsage("config set takes exactly one KEY and one VALUE")
		}
		key, raw := positional[1], positional[2]
		if err := a.settings.SetString(key, raw); err != nil {
			return 1, err
		}
		if err := a.settings.Save(""); err != nil {
			return 1, err
		}
		value, err := a.settings.Get(key)
		if err != nil {
			return 1, err
		}
		a.console.Print(fmt.Sprintf("%s = %s", key, formatSettingValue(value)))
		a.console.Emit(cliout.SettingsSetResult{Key: key, Value: value, Saved: true})
		return 0, nil

	case "list":
		keys, err := a.settings.Keys()
		if err != nil {
			return 1, err
		}
		for _, key := range keys {
			value, err := a.settings.Get(key)
			if err != nil {
				return 1, err
			}
			a.console.Print(fmt.Sprintf("%s = %s", key, formatSettingValue(value)))
		}
		a.console.Emit(cliout.SettingsListResult{Settings: a.settings})
		return 0, nil

	case "reset":
		fresh := settings.Defaults()
		if err := fresh.Save(""); err != nil {
			return 1, err
		}
		*a.settings = *fresh
		a.console.Print("Settings reset to their defaults.")
		a.console.Emit(cliout.SettingsResetResult{Settings: a.settings, Saved: true})
		return 0, nil
	}
	return 2, errUsage("unknown config subcommand `%s`", positional[0])
}

// formatSettingValue renders one schema value for the human listing. A nullable
// key holding `null` prints as Python's `None`, which is what the settings
// screen showed.
func formatSettingValue(v any) string {
	switch value := v.(type) {
	case nil:
		return "None"
	case string:
		return value
	case bool:
		if value {
			return "True"
		}
		return "False"
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64)
	case settings.SharedCollisionDomains:
		return strconv.Itoa(int(value))
	default:
		return fmt.Sprint(value)
	}
}

// newSettingsCmd is `cli/command/SettingsCommand.py`, rebuilt.
//
// The Python command "defines no parser and never parses argv": `kathara
// settings -h` silently ignores the `-h` and opens the menu, and there is no
// argparse exit-2 path at all (CLI_SURFACE.md M-5, §12). That is reproduced by
// [commandSpec.NoParser], which skips the whole parse-and-validate block: the
// help flag is never consulted because argv is never read.
//
// What replaces the 1,295-line vendored curses menu (PORT_SPEC §0.2 #1) is a
// numbered prompt loop over the same keys, running the same `settings`
// validators as `kathara config set`. A full-screen bubbletea form is the
// §3.2-item-3 target and is Phase 6 work; what is here is dependency-free, works
// over SSH, and — unlike the curses menu — degrades honestly when there is no
// terminal.
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

func runSettings(a *app) (int, error) {
	if !a.console.TTY {
		return 1, kerrors.New(kerrors.ErrInvocation,
			"`kathara settings` needs a terminal. Use `kathara config get|set|list|reset` instead.")
	}

	reader := bufio.NewReader(a.stdin)
	for {
		keys, err := a.settings.Keys()
		if err != nil {
			return 1, err
		}
		a.console.PrintPanel("Kathara Settings", cliout.PanelOptions{Justify: cliout.JustifyCenter})
		for i, key := range keys {
			value, err := a.settings.Get(key)
			if err != nil {
				return 1, err
			}
			a.console.Print(fmt.Sprintf("%2d) %-22s %s", i+1, key, formatSettingValue(value)))
		}
		a.console.Print("")
		a.console.Print("Enter a number to change a setting, or `q` to save and quit.")

		choice, err := readPrompt(a, reader, "> ")
		if err != nil {
			return 1, err
		}
		if choice == "q" || choice == "" {
			if err := a.settings.Save(""); err != nil {
				return 1, err
			}
			return 0, nil
		}
		index, convErr := strconv.Atoi(choice)
		if convErr != nil || index < 1 || index > len(keys) {
			a.console.Print("Please enter one of the numbers above, or `q`.")
			continue
		}

		key := keys[index-1]
		value, err := readPrompt(a, reader, fmt.Sprintf("%s = ", key))
		if err != nil {
			return 1, err
		}
		if err := a.settings.SetString(key, value); err != nil {
			// A rejected value re-prompts instead of aborting, which is what
			// the curses menu's validators did.
			a.console.Print(err.Error())
		}
	}
}

// readPrompt writes a prompt with no newline and reads one line.
func readPrompt(a *app, reader *bufio.Reader, prompt string) (string, error) {
	a.console.WriteOut([]byte(prompt))
	line, err := reader.ReadString('\n')
	if line == "" && err != nil {
		return "", cliout.ErrPromptEOF
	}
	return strings.TrimSpace(line), nil
}
