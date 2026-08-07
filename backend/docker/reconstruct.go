// This file is `get_lab_from_api` (`DockerManager.py:676`) and
// `update_lab_from_api` (:769): the inverse mapping, from what the daemon says
// is running back into a [model.Lab].
//
// It is the highest-value pure logic in the backend (EXPECTATIONS-docker.md §7
// items 3 and 4) and the only place the port has to UNDO docker-py's
// conversions: bytes back to "64M", nano-CPUs back to a float, a PortBindings
// map back to `(host, proto) → guest`, a DriverOpts string back to interface
// metadata.

package docker

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// reconstructedLabName is the placeholder a hash-addressed reconstruction gets
// (`DockerManager.py:698`): the name that produced the hash is not recoverable
// from the hash, so the scenario is named this and the hash is forced onto it
// afterwards.
const reconstructedLabName = "reconstructed_lab"

// GetLabFromAPI is `get_lab_from_api` (`DockerManager.py:676`).
//
// Its guard is NOT the lab-identifier triple the rest of the interface uses:
// it is `if not lab_hash and not lab_name`, a truthiness test, and when both
// are given `lab_name` WINS rather than erroring (NILABILITY.tsv:65). Both are
// reproduced.
//
// The network listing's scope follows `shared_cds`, and it has to: in a shared
// mode the collision domains a scenario's containers are attached to were
// created by another scenario or another user, and filtering by this
// scenario's hash would miss them.
//
//	NOT_SHARED  filter by this scenario's hash, this user
//	LABS        no hash filter, this user
//	USERS       no hash filter, ALL users
//
// Errors: [kerrors.ErrLabHashOrName] when both are empty; a
// [model.PyRuntimeError] KeyError for a container attached to a network the
// listing did not return, which is where Python's `lab_networks[network_name]`
// crashes.
func (m *Manager) GetLabFromAPI(ctx context.Context, labHash, labName string) (*model.Lab, error) {
	if labHash == "" && labName == "" {
		return nil, kerrors.ErrLabHashOrName
	}

	var lab *model.Lab
	if labName != "" {
		lab = model.NewLab(labName, m.defaults)
	} else {
		lab = model.NewLab(reconstructedLabName, m.defaults)
		// The hash the caller asked for replaces the one the placeholder name
		// produced. Assigning the field directly is what Python does
		// (`reconstructed_lab.hash = lab_hash`); `SetName` would recompute it.
		lab.Hash = labHash
	}

	user, err := scopedUser(false)
	if err != nil {
		return nil, err
	}

	containers, err := m.machine.getByFilters(ctx, lab.Hash, "", user)
	if err != nil {
		return nil, err
	}

	networksByName, err := m.reconstructionNetworks(ctx, lab.Hash)
	if err != nil {
		return nil, err
	}

	for _, c := range containers {
		if err := reloadContainer(ctx, m.api, c); err != nil {
			return nil, err
		}

		device, err := lab.GetOrNewMachine(c.Label(labelName), nil)
		if err != nil {
			return nil, err
		}
		device.APIObject = c

		if err := applyContainerMetas(device, c); err != nil {
			return nil, err
		}
		if err := m.applyContainerInterfaces(lab, device, c, networksByName); err != nil {
			return nil, err
		}
	}

	return lab, nil
}

// reconstructionNetworks is the `lab_networks` / `deployed_networks` dict both
// reconstruction methods build (`DockerManager.py:702-708`, :778-784), keyed by
// DOCKER network name — which is what a container's endpoint map is keyed by.
func (m *Manager) reconstructionNetworks(ctx context.Context, labHash string) (map[string]*Network, error) {
	shared := m.settings.SharedCds

	filterHash := labHash
	if shared != settings.NotShared {
		filterHash = ""
	}
	user, err := scopedUser(shared == settings.SharedBetweenUsers)
	if err != nil {
		return nil, err
	}

	networks, err := m.link.getByFilters(ctx, filterHash, "", user)
	if err != nil {
		return nil, err
	}

	byName := make(map[string]*Network, len(networks))
	for _, n := range networks {
		byName[n.Name()] = n
	}
	return byName, nil
}

