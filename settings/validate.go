// Python's validators return a bool and `print()` the reason, because
// `consolemenu` re-prompts on False and had nowhere else to put the text. Here
// they return the error, so that `kathara config set` can report it, the
// settings form can show it next to the field, and the two cannot disagree.
// "Valid" is `err == nil`.
// One member of the family is deliberately absent.

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

// Reserved values of the `terminal` key. They name a terminal *integration*
// rather than a program, so [Settings.CheckTerminal] short-circuits on both
// instead of looking for an executable.
const (
	// TerminalTMUX selects the tmux backend. Python already reserved this
	// value in this key (Setting.py:288).
	TerminalTMUX = "TMUX"

	TerminalMultiplexer = "MULTIPLEXER"
)

// CheckTerminal is `Setting.check_terminal` (Setting.py:275): the configured
// terminal emulator must be something this platform can actually launch.
func (s *Settings) CheckTerminal(terminal string) error {
	if terminal == "" {
		terminal = s.Terminal
	}

	if terminal == TerminalTMUX || terminal == TerminalMultiplexer {
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
type ImageChecker interface {
	CheckImage(ctx context.Context, image string) error
}

// CheckImage is `Setting.check_image` (Setting.py:257).
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
func IsImageRejection(err error) bool {
	return errors.Is(err, kerrors.ErrConnection) ||
		errors.Is(err, kerrors.ErrDockerImageNotFound) ||
		errors.Is(err, kerrors.ErrInvalidImageArchitecture)
}

// ValidateDockerConfigJSON is `validator/DockerConfigJsonValidator.validate`:
// the named path must expand, open, and parse as JSON.
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
