// This file is the frozen half of the backend (PORT_SPEC §0.4, SYNTHESIS §1.2):
// how a device and a collision domain get their Docker names, which labels go
// on them, and which label filters find them again. Nothing here talks to a
// daemon, and everything here is what the Layer A goldens read back out of
// `docker inspect`.
//
// `DockerMachine.get_container_name` (:1073), `DockerLink.get_network_name`
// (:399), the two `*_by_filters` label lists (`DockerMachine.py:1010-1016`,
// `DockerLink.py:240-246`) and the two label literals
// (`DockerMachine.py:347-356`, `DockerLink.py:142-147`).

package docker

import (
	"strconv"

	"github.com/KatharaFramework/kathara-go/kerrors"
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
//
// It carries a dead conditional that the port keeps (SYNTHESIS §1.2,
// docker-backend.md gotcha 3):
//
//	lab_hash = lab_hash if "_%s" % lab_hash else ""
//
// The tested expression is the *formatted string*, which is at least one
// character long for every input including None, so it is always truthy and
// the `else ""` is unreachable. A `None` hash therefore reaches the name as the
// literal "None" rather than being dropped. Go has no None here — the hash is a
// string and an empty one produces a trailing underscore, which is what
// `"%s_%s_%s_%s"` does with `""` too — so the conditional collapses to "keep
// the input", which is the only branch Python ever takes.
//
// Note what it does NOT do: the name is the same in all three `shared_cds`
// modes. Only [NetworkName] varies (SYNTHESIS C-7 — the two shared-CD
// `get_container_name` tests in the Python suite are assertion-free and imply
// otherwise; source wins).
func ContainerName(devicePrefix, user, machineName, labHash string) string {
	return devicePrefix + "_" + user + "_" + machineName + "_" + labHash
}

// NetworkName is `DockerLink.get_network_name` (`DockerLink.py:399`), the one
// name that changes with `shared_cds`:
//
//	NotShared          {net_prefix}_{user}_{link}_{lab_hash}
//	SharedBetweenLabs  {net_prefix}_{user}_{link}
//	SharedBetweenUsers {net_prefix}_{link}
//
// Python's chain is three `if`/`elif`s with no `else`, so a `shared_cds` outside
// 1-3 returns None and the network gets created with `name=None` — nothing
// validates the setting on load, so the value is reachable. The Go zero-value
// analogue is the empty string, and the caller passes it to the daemon exactly
// as Python passes None; both are rejected there.
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

// LabelFilter is one `label=value` term of a listing filter, kept as a pair so
// that the order of the terms — which is observable, SYNTHESIS §1.2 "Filter
// list order (goldens see it)" — is expressible in a slice.
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
//
// The order is fixed and the tests pin it: `app=kathara`, then `user`, then
// `lab_hash`, then `name`. Each of the last three is appended only when the
// argument is truthy, and "" is exactly as absent as Python's None
// (NILABILITY.tsv — "`""` treated same as None (truthiness in filter
// building)").
//
// name is `machine_name` on the container side and `link_name` on the network
// side; both label the object's *Kathará* name, not its Docker one.
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
//
// `shell` is `machine.meta["shell"]` when set and `Setting.device_shell`
// otherwise, which is `Machine.get_shell()` spelled inline — Python does not
// call the accessor here, but the two branches are identical and the model's
// accessor already carries the settings default (OQ-4). `bridged_iface` is
// `str(meta['bridged_iface'])`, i.e. Python's `str()` of whatever is stored,
// which for the value `create` itself computes is an int.
//
// The returned map is unordered on purpose: Docker canonicalises label order
// and ORDERING.tsv row 38 records the dict literal as not load-bearing.
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
//
//	NotShared          name, app, external, user, lab_hash
//	SharedBetweenLabs  name, app, external, user
//	SharedBetweenUsers name, app, external
//
// That is what makes `DockerLinkStats.__init__` KeyError on `lab_hash` and
// `user` in the shared modes (SYNTHESIS §1.2; stats sampling is deferred, and
// [linkStats] reads the labels defensively for it).
//
// `external` is `";".join(x.get_full_name() for x in link.external)` — a
// semicolon, not a comma (SYNTHESIS C-8) — and it is always the empty string in
// 1.0 because `lab.ext` is deferred (ORDERING.tsv row 49). The key is still
// emitted: `_delete_link` reads it unguarded, and an absent key would KeyError
// there.
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
//
// `network_plugin` is nullable in the settings schema and Python interpolates a
// None as the literal "None" (settings.Settings.NetworkPlugin's doc records
// why the pointer survives); the caller renders that, so this takes the already
// rendered string.
func NetworkDriver(networkPlugin, architecture string) string {
	return networkPlugin + ":" + architecture
}

// pluginNameOf renders `Setting.network_plugin` the way Python's f-strings do.
//
// The setting is nullable (`network_plugin: null` round-trips through the
// config file), and Python interpolates a None as the literal string "None" —
// at `DockerPlugin.py:30` and again at `DockerLink.py:139`. Substituting the
// default instead would rewrite the user's file on the next save, which
// PORT_SPEC §0.4 freezes against, so the rendering is done here and the
// pointer stays ([settings.Settings.NetworkPlugin]).
func pluginNameOf(s *settings.Settings) string {
	if s.NetworkPlugin == nil {
		return "None"
	}
	return *s.NetworkPlugin
}

// BridgeName is `DockerLink._get_bridge_name` (`DockerLink.py:387`):
// `f"kt-{network.id[:12]}"`, the host bridge a Docker network gets.
//
// Its only callers are the external-interface paths, which are deferred
// (PORT_SPEC §0.3); it is here because the name is part of the frozen naming
// schema and the deferred code has to be able to name it when it lands.
//
// The slice is by bytes, as Python's is — a network id is hex, so the two agree.
// A shorter id is returned whole rather than panicking, which is also what
// Python's slice does.
func BridgeName(networkID string) string {
	if len(networkID) > 12 {
		networkID = networkID[:12]
	}
	return "kt-" + networkID
}

// externalLabel is the `external` label's value for a collision domain:
// `";".join(x.get_full_name() for x in link.external)` (`DockerLink.py:145`).
//
// In 1.0 it is always the empty string and the second return is always nil,
// because nothing can fill [model.Link.External]: `lab.ext` parsing is deferred
// and [model.Lab.AttachExternalLinks] answers `FeatureNotAvailable`
// (PORT_SPEC §0.3, ORDERING.tsv row 49). `get_full_name` — which truncates the
// interface name to IFNAMSIZ — is deliberately not ported with it
// ([model.ExternalLink]), so a hand-built non-empty slice has no name to join
// and gets the deferral error instead of a wrong label. That is the same answer
// `_attach_external_interfaces` would give one line later
// (PACKAGE_GRAPH.md §2.8: "External-CD paths (lab.ext) DEFERRED →
// FeatureNotAvailable").
func externalLabel(link *model.Link) (string, error) {
	if len(link.External) == 0 {
		return "", nil
	}
	return "", kerrors.NewFeatureNotAvailable(kerrors.FeatureLabExt)
}