// applyContainerMetas is the meta rebuild of `get_lab_from_api`
// (`DockerManager.py:717-740`), whose comment records what CANNOT be rebuilt:
// `exec`, `ipv6` and `num_terms` leave no trace in the container.
//
// Four inversions, each with a zero-value rule:
//
//   - memory: `int(Memory / (1024 ** 2))` — FLOAT division then truncation,
//     not `//` — and always the uppercase `M` suffix regardless of what the
//     user wrote (docker-backend.md gotcha 13). Zero means ABSENT, not zero.
//   - CPUs: `NanoCpus / 1e9`, a float, stored under the meta name `cpu` — NOT
//     `cpus`, which is what the model reads. So a round-tripped scenario loses
//     its CPU limit unless something renames it; that is the Python behaviour
//     and the meta lands in [model.Meta.Extras] where Python's dict took it
//     (OQ-3).
//   - ports: `(int(host_port), protocol) → int(guest_port)`, written STRAIGHT
//     into the meta dict rather than through `add_meta`, so none of the port
//     validation runs.
//   - sysctls: the whole map is ASSIGNED over, again bypassing `add_meta` and
//     therefore the `net.*` namespace check.
//
// The sysctls arrive as a JSON object whose key order Go's decoder does not
// preserve; the keys are sorted so that the rebuilt scenario is the same on
// every run (ORDERING.tsv row 54's "envs/ports ordered maps" has no counterpart
// for sysctls because Python got the order for free).
func applyContainerMetas(device *model.Machine, c *Container) error {
	hostConfig := c.HostConfig()
	if hostConfig == nil || c.Attrs.Config == nil {
		return newPyKeyError("HostConfig")
	}

	if _, _, err := device.AddMeta("privileged", pyBoolString(hostConfig.Privileged)); err != nil {
		return err
	}
	if _, _, err := device.AddMeta("image", c.Attrs.Config.Image); err != nil {
		return err
	}
	shell, ok := c.Labels()[labelShell]
	if !ok {
		return newPyKeyError(labelShell)
	}
	if _, _, err := device.AddMeta("shell", shell); err != nil {
		return err
	}

	if hostConfig.Memory > 0 {
		megabytes := int64(float64(hostConfig.Memory) / (1024 * 1024))
		if _, _, err := device.AddMeta("mem", strconv.FormatInt(megabytes, 10)+"M"); err != nil {
			return err
		}
	}

	if hostConfig.NanoCPUs > 0 {
		// `add_meta("cpu", <float>)` stores a Python float under a meta name
		// the model has no field for, so it lands in Extras — `AddMeta` would
		// store the string form and lose the type.
		device.Meta.Extras.Set("cpu", model.Float(float64(hostConfig.NanoCPUs)/1e9))
	}

	for _, env := range c.Attrs.Config.Env {
		if _, _, err := device.AddMeta("env", env); err != nil {
			return err
		}
	}

	// `if container.attrs['HostConfig']['PortBindings']:` — a truthiness test,
	// so an empty map leaves the ports meta untouched.
	if len(hostConfig.PortBindings) > 0 {
		for _, port := range sortedPortBindings(hostConfig.PortBindings) {
			bindings := hostConfig.PortBindings[port]
			if len(bindings) == 0 {
				// `port_data[0]` on the empty list — an IndexError, which
				// carries no index, not a KeyError.
				return newPyIndexError("list index out of range")
			}
			guestPort, protocol, found := strings.Cut(string(port), "/")
			if !found {
				return kerrors.NewValue("not enough values to unpack (expected 2, got 1)")
			}
			hostPort, err := pyInt(bindings[0].HostPort)
			if err != nil {
				return err
			}
			guest, err := pyInt(guestPort)
			if err != nil {
				return err
			}
			device.Meta.Ports.Set(model.PortKey{HostPort: hostPort, Protocol: protocol}, guest)
		}
	}

	// `device.meta["sysctls"] = container.attrs["HostConfig"]["Sysctls"]` — a
	// wholesale replacement of the container, so anything the model had is
	// gone.
	sysctls := model.NewOrderedMap[string, model.Scalar]()
	for _, key := range sortedKeys(hostConfig.Sysctls) {
		sysctls.Set(key, model.Str(hostConfig.Sysctls[key]))
	}
	device.Meta.Sysctls = sysctls

	return nil
}

