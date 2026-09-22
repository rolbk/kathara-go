// This file is the payload half of `DockerMachine.create`
// (`DockerMachine.py:206-393`): everything between reading the model and
// calling the daemon, with no daemon in it.
// It exists as its own file because docker-py's `containers.create(**kwargs)`
// is a translation layer the Go SDK does not have — `mem_limit="64m"` becomes
// `HostConfig.Memory`, `volumes={host: {bind, mode}}` becomes
// `HostConfig.Binds`, `ports={"55/udp": 3000}` becomes both `ExposedPorts` and
// `PortBindings`, and `network=` silently OVERRIDES `network_mode=` — and
// getting those conversions wrong is invisible until a golden runs. Each is
// reproduced from docker-py 7.2.0's own source and pinned by a table test.

package docker

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"

	"github.com/KatharaFramework/kathara-go/model"
)

// rpFilterNamespace is `RP_FILTER_NAMESPACE` (`DockerMachine.py:34`).
const rpFilterNamespace = "net.ipv4.conf.%s.rp_filter"

func rpFilter(iface string) string { return strings.Replace(rpFilterNamespace, "%s", iface, 1) }

// containerSysctls is the sysctl block of `DockerMachine.create`
// (`DockerMachine.py:272-298`), in Python's merge order — which is the whole
// point, since later writers win.
func containerSysctls(engineVersion string, machine *model.Machine, hasFirstInterface bool) (map[string]string, error) {
	// Insertion order is not observable — the daemon canonicalises the map —
	// but the OVERRIDE order is, so the merge is done on an ordered structure
	// and flattened at the end.
	sysctls := model.NewOrderedMap[string, model.Scalar]()
	for _, iface := range []string{"all", "default", "lo"} {
		sysctls.Set(rpFilter(iface), model.Int(0))
	}
	sysctls.Set("net.ipv4.ip_forward", model.Int(1))
	sysctls.Set("net.ipv4.icmp_ratelimit", model.Int(0))

	// `sysctl_first_interface` is decided BEFORE `is_ipv6_enabled` is read
	// (`DockerMachine.py:277-282`), and the order is observable: on a device
	// with an interface, an unparsable engine version (`version_lt` → `int("")`
	// ValueError) wins over an invalid `ipv6` meta. The entry itself is applied
	// last, after the device's own sysctls.
	firstIfaceRPFilter := false
	if hasFirstInterface {
		oldEngine, err := versionLT(engineVersion, engineFirstIfaceRPFilter)
		if err != nil {
			return nil, err
		}
		firstIfaceRPFilter = oldEngine
	}

	ipv6, err := machine.IsIPv6Enabled()
	if err != nil {
		return nil, err
	}
	if ipv6 {
		sysctls.Set("net.ipv6.conf.all.forwarding", model.Int(1))
		sysctls.Set("net.ipv6.conf.all.accept_ra", model.Int(0))
		sysctls.Set("net.ipv6.icmp.ratelimit", model.Int(0))
		sysctls.Set("net.ipv6.conf.default.disable_ipv6", model.Int(0))
		sysctls.Set("net.ipv6.conf.all.disable_ipv6", model.Int(0))
	} else {
		sysctls.Set("net.ipv6.conf.default.disable_ipv6", model.Int(1))
		sysctls.Set("net.ipv6.conf.all.disable_ipv6", model.Int(1))
		sysctls.Set("net.ipv6.conf.default.forwarding", model.Int(0))
		sysctls.Set("net.ipv6.conf.all.forwarding", model.Int(0))
	}

	for _, entry := range machine.Sysctls().Entries() {
		sysctls.Set(entry.Key, entry.Value)
	}

	if firstIfaceRPFilter {
		sysctls.Set(rpFilter("eth0"), model.Int(0))
	}

	newEngine, err := versionGTE(engineVersion, engineEndpointSysctls)
	if err != nil {
		return nil, err
	}

	out := make(map[string]string, sysctls.Len())
	for _, entry := range sysctls.Entries() {
		if newEngine && ifaceSysctlRE.MatchString(entry.Key) {
			continue
		}
		out[entry.Key] = entry.Value.String()
	}
	return out, nil
}

