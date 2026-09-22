// This file implements `Setting.check` and `Setting._check_manager`
// (Setting.py:186-243), the startup validation `kathara.py` runs before every
// command whose name does not contain "settings".

package settings

import (
	"regexp"
	"slices"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// prefixPattern is `r"^[a-z]+_?[a-z_]+$"` (Setting.py:216, :221) translated
// for RE2.
var prefixPattern = regexp.MustCompile(`^[a-z]+_?[a-z_]+\n?$`)

// Check is `Setting.check` (Setting.py:186): the startup sanity pass over the
// configuration, in Python's order, with Python's side effect.
func (s *Settings) Check() error {
	if err := s.CheckManager(); err != nil {
		return err
	}

	currentTime := timeNow()
	if currentTime-s.LastChecked > oneWeek {
		s.LastChecked = currentTime
		if err := s.Save(""); err != nil {
			return err
		}
	}

	if err := CheckNetPrefix(s.NetPrefix); err != nil {
		return err
	}
	if err := CheckDevicePrefix(s.DevicePrefix); err != nil {
		return err
	}
	return CheckDebugLevel(s.DebugLevel)
}

// CheckManager is `Setting._check_manager` (Setting.py:231): the
// `manager_type` must be one of [AvailableManagers].
func (s *Settings) CheckManager() error {
	if !slices.Contains(availableManagers, s.ManagerType) {
		return kerrors.ErrSettingsManagerType
	}
	return nil
}

// CheckNetPrefix is the `net_prefix` half of `Setting.check` (Setting.py:215).
func CheckNetPrefix(prefix string) error {
	if _, err := util.ReSearchFail(prefixPattern, prefix); err != nil {
		return kerrors.ErrSettingsNetworksPrefix
	}
	return nil
}

// CheckDevicePrefix is the `device_prefix` half of `Setting.check`
// (Setting.py:220). Same pattern as [CheckNetPrefix], different message.
func CheckDevicePrefix(prefix string) error {
	if _, err := util.ReSearchFail(prefixPattern, prefix); err != nil {
		return kerrors.ErrSettingsDevicePrefix
	}
	return nil
}

// CheckDebugLevel is the `debug_level` half of `Setting.check`
// (Setting.py:225): membership in [AvailableDebugLevels], exact and
// case-sensitive.
func CheckDebugLevel(level string) error {
	if !slices.Contains(availableDebugLevels, level) {
		return kerrors.ErrSettingsDebugLevel
	}
	return nil
}
