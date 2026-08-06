package model

import "testing"

// TestDefaultDefaults pins the four settings-derived fallbacks against
// `setting/Setting.py:22-33`, the factory state of `Setting.__init__`.
//
// The values are duplicated in `settings.Defaults()` and cannot be compared
// against it from here: the OQ-4 resolution removed the model → settings edge,
// so each side is pinned separately against the same Python source
// (`settings.TestDefaults` is the other half).
func TestDefaultDefaults(t *testing.T) {
	t.Parallel()

	got := DefaultDefaults()
	want := Defaults{
		Image:             "kathara/base",
		DeviceShell:       "/bin/bash",
		EnableIPv6:        false,
		VolumeMountPolicy: "Always",
	}
	if got != want {
		t.Errorf("DefaultDefaults() = %+v, want %+v", got, want)
	}
}
