package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

func TestCheckStampsAndSavesWhenStale(t *testing.T) {
	const now = 1_800_000_000.5
	pinClock(t, now)

	path := filepath.Join(t.TempDir(), Filename)
	pinDefaultPath(t, path)

	s := Defaults()
	s.LastChecked = now - oneWeek - 1

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if s.LastChecked != now {
		t.Errorf("last_checked = %v, want %v", s.LastChecked, now)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Check did not write the file: %v", err)
	}
	if !strings.Contains(string(written), `"last_checked": 1800000000.5`) {
		t.Errorf("file does not carry the new stamp: %s", written)
	}
}

// TestCheckDoesNotSaveWhenFresh: inside the week, Python neither calls GitHub
// nor writes. Probed against 3.8.3.
func TestCheckDoesNotSaveWhenFresh(t *testing.T) {
	const now = 1_800_000_000.0
	pinClock(t, now)

	path := filepath.Join(t.TempDir(), Filename)
	pinDefaultPath(t, path)

	s := Defaults()
	s.LastChecked = now - oneWeek // strictly greater than is required

	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if s.LastChecked != now-oneWeek {
		t.Errorf("last_checked moved to %v", s.LastChecked)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("Check wrote the file while the stamp was still fresh")
	}
}

func TestCheckWritesToDefaultPathNotTheLoadedOne(t *testing.T) {
	const now = 1_800_000_000.0
	pinClock(t, now)

	defaultPath := filepath.Join(t.TempDir(), Filename)
	pinDefaultPath(t, defaultPath)

	labDir := writeConf(t, `{"net_prefix":"labpfx","last_checked":1.0}`)
	s, err := Load(labDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}

	global, err := os.ReadFile(defaultPath)
	if err != nil {
		t.Fatalf("the default path was not written: %v", err)
	}
	if !strings.Contains(string(global), `"net_prefix": "labpfx"`) {
		t.Error("the scenario's settings did not land in the global file")
	}

	lab, err := os.ReadFile(filepath.Join(labDir, Filename))
	if err != nil {
		t.Fatalf("read lab conf: %v", err)
	}
	if strings.Contains(string(lab), `"last_checked": 1800000000.0`) {
		t.Error("the scenario's own file was rewritten; Python leaves it alone")
	}
}

// TestCheckOrder pins the two places the order is observable: an unknown
// manager stops before the write, and a bad prefix does not.
func TestCheckOrder(t *testing.T) {
	const now = 1_800_000_000.0

	t.Run("manager failure precedes the write", func(t *testing.T) {
		pinClock(t, now)
		path := filepath.Join(t.TempDir(), Filename)
		pinDefaultPath(t, path)

		s := Defaults()
		s.LastChecked = 1
		s.ManagerType = "DOCKER"

		if err := s.Check(); !errors.Is(err, kerrors.ErrSettingsManagerType) {
			t.Fatalf("Check = %v, want ErrSettingsManagerType", err)
		}
		if _, err := os.Stat(path); err == nil {
			t.Error("the file was written despite the manager check failing first")
		}
	})

	t.Run("prefix failure follows the write", func(t *testing.T) {
		pinClock(t, now)
		path := filepath.Join(t.TempDir(), Filename)
		pinDefaultPath(t, path)

		s := Defaults()
		s.LastChecked = 1
		s.NetPrefix = "BAD"

		if err := s.Check(); !errors.Is(err, kerrors.ErrSettingsNetworksPrefix) {
			t.Fatalf("Check = %v, want ErrSettingsNetworksPrefix", err)
		}
		written, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("the file should already have been written: %v", err)
		}
		if !strings.Contains(string(written), `"last_checked": 1800000000.0`) {
			t.Error("the stamp was not written before the prefix check ran")
		}
	})

	t.Run("net_prefix precedes device_prefix", func(t *testing.T) {
		pinClock(t, now)
		s := Defaults()
		s.LastChecked = now
		s.NetPrefix = "BAD"
		s.DevicePrefix = "ALSO_BAD"

		if err := s.Check(); !errors.Is(err, kerrors.ErrSettingsNetworksPrefix) {
			t.Fatalf("Check = %v, want the networks-prefix error first", err)
		}
	})

	t.Run("prefixes precede debug_level", func(t *testing.T) {
		pinClock(t, now)
		s := Defaults()
		s.LastChecked = now
		s.DevicePrefix = "BAD"
		s.DebugLevel = "LOUD"

		if err := s.Check(); !errors.Is(err, kerrors.ErrSettingsDevicePrefix) {
			t.Fatalf("Check = %v, want the device-prefix error first", err)
		}
	})
}