// applyContainerInterfaces is the network half of `get_lab_from_api`
// (`DockerManager.py:742-765`).
//
// The `none` network is the marker for a device with no interfaces at all, and
// its presence skips the whole block — so a device on `none` is not bridged and
// has no interfaces, whatever else the endpoint map says. The `bridge` network
// is the marker for a bridged device, and it is POPPED so it does not become a
// collision domain.
//
// The remaining endpoints are sorted by the `kathara.iface` DriverOpt AS A
// STRING, so "10" sorts before "2" (ORDERING.tsv row 55). The interface
// NUMBERS stay correct — `add_interface` is given the number explicitly — and
// the port's ordered interface slice keeps itself sorted numerically, so the
// lexicographic quirk that corrupts Python's insertion order for ten or more
// interfaces is unobservable here. The sort is kept anyway: it is what makes
// the rebuild deterministic, and it is the order the register pins.
func (m *Manager) applyContainerInterfaces(lab *model.Lab, device *model.Machine, c *Container, networksByName map[string]*Network) error {
	endpoints := c.Networks()
	if _, hasNone := endpoints["none"]; hasNone {
		return nil
	}

	// A copy, because Python pops from the container's own attrs and the map
	// is re-read by nothing afterwards — but a Go map is shared with the
	// caller's Container and popping would corrupt it.
	remaining := make(map[string]*network.EndpointSettings, len(endpoints))
	for name, settings := range endpoints {
		remaining[name] = settings
	}

	if _, bridged := remaining["bridge"]; bridged {
		if _, _, err := device.AddMeta("bridged", "True"); err != nil {
			return err
		}
		// `int(container.labels['bridged_iface'])` — the label is INDEXED, so
		// an absent one is a KeyError and only a present-but-malformed one is
		// the ValueError of `int()`.
		raw, ok := c.Labels()[labelBridgedIface]
		if !ok {
			return newPyKeyError(labelBridgedIface)
		}
		number, err := pyInt(raw)
		if err != nil {
			return err
		}
		device.Meta.BridgedIface = model.Int(int64(number))
		delete(remaining, "bridge")
	}

	for _, name := range sortByIfaceOpt(remaining) {
		options := remaining[name]

		n, ok := networksByName[name]
		if !ok {
			// `lab_networks[network_name]` on a network the filtered listing
			// did not return — a container attached to something that is not
			// this scenario's collision domain.
			return newPyKeyError(name)
		}

		link := lab.GetOrNewLink(n.Label(labelName))
		link.APIObject = n

		ifaceNumber, err := ifaceNumberOf(options)
		if err != nil {
			return err
		}

		mac := ""
		if options.DriverOpts != nil {
			mac = options.DriverOpts[driverOptMacAddr]
			if err := applyEndpointSysctls(device, options.DriverOpts, ifaceNumber); err != nil {
				return err
			}
		}

		if _, err := device.AddInterface(link, model.AddInterfaceOptions{
			Number: model.InterfaceNumber(ifaceNumber),
			MAC:    mac,
		}); err != nil {
			return err
		}
	}

	return nil
}

