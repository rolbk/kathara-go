// This file folds in the `validator/` package (§3.2 item 4): the three
// validators the settings screen hung off `consolemenu`, plus the value
// restrictions its menus expressed by only offering the legal answers.
//
// Python's validators return a bool and `print()` the reason, because
// `consolemenu` re-prompts on False and had nowhere else to put the text. Here
// they return the error, so that `kathara config set` can report it, the
// settings form can show it next to the field, and the two cannot disagree.
// "Valid" is `err == nil`.
//
// One member of the family is deliberately absent. The settings screen also
// guarded `remote_url`, `api_server_url`, `api_token`, `cert_path` and
// `device_shell` with `consolemenu`'s own `RegexValidator`, which belongs to
// the replaced prompt layer rather than to `validator/`. Porting the URL one
// would be actively harmful: it is `re.match` with no `re.IGNORECASE` against
// a pattern built from `[A-Z0-9]` character classes, so it rejects every
// lower-case domain name a user could type (DIVERGENCES.md).

package settings

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// requireOneOf is the shape every menu-restricted key shares: membership in a
// fixed list, reported with the sentence `Setting.check` uses for
// `debug_level`. The keys with a frozen Python message use that message
// instead and do not come through here.
func requireOneOf(name string, value any, allowed []string) error {
	v, ok := value.(string)
	if !ok {
		return kerrors.NewSettingsInvalid("Setting `" + name + "` must be a string.")
	}
	if slices.Contains(allowed, v) {
		return nil
	}
	return kerrors.NewSettingsInvalid(
		"Setting `" + name + "` must be one of the following: " + strings.Join(allowed, ", ") + ".")
}

// validateManagerTypeValue is `Setting._check_manager` run on a candidate
// value rather than on the loaded one.
func validateManagerTypeValue(_ *Settings, value any) error {
	v, ok := value.(string)
	if !ok || !slices.Contains(availableManagers, v) {
		return kerrors.ErrSettingsManagerType
	}
	return nil
}

// validateNetPrefixValue is the `net_prefix` check of `Setting.check`, which
// is also the `RegexValidator(r"^[a-z]+_?[a-z_]+$")` the settings screen put
// on the same field.
func validateNetPrefixValue(_ *Settings, value any) error {
	v, _ := value.(string)
	return CheckNetPrefix(v)
}

// validateDevicePrefixValue is the `device_prefix` counterpart.
func validateDevicePrefixValue(_ *Settings, value any) error {
	v, _ := value.(string)
	return CheckDevicePrefix(v)
}

// validateDebugLevelValue is the `debug_level` check of `Setting.check`.
func validateDebugLevelValue(_ *Settings, value any) error {
	v, _ := value.(string)
	return CheckDebugLevel(v)
}

// validateVolumeMountPolicyValue restricts `volume_mount_policy` to the menu.
func validateVolumeMountPolicyValue(_ *Settings, value any) error {
	return requireOneOf("volume_mount_policy", value, availableVolumeMountPolicies)
}

// validateTerminalValue is `validator/TerminalValidator.validate`: it defers
// to [Settings.CheckTerminal], which means setting this key touches the
// filesystem (or, on macOS, LaunchServices). That is the settings screen's own
// behaviour — the terminal menu's free-text answer went through the same
// validator — and it is why `kathara config set terminal /usr/bin/foo` fails
// when the emulator is not installed.
func validateTerminalValue(s *Settings, value any) error {
	v, _ := value.(string)
	return s.CheckTerminal(v)
}

// CheckTerminal is `Setting.check_terminal` (Setting.py:275): the configured
// terminal emulator must be something this platform can actually launch.
//
// An empty terminal means "check the configured one" (`if not terminal`,
// NILABILITY.tsv:45), so a caller validating the current setting passes "".
//
// "TMUX" is a special value, not a program: it selects the tmux backend and
// short-circuits the check (Setting.py:288). Python returns True for it and
// True at the end, which is the bool `TerminalValidator` hands back; the
// information content is "did not raise", so the Go answer is a nil error.
//
// The check itself is per-platform and asymmetric on purpose (Setting.py:293):
// Linux tests for an executable file, macOS resolves an application by name
// through LaunchServices because the setting is a name and not a path, and
// Windows does not check at all. A platform that is none of the three falls
// off the end of `exec_by_platform` with None, which is falsy, so the terminal
// is reported invalid — reproduced in terminal_other.go.
func (s *Settings) CheckTerminal(terminal string) error {
	if terminal == "" {
		terminal = s.Terminal
	}

	if terminal == "TMUX" {
		return nil
	}

	ok, err := terminalAvailable(terminal)
	if err != nil {
		return err
	}
	if !ok {
		return kerrors.NewSettingsTerminal(terminal)
	}
	return nil
}

// ImageChecker is what `Setting.check_image` reaches for: the backend's
// `check_image`, which pulls the image's manifest and verifies its
// architecture.
//
// It is an interface because `settings` sits below `kathara` in the import
// graph and cannot name the client. Python resolves the same dependency at
// call time with a function-local `from ..manager.Kathara import Kathara`,
// which is the cycle this interface replaces.
type ImageChecker interface {
	CheckImage(ctx context.Context, image string) error
}

// CheckImage is `Setting.check_image` (Setting.py:257). An empty image means
// the configured one (`if not image`, NILABILITY.tsv:44).
func (s *Settings) CheckImage(ctx context.Context, checker ImageChecker, image string) error {
	if image == "" {
		image = s.Image
	}
	return checker.CheckImage(ctx, image)
}

// IsImageRejection reports whether err is one of the three classes
// `validator/ImageValidator.validate` catches — a connection failure, an
// unknown image, or an image built for another architecture. Those are the
// answers that mean "the user typed a bad image name", which the settings
// screen answered by re-prompting; anything else propagates.
//
// It exists because a bool return would have thrown the reason away, and the
// reason is what the caller shows.
func IsImageRejection(err error) bool {
	return errors.Is(err, kerrors.ErrConnection) ||
		errors.Is(err, kerrors.ErrDockerImageNotFound) ||
		errors.Is(err, kerrors.ErrInvalidImageArchitecture)
}

// ValidateDockerConfigJSON is `validator/DockerConfigJsonValidator.validate`:
// the named path must expand, open, and parse as JSON.
//
// It takes the path the user types, not the value that ends up in
// `docker_config_json` — the settings screen validates the path and then
// stores the base64 of the file's contents. `~` is expanded the way
// `os.path.expanduser` expands it, because the offered default is
// [DefaultDockerConfigJSONPath].
//
// Python catches `(OSError, ValueError)` and prints the CPython message. The
// two branches keep their exception classes here — `OS` for the open, `Value`
// for the parse (ERROR_CODES.md §1.2) — but the interpolated text is Go's
// runtime text, which is not portable in either direction.
func ValidateDockerConfigJSON(path string) error {
	data, err := os.ReadFile(expandUser(path))
	if err != nil {
		return kerrors.WrapOS(err, err.Error())
	}

	var parsed any
	if err := json.Unmarshal(data, &parsed); err != nil {
		return kerrors.Wrap(kerrors.ErrValue, err, err.Error())
	}
	return nil
}
