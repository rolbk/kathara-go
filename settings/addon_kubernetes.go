// This file implements `setting/addon/KubernetesSettingsAddon.py`: the
// five keys the file carries when `manager_type` is kubernetes, in `_to_dict`
// order.

package settings

// availableImagePullPolicies is the `image_pull_policy` menu of
// `KubernetesOptionsHandler.py:132-156`, in menu order. The values are
// Kubernetes' own `imagePullPolicy` spellings and are copied into the pod spec
// verbatim (`KubernetesMachine.py:474`), so a fourth spelling is rejected by
// the API server rather than by Kathará.
var availableImagePullPolicies = []string{"Always", "IfNotPresent", "Never"}

// AvailableImagePullPolicies returns the `image_pull_policy` menu in menu
// order.
func AvailableImagePullPolicies() []string {
	return append([]string(nil), availableImagePullPolicies...)
}

// DefaultDockerConfigJSONPath is `DEFAULT_DOCKER_CONFIG_JSON_PATH`
// (KubernetesSettingsAddon.py:13): the path the settings screen offers as the
// pre-filled answer when asking which docker `config.json` to read. It is a
// path to read, not a value to store — the stored value is that file's
// base64.
const DefaultDockerConfigJSONPath = "~/.docker/config.json"

// kubernetesKeys is `KubernetesSettingsAddon._to_dict`
// (KubernetesSettingsAddon.py:26).
var kubernetesKeys = []keyDesc{
	{name: "api_server_url", kind: KindNullableString, ptr: func(s *Settings) any { return &s.APIServerURL }},
	{name: "api_token", kind: KindNullableString, ptr: func(s *Settings) any { return &s.APIToken }},
	{name: "host_shared", kind: KindBool, ptr: func(s *Settings) any { return &s.HostShared }},
	{name: "image_pull_policy", kind: KindNullableString, ptr: func(s *Settings) any { return &s.ImagePullPolicy }, validate: validateImagePullPolicyValue},
	{name: "docker_config_json", kind: KindNullableString, ptr: func(s *Settings) any { return &s.DockerConfigJSON }},
}

// validateImagePullPolicyValue restricts `image_pull_policy` to the menu, and
// accepts `null` for the reason [validateNetworkPluginValue] does.
func validateImagePullPolicyValue(_ *Settings, value any) error {
	v, _ := value.(*string)
	if v == nil {
		return nil
	}
	return requireOneOf("image_pull_policy", *v, availableImagePullPolicies)
}
