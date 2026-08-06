package model

// Defaults are the four `Setting` values the model falls back to when a device
// says nothing.
//
// In 3.8.3 they are read through `Setting.get_instance()` from inside
// `get_image`, `get_shell`, `get_num_terms`'s call chain, `get_volumes` and
// `is_ipv6_enabled` — a model → settings edge, and a process-wide singleton
// read from pool workers. The OQ-4 resolution (PACKAGE_GRAPH.md §1.1, §1.2)
// removes both: the caller resolves the settings and hands the result to
// [NewLab], so this package imports no `settings` and a process can hold two
// scenarios with different defaults.
//
// The zero value is usable but empty, which is not what any real caller wants:
// `cmd` and `kathara` build it from a loaded `*settings.Settings`, and the
// field names are the setting keys. [DefaultDefaults] is the 3.8.3 factory
// default, for tests and for an API user with no configuration file.
type Defaults struct {
	// Image is `Setting.image`, the fallback of [Machine.GetImage].
	Image string
	// DeviceShell is `Setting.device_shell`, the fallback of
	// [Machine.GetShell].
	DeviceShell string
	// EnableIPv6 is `Setting.enable_ipv6`, the fallback of
	// [Machine.IsIPv6Enabled].
	EnableIPv6 bool
	// VolumeMountPolicy is `Setting.volume_mount_policy`, consulted by
	// [Machine.GetVolumes] when the scenario carries no `_mount_volumes`
	// general option. "Prompt" and "Always" allow the mount; anything else,
	// "Never" included, denies it (`model/Machine.py:589-590`).
	VolumeMountPolicy string
}

// DefaultDefaults is `Setting()`'s factory state for the four keys the model
// reads (`setting/Setting.py:22-33`): the `kathara/base` image, `/bin/bash`,
// IPv6 off and the `Always` volume policy.
//
// It is what `settings.Defaults()` produces, restated here because this package
// must not import that one. Neither side can test the other without re-creating
// the edge OQ-4 removed, so the two are pinned separately against the same
// Python source: [TestDefaultDefaults] here, `settings.TestDefaults` there. A
// change to `Setting.py` has to land in both.
func DefaultDefaults() Defaults {
	return Defaults{
		Image:             "kathara/base",
		DeviceShell:       "/bin/bash",
		EnableIPv6:        false,
		VolumeMountPolicy: "Always",
	}
}
