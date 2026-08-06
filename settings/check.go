// This file is the port of `Setting.check` and `Setting._check_manager`
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
//
// The `\n?` is not decoration. Python's `$` matches at the end of the string
// *or* just before a trailing newline, so `re.search(r"^[a-z]+$", "kathara\n")`
// succeeds and a prefix with a stray newline is accepted; Go's `$` without
// `(?m)` is end-of-text only. `internal/util.ReSearchFail` documents the same
// translation for the same two call sites, which are the only two in the
// codebase.
//
// The pattern is also stricter than its message admits: `[a-z]+_?[a-z_]+`
// needs at least two characters, so a one-letter prefix is rejected with
// "must only contain lowercase letters and underscore", and a leading
// underscore is rejected too.
var prefixPattern = regexp.MustCompile(`^[a-z]+_?[a-z_]+\n?$`)

// Check is `Setting.check` (Setting.py:186): the startup sanity pass over the
// configuration, in Python's order, with Python's side effect.
//
// The order matters and is observable:
//
//  1. `_check_manager` — an unknown `manager_type` stops here, so the
//     last_checked write below never happens for a broken manager.
//  2. The weekly update bookkeeping, including the write to disk.
//  3. `net_prefix`, then `device_prefix`, then `debug_level`.
//
// Step 2 runs *before* the three value checks, so a file with a bad
// `net_prefix` is still rewritten with a fresh `last_checked` before the error
// is reported. Probed against 3.8.3; DIVERGENCES.md records the write.
//
// # What the deferred webhook leaves behind
//
// Python's step 2 asks GitHub for the latest release, logs a notice when the
// running version is older, and then — *only if the call succeeded* — sets
// `last_checked` to now and saves the file. A `HTTPConnectionError` leaves
// both alone, so an offline machine retries on every run.
//
// The webhook is deferred (§0.3), and `last_checked` stays in the schema
// (§0.4). Of the two branches only one can survive the removal, and it is the
// success branch: with no call to fail, `checked` is never set to False. So
// Check stamps `last_checked` and rewrites the file exactly when Python would
// have after a reachable GitHub, which is the common case and the one every
// existing install's file reflects. Probed against 3.8.3 with the webhook
// stubbed both ways. It is an autonomous ruling on deferred-feature semantics,
// not a row of the frozen RULINGS.md; DIVERGENCES.md item 34 records it, along
// with the one host class that notices — a machine that has always been
// offline.
//
// The write goes to [DefaultPath], not to wherever the receiver was loaded
// from: `check()` calls `save_to_disk()` with no argument. A per-scenario
// `kathara.conf` loaded by `Command._load_custom_configuration` therefore gets
// copied over the user's global settings the first time a week has passed.
// That is a Python bug, it is in DIVERGENCES.md, and it is reproduced.
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
//
// Python asks `Kathara.get_available_managers_name()` for the set, which is
// built by iterating `AVAILABLE_MANAGERS` — the list that lives in
// `Setting.py` in the first place (SYNTHESIS C-5). The comparison is exact and
// case-sensitive, which is what makes a `manager_type` of "DOCKER" load fine
// and then fail here.
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
