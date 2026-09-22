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