// UpdateLabFromAPI is `update_lab_from_api` (`DockerManager.py:769`): refresh
// an existing scenario in place, adding the collision domains that were
// attached at runtime and removing the declared ones that were detached.
//
// The diff is over Link OBJECTS, not names:
//
//	static   the collision domains the scenario declares
//	current  the ones the container is attached to now
//	dynamic  current − static  → materialise an interface for each
//	deleted  static − current  → remove the interface, which TOMBSTONES the
//	                             slot rather than shifting the others
//
// Every static link gets its api_object refreshed whether or not it is still
// attached, which is what makes a subsequent `undeploy` able to find them.
//
// Python iterates `dynamic_links`, a set of objects whose hash is `id()`, so
// its order varies per run (ORDERING.tsv row 57, flagged "!!"). The iteration
// here follows the same lexicographic `kathara.iface` order the listing was
// sorted into; `deleted_links` is order-insensitive (row 58) and is walked in
// name order.
func (m *Manager) UpdateLabFromAPI(ctx context.Context, lab *model.Lab) error {
	user, err := scopedUser(false)
	if err != nil {
		return err
	}

	containers, err := m.machine.getByFilters(ctx, lab.Hash, "", user)
	if err != nil {
		return err
	}

	networksByName, err := m.reconstructionNetworks(ctx, lab.Hash)
	if err != nil {
		return err
	}
	// `for network in deployed_networks.values(): network.reload()` — the
	// listing was already greedy, so this is a second inspect per network, and
	// it is what picks up a container attached since the listing.
	for _, n := range networksByName {
		if err := reloadNetwork(ctx, m.api, n); err != nil {
			return err
		}
	}

	byLinkName := make(map[string]*Network, len(networksByName))
	for _, n := range networksByName {
		byLinkName[n.Label(labelName)] = n
	}

	for _, c := range containers {
		if err := reloadContainer(ctx, m.api, c); err != nil {
			return err
		}

		device, err := lab.GetOrNewMachine(c.Label(labelName), nil)
		if err != nil {
			return err
		}
		device.APIObject = c

		staticLinks := make(map[string]*model.Link)
		for _, iface := range device.Interfaces() {
			if iface.IsTombstone() {
				// `set([x.link for x in device.interfaces.values()])`
				// (`DockerManager.py:798`) has no guard: the comprehension
				// itself reads `.link` off the None, so the crash is
				// AttributeError on `link` and it happens right here, not at
				// any later `.name`.
				return newPyAttributeError("link")
			}
			staticLinks[iface.Link.Name] = iface.Link
		}

		endpoints := c.Networks()
		remaining := make(map[string]*network.EndpointSettings, len(endpoints))
		for name, options := range endpoints {
			if name == "bridge" || name == "none" {
				continue
			}
			remaining[name] = options
		}

		// `current_ifaces` — the ordered (link, options) pairs.
		order := sortByIfaceOpt(remaining)
		currentByLinkName := make(map[string]*network.EndpointSettings, len(order))
		currentOrder := make([]string, 0, len(order))
		for _, name := range order {
			n, ok := networksByName[name]
			if !ok {
				return newPyKeyError(name)
			}
			link := lab.GetOrNewLink(n.Label(labelName))
			if _, seen := currentByLinkName[link.Name]; !seen {
				currentOrder = append(currentOrder, link.Name)
			}
			// `dict([(x[0].name, x[1]) for x in current_ifaces])` — last wins
			// when two endpoints resolve to the same collision domain.
			currentByLinkName[link.Name] = remaining[name]
		}

		for name, link := range staticLinks {
			if n, ok := byLinkName[name]; ok {
				link.APIObject = n
			}
		}

		// `for link in dynamic_links` iterates a SET of Link objects, so
		// Python's order is `id()`-based and varies run to run — including the
		// `add_meta("sysctl")` application order and the interface insertion
		// order. ORDERING.tsv:57 rules the port iterates by the endpoint's
		// `kathara.iface` NUMBER, which is NOT the lexicographic string order
		// rows 55-56 pin for the listing above ("10" before "2"); that one
		// still decides the last-wins collapse into `currentByLinkName` and is
		// the tie-break here.
		dynamic := make([]string, 0, len(currentOrder))
		for _, name := range currentOrder {
			if _, isStatic := staticLinks[name]; isStatic {
				continue
			}
			dynamic = append(dynamic, name)
		}
		slices.SortStableFunc(dynamic, func(a, b string) int {
			return cmp.Compare(ifaceOptNumber(currentByLinkName[a]), ifaceOptNumber(currentByLinkName[b]))
		})

		for _, name := range dynamic {
			link := lab.GetOrNewLink(name)
			n, ok := byLinkName[name]
			if !ok {
				return newPyKeyError(name)
			}
			link.APIObject = n

			options := currentByLinkName[name]
			ifaceNumber, err := ifaceNumberOf(options)
			if err != nil {
				return err
			}

			mac := ""
			if options.DriverOpts != nil {
				mac = options.DriverOpts[driverOptMacAddr]
				if err := applyEndpointSysctls(device, options.DriverOpts, ifaceNumber); err != nil {
					return err
				}
			}

			if _, err := device.AddInterface(link, model.AddInterfaceOptions{
				Number: model.InterfaceNumber(ifaceNumber),
				MAC:    mac,
			}); err != nil {
				return err
			}
		}

		deleted := make([]string, 0, len(staticLinks))
		for name := range staticLinks {
			if _, stillThere := currentByLinkName[name]; !stillThere {
				deleted = append(deleted, name)
			}
		}
		slices.Sort(deleted)
		for _, name := range deleted {
			if err := device.RemoveInterface(staticLinks[name]); err != nil {
				return err
			}
		}
	}

	return nil
}

