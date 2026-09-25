// `DockerMachine.get_container_name` (:1073), `DockerLink.get_network_name`
// (:399), the two `*_by_filters` label lists (`DockerMachine.py:1010-1016`,
// `DockerLink.py:240-246`) and the two label literals
// (`DockerMachine.py:347-356`, `DockerLink.py:142-147`).

package docker

import (
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// The label keys of the frozen schema. They are spelled once so that the
// builders and the filters cannot drift apart.
const (
	labelApp          = "app"
	labelName         = "name"
	labelLabHash      = "lab_hash"
	labelUser         = "user"
	labelShell        = "shell"
	labelBridgedIface = "bridged_iface"
	labelExternal     = "external"

	// labelAppValue is the constant every Kathará object carries and every
	// listing filters on first.
	labelAppValue = "kathara"
)

// ContainerName is `DockerMachine.get_container_name` (`DockerMachine.py:1073`):
// `"{device_prefix}_{user}_{name}_{lab_hash}"`.
func ContainerName(devicePrefix, user, machineName, labHash string) string {
	return devicePrefix + "_" + user + "_" + machineName + "_" + labHash
}

// NetworkName is `DockerLink.get_network_name` (`DockerLink.py:399`), the one
// name that changes with `shared_cds`:
func NetworkName(netPrefix, user, linkName, labHash string, shared settings.SharedCollisionDomains) string {
	switch shared {
	case settings.NotShared:
		return netPrefix + "_" + user + "_" + linkName + "_" + labHash
	case settings.SharedBetweenLabs:
		return netPrefix + "_" + user + "_" + linkName
	case settings.SharedBetweenUsers:
		return netPrefix + "_" + linkName
	}
	return ""
}

type LabelFilter struct {
	Key   string
	Value string
}

// String renders the term the way Python builds it: `f"{key}={value}"`.
func (f LabelFilter) String() string { return f.Key + "=" + f.Value }

// ObjectFilters is the `filters["label"]` list of
// `get_machines_api_objects_by_filters` (`DockerMachine.py:1010-1016`) and
// `get_links_api_objects_by_filters` (`DockerLink.py:240-246`), which are the
// same six lines twice.
func ObjectFilters(user, labHash, name string) []LabelFilter {
	filters := make([]LabelFilter, 0, 4)
	filters = append(filters, LabelFilter{labelApp, labelAppValue})
	if user != "" {
		filters = append(filters, LabelFilter{labelUser, user})
	}
	if labHash != "" {
		filters = append(filters, LabelFilter{labelLabHash, labHash})
	}
	if name != "" {
		filters = append(filters, LabelFilter{labelName, name})
	}
	return filters
}

// ContainerLabels is the `labels` dict of `DockerMachine.create`
// (`DockerMachine.py:347-356`): five keys, plus `bridged_iface` on a bridged
// device.
func ContainerLabels(machineName, labHash, user, shell string, bridgedIface *int) map[string]string {
	labels := map[string]string{
		labelName:    machineName,
		labelLabHash: labHash,
		labelUser:    user,
		labelApp:     labelAppValue,
		labelShell:   shell,
	}
	if bridgedIface != nil {
		labels[labelBridgedIface] = strconv.Itoa(*bridgedIface)
	}
	return labels
}

// NetworkLabels is the `labels` dict of `DockerLink.create`
// (`DockerLink.py:142-147`), whose key set SHRINKS as sharing widens:
func NetworkLabels(linkName, user, labHash, external string, shared settings.SharedCollisionDomains) map[string]string {
	labels := map[string]string{
		labelName:     linkName,
		labelApp:      labelAppValue,
		labelExternal: external,
	}
	if shared != settings.SharedBetweenUsers {
		labels[labelUser] = user
	}
	if shared == settings.NotShared {
		labels[labelLabHash] = labHash
	}
	return labels
}

// NetworkDriver is the `driver=` of `DockerLink.create` (`DockerLink.py:139`)
// and the plugin reference `DockerPlugin` resolves (`DockerPlugin.py:30`):
// `f"{network_plugin}:{architecture}"`.
func NetworkDriver(networkPlugin, architecture string) string {
	return networkPlugin + ":" + architecture
}

// pluginNameOf renders `Setting.network_plugin` the way Python's f-strings do.
func pluginNameOf(s *settings.Settings) string {
	if s.NetworkPlugin == nil {
		return "None"
	}
	return *s.NetworkPlugin
}

// BridgeName is `DockerLink._get_bridge_name` (`DockerLink.py:387`):
// `f"kt-{network.id[:12]}"`, the host bridge a Docker network gets.
func BridgeName(networkID string) string {
	if len(networkID) > 12 {
		networkID = networkID[:12]
	}
	return "kt-" + networkID
}

// externalLabel is the `external` label's value for a collision domain:
// `";".join(x.get_full_name() for x in link.external)` (`DockerLink.py:145`).
func externalLabel(link *model.Link) (string, error) {
	names := make([]string, 0, len(link.External))
	for _, external := range link.External {
		names = append(names, external.FullName())
	}
	return strings.Join(names, ";"), nil
}