// parseMemory is docker-py's `utils.parse_bytes` applied to `mem_limit`
// (`DockerMachine.py:227,373`), which is the string
// [model.Machine.GetMem] produces: an integer followed by one of `b`, `k`, `m`
// or `g`, lower-cased by the model.
func parseMemory(mem string) int64 {
	if mem == "" {
		return 0
	}

	unit := int64(1)
	digits := mem
	switch mem[len(mem)-1] {
	case 'b':
		digits = mem[:len(mem)-1]
	case 'k':
		unit, digits = units.KiB, mem[:len(mem)-1]
	case 'm':
		unit, digits = units.MiB, mem[:len(mem)-1]
	case 'g':
		unit, digits = units.GiB, mem[:len(mem)-1]
	}

	value, err := strconv.ParseFloat(digits, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		// Unreachable from `GetMem`, which has already run the digits through
		// Python's `int()`. docker-py raises `DockerException` here; the zero
		// is "no limit", the same answer an omitted key gives.
		return 0
	}
	if math.IsInf(value, 0) {
		if value < 0 {
			return math.MinInt64
		}
		return math.MaxInt64
	}

	product := new(big.Float).Mul(big.NewFloat(value), new(big.Float).SetInt64(unit))
	scaled, _ := product.Int(nil)
	if !scaled.IsInt64() {
		if scaled.Sign() < 0 {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	return scaled.Int64()
}

// portBindings is docker-py's `convert_port_bindings` over the dict
// `DockerMachine.create` builds at `:231-236`:
func portBindings(ports *model.OrderedMap[model.PortKey, int]) (nat.PortMap, nat.PortSet) {
	if ports.Len() == 0 {
		return nil, nil
	}

	bindings := nat.PortMap{}
	for _, entry := range ports.Entries() {
		port := nat.Port(strconv.Itoa(entry.Value) + "/" + entry.Key.Protocol)
		// A DICT ASSIGNMENT, not an append: the model keys by `(host, proto)`
		// so two `port=` lines can name the same guest port with different
		// host ports, and Python's `ports[key] = host_port` keeps only the
		// LAST. `convert_port_bindings` then wraps that single value in a
		// one-element list. Appending would bind both host ports.
		bindings[port] = []nat.PortBinding{{
			HostIP:   "",
			HostPort: strconv.Itoa(entry.Key.HostPort),
		}}
	}

	exposed := nat.PortSet{}
	for port := range bindings {
		exposed[port] = struct{}{}
	}
	return bindings, exposed
}

// bind is one element of docker-py's `convert_volume_binds` output:
// `"{host}:{bind}:{mode}"` (verified against 7.2.0).
func bind(hostPath, guestPath, mode string) string {
	return hostPath + ":" + guestPath + ":" + mode
}

// envList is docker-py's `format_environment` over `machine.meta['envs']`:
// `["KEY=value", ...]` in the dict's insertion order, which the model preserves
// and which is visible in `docker inspect` (`Config.Env`).
func envList(envs *model.OrderedMap[string, string]) []string {
	if envs.Len() == 0 {
		return nil
	}
	out := make([]string, 0, envs.Len())
	for _, entry := range envs.Entries() {
		out = append(out, entry.Key+"="+entry.Value)
	}
	return out
}

func ulimitList(ulimits *model.OrderedMap[string, model.Ulimit]) []*container.Ulimit {
	out := make([]*container.Ulimit, 0, ulimits.Len())
	for _, entry := range ulimits.Entries() {
		out = append(out, &container.Ulimit{
			Name: entry.Key,
			Soft: entry.Value.Soft,
			Hard: entry.Value.Hard,
		})
	}
	return out
}

// createRequest is everything `client.containers.create(**kwargs)` sends, in
// the three structs the Go SDK splits it into plus the name.
type createRequest struct {
	Name       string
	Config     *container.Config
	HostConfig *container.HostConfig
	Networking *network.NetworkingConfig
}

// createArgs is the `client.containers.create(...)` call of
// `DockerMachine.create` (`DockerMachine.py:363-384`) translated through
// docker-py's `_create_container_args`.
func createArgs(
	name string,
	image string,
	hostname string,
	privileged bool,
	firstNetworkName string,
	endpoint *network.EndpointSettings,
	envs []string,
	sysctls map[string]string,
	memory int64,
	nanoCPUs int64,
	bindings nat.PortMap,
	exposed nat.PortSet,
	binds []string,
	mountPoints []string,
	labels map[string]string,
	ulimits []*container.Ulimit,
	entrypoint []string,
	args []string,
) createRequest {
	capAdd := model.MachineCapabilities()
	if privileged {
		capAdd = nil
	}

	// `create_kwargs['volumes']` is set only `if volumes:`, and
	// `ContainerConfig` posts `'Volumes': None` otherwise — a nil map here.
	var volumes map[string]struct{}
	if len(mountPoints) > 0 {
		volumes = make(map[string]struct{}, len(mountPoints))
		for _, target := range mountPoints {
			volumes[target] = struct{}{}
		}
	}

	networkMode := "none"
	var networking *network.NetworkingConfig
	if firstNetworkName != "" {
		networkMode = firstNetworkName
		if endpoint != nil {
			networking = &network.NetworkingConfig{
				EndpointsConfig: map[string]*network.EndpointSettings{firstNetworkName: endpoint},
			}
		}
	}

	return createRequest{
		Name: name,
		Config: &container.Config{
			Image:        image,
			Hostname:     hostname,
			Env:          envs,
			Labels:       labels,
			Tty:          true,
			OpenStdin:    true,
			ExposedPorts: exposed,
			Volumes:      volumes,
			Entrypoint:   entrypoint,
			Cmd:          args,
		},
		HostConfig: &container.HostConfig{
			NetworkMode:  container.NetworkMode(networkMode),
			CapAdd:       capAdd,
			Privileged:   privileged,
			Sysctls:      sysctls,
			PortBindings: bindings,
			Binds:        binds,
			Resources: container.Resources{
				Memory:   memory,
				NanoCPUs: nanoCPUs,
				Ulimits:  ulimits,
			},
		},
		Networking: networking,
	}
}

// networkCreateOptions is the `client.networks.create(...)` call of
// `DockerLink.create` (`DockerLink.py:137-148`).
func networkCreateOptions(driver string, labels map[string]string) network.CreateOptions {
	return network.CreateOptions{
		Driver: driver,
		IPAM:   &network.IPAM{Driver: "null"},
		Labels: labels,
	}
}