// ifaceNumberOf is `int(network_options["DriverOpts"]["kathara.iface"])`
// (`DockerManager.py:755,828`), which runs BEFORE the `is not None` guard on
// the same map two lines below — so a nil DriverOpts crashes here and the
// guard is dead code. Reproduced: the crash is what a container attached to a
// non-Kathará network produces.
func ifaceNumberOf(options *network.EndpointSettings) (int, error) {
	if options == nil || options.DriverOpts == nil {
		return 0, newPyTypeError("'NoneType' object is not subscriptable")
	}
	raw, ok := options.DriverOpts[driverOptIface]
	if !ok {
		return 0, newPyKeyError(driverOptIface)
	}
	return pyInt(raw)
}

// applyEndpointSysctls is the `com.docker.network.endpoint.sysctls` branch
// (`DockerManager.py:761-763`, :833-835): each comma-separated entry becomes a
// `sysctl` meta with the literal `IFNAME` token replaced by this endpoint's
// real interface name.
//
// It is `str.replace`, so EVERY occurrence goes — the mirror of the `re.sub`
// that put the token there ([ifaceSysctls]).
func applyEndpointSysctls(device *model.Machine, driverOpts map[string]string, ifaceNumber int) error {
	value, ok := driverOpts[driverOptSysctls]
	if !ok {
		return nil
	}
	ifname := "eth" + strconv.Itoa(ifaceNumber)
	for _, entry := range strings.Split(value, ",") {
		if _, _, err := device.AddMeta("sysctl", strings.ReplaceAll(entry, ifnamePlaceholder, ifname)); err != nil {
			return err
		}
	}
	return nil
}

// sortByIfaceOpt is `sorted(Networks.items(), key=lambda x:
// x[1]["DriverOpts"]["kathara.iface"])` (`DockerManager.py:748`, :808).
//
// The key is the raw STRING, so "10" < "2" (ORDERING.tsv rows 55-56). An
// endpoint whose DriverOpts are missing the key sorts as the empty string,
// where Python would have raised inside the sort; the raise still happens, one
// step later, in [ifaceNumberOf], which is where Python raises for a nil
// DriverOpts too.
//
// Ties are broken by network name so that the result does not depend on Go's
// map iteration.
func sortByIfaceOpt(endpoints map[string]*network.EndpointSettings) []string {
	names := make([]string, 0, len(endpoints))
	for name := range endpoints {
		names = append(names, name)
	}
	slices.SortFunc(names, func(a, b string) int {
		if c := strings.Compare(ifaceOptOf(endpoints[a]), ifaceOptOf(endpoints[b])); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return names
}

func ifaceOptOf(options *network.EndpointSettings) string {
	if options == nil || options.DriverOpts == nil {
		return ""
	}
	return options.DriverOpts[driverOptIface]
}

// ifaceOptNumber is [ifaceNumberOf] as a total sort key, for ORDERING.tsv:57's
// numeric ordering of the dynamic links.
//
// An endpoint whose `kathara.iface` is missing or unparsable sorts as 0 and
// keeps its relative place under a stable sort; the failure is not swallowed,
// it is merely deferred to the loop body's own [ifaceNumberOf], which is where
// Python raises it too. Python's order for that loop is a set's, so no error
// ordering is more faithful than another.
func ifaceOptNumber(options *network.EndpointSettings) int {
	number, err := ifaceNumberOf(options)
	if err != nil {
		return 0
	}
	return number
}

// pyBoolString is Python's `str(bool)`, which `add_meta("privileged", …)` runs
// through `strtobool`: "True" and "False", capitalised.
func pyBoolString(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// sortedKeys returns a map's keys in ascending order, for the places a Go map
// stands in for a Python dict whose insertion order was observable.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// sortedPortBindings returns the port-binding map's keys in ascending order.
//
// Python iterates `PortBindings.items()` in JSON-object order, which is the
// daemon's, and the insertion order of the rebuilt `ports` meta is what a
// re-deploy would replay. Go's decoder does not preserve that order, so the
// keys are sorted; the meta's own keys carry the host port and protocol, so
// nothing but the ordering of the `OrderedMap` differs.
func sortedPortBindings(bindings nat.PortMap) []nat.Port {
	ports := make([]nat.Port, 0, len(bindings))
	for port := range bindings {
		ports = append(ports, port)
	}
	slices.SortFunc(ports, func(a, b nat.Port) int { return strings.Compare(string(a), string(b)) })
	return ports
}
