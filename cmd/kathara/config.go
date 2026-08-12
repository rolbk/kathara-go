// This file is PORT_SPEC §3.2 item 2: the scriptable settings interface.
//
// It has no Python original — the 3.8.3 CLI could only edit settings through
// the curses menu, which is why the menu's bugs were unworkaroundable. Every
// value it writes goes through `settings/`'s own validators, which is §3.2
// item 4 ("so `config set` and the TUI cannot disagree"); nothing here
// re-implements a check, and the two entry points share even the conversion
// from a command-line word to a schema value ([settings.Settings.SetString]).
//
// JSON_CLI_CONTRACT.md §3.12 pins the four envelope shapes.

package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/settings"
)

// newConfigCmd builds the command. The four operations are one `nargs='*'`
// positional rather than four cobra sub-commands, because the top-level
// dispatcher (root.go) parses exactly one word and hands the rest to a single
// parser: a nested cobra command tree would need a second dispatcher.
func newConfigCmd(a *app) *commandSpec {
	cmd := newParser("config")
	cmd.Short = "Read and write Kathara settings"
	cmd.Long = cmd.Short
	registerFormat(cmd, false)
	cmd.pos("get|set|list|reset", nargsZeroOrMore, "The settings operation to perform.")

	return &commandSpec{
		Name: "config",
		Cmd:  cmd,
		Run: func(_ context.Context, a *app, positional, _ []string) (int, error) {
			return runConfigCmd(a, positional)
		},
	}
}

func runConfigCmd(a *app, positional []string) (int, error) {
	if len(positional) == 0 {
		return 2, errUsage("the following arguments are required: get|set|list|reset")
	}

	switch positional[0] {
	case "get":
		if len(positional) != 2 {
			return 2, errUsage("config get takes exactly one KEY")
		}
		return configGet(a, positional[1])

	case "set":
		if len(positional) != 3 {
			return 2, errUsage("config set takes exactly one KEY and one VALUE")
		}
		return configSet(a, positional[1], positional[2])

	case "list":
		if len(positional) != 1 {
			return 2, errUsage("config list takes no arguments")
		}
		return configList(a)

	case "reset":
		switch len(positional) {
		case 1:
			return configResetAll(a)
		case 2:
			return configResetKey(a, positional[1])
		}
		return 2, errUsage("config reset takes at most one KEY")
	}

	return 2, errUsage("unknown config subcommand `%s`", positional[0])
}

func configGet(a *app, key string) (int, error) {
	value, err := a.settings.Get(key)
	if err != nil {
		return 1, err
	}
	a.console.Print(fmt.Sprintf("%s = %s", key, formatSettingValue(value)))
	a.console.Emit(cliout.SettingsGetResult{Key: key, Value: value})
	return 0, nil
}

// configSet is the write path.
//
// `docker_config_json` is the one key whose command-line word is not its value:
// the settings screen asked for a *path* and stored the base64 of that file's
// re-serialized JSON, so this does the same (see
// [settings.EncodeDockerConfigJSON]). The empty string still means `null`,
// which is the screen's "Reset value to Empty String" item.
func configSet(a *app, key, raw string) (int, error) {
	value := raw
	if key == dockerConfigJSONKey && raw != "" {
		encoded, err := settings.EncodeDockerConfigJSON(raw)
		if err != nil {
			return 1, err
		}
		value = encoded
	}

	if err := a.settings.SetString(key, value); err != nil {
		return 1, err
	}
	if err := a.settings.Save(a.settingsDir); err != nil {
		return 1, err
	}

	stored, err := a.settings.Get(key)
	if err != nil {
		return 1, err
	}
	a.console.Print(fmt.Sprintf("%s = %s", key, formatSettingValue(stored)))
	a.console.Emit(cliout.SettingsSetResult{Key: key, Value: stored, Saved: true})
	return 0, nil
}

func configList(a *app) (int, error) {
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
}

// configResetAll is `reset` with no key: the whole file goes back to
// `Setting.__init__`, including the `last_checked` stamp, which [settings.Defaults]
// backdates by a week so the next `check()` does its update bookkeeping.
func configResetAll(a *app) (int, error) {
	fresh := settings.Defaults()
	if err := fresh.Save(a.settingsDir); err != nil {
		return 1, err
	}
	*a.settings = *fresh
	a.console.Print("Settings reset to their defaults.")
	a.console.Emit(cliout.SettingsResetResult{Settings: a.settings, Saved: true})
	return 0, nil
}

// configResetKey is `reset <key>`: one key back to its default, the rest of the
// file untouched.
//
// The default is read off a fresh [settings.Defaults] whose `manager_type` has
// been forced to the *current* one, because which addon keys exist at all
// depends on it — `api_token` is not a key of a docker-typed schema. The field
// is assigned directly rather than through Set so that a file already holding
// an unusable `manager_type` can still have its other keys reset.
//
// `manager_type` itself is the one key that must not be forced: doing so would
// read the current manager back as its own "default", making `config reset
// manager_type` a no-op that still reported `saved: true`. That key reads the
// real default (`docker`) and goes through Set like any other, so the switch
// also resets the newly selected addon.
//
// The write goes through [settings.Settings.Set], so a default that this host
// cannot accept is refused exactly as `config set` would refuse it: on a
// machine with no xterm, `config reset terminal` reports the same
// `SettingsError` as `config set terminal /usr/bin/xterm`.
func configResetKey(a *app, key string) (int, error) {
	fresh := settings.Defaults()
	if key != "manager_type" {
		fresh.ManagerType = a.settings.ManagerType
	}

	value, err := fresh.Get(key)
	if err != nil {
		return 1, err
	}
	if err := a.settings.Set(key, value); err != nil {
		return 1, err
	}
	if err := a.settings.Save(a.settingsDir); err != nil {
		return 1, err
	}

	a.console.Print(fmt.Sprintf("%s = %s", key, formatSettingValue(value)))
	a.console.Emit(cliout.SettingsResetResult{Settings: a.settings, Saved: true})
	return 0, nil
}

// dockerConfigJSONKey is the one key `config set` reads as a path.
const dockerConfigJSONKey = "docker_config_json"

// formatSettingValue renders one schema value for the human listing, in
// Python's own spellings: a nullable key holding `null` prints as `None`, and a
// bool as `True`/`False`, which is what `print(setting)` would have shown.
// The settings form renders the same values the *menu* spelled them
// (`Yes`/`No`, the `shared_cds` sentence); see displaySettingValue.
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
