package model

// Defaults are the four `Setting` values the model falls back to when a device
// says nothing.
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
func DefaultDefaults() Defaults {
	return Defaults{
		Image:             "kathara/base",
		DeviceShell:       "/bin/bash",
		EnableIPv6:        false,
		VolumeMountPolicy: "Always",
	}
}