// TestCheckAcceptsDefaults is the happy path: a fresh install passes, having
// just stamped itself.
func TestCheckAcceptsDefaults(t *testing.T) {
	pinClock(t, 1_800_000_000.0)
	pinDefaultPath(t, filepath.Join(t.TempDir(), Filename))

	if err := Defaults().Check(); err != nil {
		t.Fatalf("Check on defaults: %v", err)
	}
}

// TestPrefixRegexAgainstOracle replays `utils.re_search_fail(r"^[a-z]+_?[a-z_]+$", v)`
// (testdata/prefix_regex.json). The rows that matter are the length floor of
// two, the rejected leading underscore, and the trailing newline Python's `$`
// accepts and Go's does not without the `\n?`.
func TestPrefixRegexAgainstOracle(t *testing.T) {
	type row struct {
		In    string `json:"in"`
		Valid bool   `json:"valid"`
	}
	rows := loadJSONFixture[[]row](t, "prefix_regex.json")
	if len(rows) < 30 {
		t.Fatalf("fixture shrank: %d rows", len(rows))
	}

	for _, r := range rows {
		err := CheckNetPrefix(r.In)
		if got := err == nil; got != r.Valid {
			t.Errorf("CheckNetPrefix(%q) valid = %v, want %v", r.In, got, r.Valid)
		}
		if err != nil && !errors.Is(err, kerrors.ErrSettingsNetworksPrefix) {
			t.Errorf("CheckNetPrefix(%q) = %v, want ErrSettingsNetworksPrefix", r.In, err)
		}

		devErr := CheckDevicePrefix(r.In)
		if got := devErr == nil; got != r.Valid {
			t.Errorf("CheckDevicePrefix(%q) valid = %v, want %v", r.In, got, r.Valid)
		}
		if devErr != nil && !errors.Is(devErr, kerrors.ErrSettingsDevicePrefix) {
			t.Errorf("CheckDevicePrefix(%q) = %v, want ErrSettingsDevicePrefix", r.In, devErr)
		}
	}
}

// TestCheckDebugLevel is membership in AVAILABLE_DEBUG_LEVELS, exact and
// case-sensitive.
func TestCheckDebugLevel(t *testing.T) {
	for _, level := range AvailableDebugLevels() {
		if err := CheckDebugLevel(level); err != nil {
			t.Errorf("CheckDebugLevel(%q) = %v", level, err)
		}
	}
	for _, level := range []string{"", "info", "Info", "TRACE", "INFO "} {
		if err := CheckDebugLevel(level); !errors.Is(err, kerrors.ErrSettingsDebugLevel) {
			t.Errorf("CheckDebugLevel(%q) = %v, want it rejected", level, err)
		}
	}
}

func TestDebugLevelMessageMatchesRegistry(t *testing.T) {
	want := "Settings file is not valid: Debug Level must be one of the following: " +
		strings.Join(availableDebugLevels, ", ") + ". Fix it or delete it before launching."
	if got := kerrors.ErrSettingsDebugLevel.Error(); got != want {
		t.Errorf("frozen message and AVAILABLE_DEBUG_LEVELS disagree\n got: %q\nwant: %q", got, want)
	}
}

// TestCheckManager is the exact, case-sensitive comparison against
// AVAILABLE_MANAGERS.
func TestCheckManager(t *testing.T) {
	s := Defaults()
	for _, name := range AvailableManagers() {
		s.ManagerType = name
		if err := s.CheckManager(); err != nil {
			t.Errorf("CheckManager(%q) = %v", name, err)
		}
	}
	for _, name := range []string{"", "Docker", "DOCKER", "podman", " docker"} {
		s.ManagerType = name
		if err := s.CheckManager(); !errors.Is(err, kerrors.ErrSettingsManagerType) {
			t.Errorf("CheckManager(%q) = %v, want it rejected", name, err)
		}
	}
}
