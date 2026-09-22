// This file implements `setting/addon/DockerSettingsAddon.py`: the seven
// keys the file carries when `manager_type` is docker, in the order
// `_to_dict` lists them, which is the order they are appended to the base keys
// on disk.

package settings

import "github.com/KatharaFramework/kathara-go/kerrors"

// availableImageUpdatePolicies is the `image_update_policy` menu of
// `DockerOptionsHandler.py:125-151`, in menu order. It governs what happens
// when a device's image has a newer tag upstream.
var availableImageUpdatePolicies = []string{"Prompt", "Always", "Never"}

// AvailableImageUpdatePolicies returns the `image_update_policy` menu in menu
// order.
func AvailableImageUpdatePolicies() []string {
	return append([]string(nil), availableImageUpdatePolicies...)
}

// availableNetworkPlugins is the `network_plugin` menu of
// `DockerOptionsHandler.py:28-46`, in menu order: the bridge plugin first, the
// VDE one second, which is the default.
var availableNetworkPlugins = []string{"kathara/katharanp", "kathara/katharanp_vde"}

// AvailableNetworkPlugins returns the `network_plugin` menu in menu order.
func AvailableNetworkPlugins() []string {
	return append([]string(nil), availableNetworkPlugins...)
}

// dockerKeys is `DockerSettingsAddon._to_dict` (DockerSettingsAddon.py:31).
var dockerKeys = []keyDesc{
	{name: "hosthome_mount", kind: KindBool, ptr: func(s *Settings) any { return &s.HosthomeMount }},
	{name: "shared_mount", kind: KindBool, ptr: func(s *Settings) any { return &s.SharedMount }},
	{name: "image_update_policy", kind: KindString, ptr: func(s *Settings) any { return &s.ImageUpdatePolicy }, validate: validateImageUpdatePolicyValue},
	{name: "shared_cds", kind: KindInt, ptr: func(s *Settings) any { return &s.SharedCds }, validate: validateSharedCdsValue},
	{name: "remote_url", kind: KindNullableString, ptr: func(s *Settings) any { return &s.RemoteURL }},
	{name: "cert_path", kind: KindNullableString, ptr: func(s *Settings) any { return &s.CertPath }},
	{name: "network_plugin", kind: KindNullableString, ptr: func(s *Settings) any { return &s.NetworkPlugin }, validate: validateNetworkPluginValue},
}

// validateImageUpdatePolicyValue restricts `image_update_policy` to the menu.
func validateImageUpdatePolicyValue(_ *Settings, value any) error {
	return requireOneOf("image_update_policy", value, availableImageUpdatePolicies)
}

// validateSharedCdsValue restricts `shared_cds` to the three enum members the
// menu offered (`DockerOptionsHandler.py:164-190`).
func validateSharedCdsValue(_ *Settings, value any) error {
	v, ok := value.(SharedCollisionDomains)
	if !ok {
		return kerrors.NewSettingsInvalid("Setting `shared_cds` must be an integer.")
	}
	switch v {
	case NotShared, SharedBetweenLabs, SharedBetweenUsers:
		return nil
	}
	return kerrors.NewSettingsInvalid("Setting `shared_cds` must be one of the following: 1, 2, 3.")
}

// validateNetworkPluginValue restricts `network_plugin` to the menu, and
// accepts `null` — the settings screen could not produce one, but a file can
// hold one and [Settings.Set] must be able to put the receiver back into any
// state the file format allows.
func validateNetworkPluginValue(_ *Settings, value any) error {
	v, _ := value.(*string)
	if v == nil {
		return nil
	}
	return requireOneOf("network_plugin", *v, availableNetworkPlugins)
}
