// This file is the payload half of `DockerMachine.create`
// (`DockerMachine.py:206-393`): everything between reading the model and
// calling the daemon, with no daemon in it.
//
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
//
//	baseline           rp_filter=0 for all/default/lo, ip_forward=1,
//	                   icmp_ratelimit=0, then the IPv6 block or the IPv6-off
//	                   block
//	machine sysctls    the device's own, overriding the baseline
//	first-interface    eth0's rp_filter=0 on engine < 26 only, overriding both
//	engine >= 27       every interface-scoped sysctl is then REMOVED, because
//	                   those move to per-endpoint DriverOpts
//
// The last two steps do not overlap by accident: on engine >= 27 the eth0 entry
// is not added in the first place (the `version_lt(…, "26.0.0")` guard is
// false), and on engine < 26 the filter does not run. On 26.x neither happens,
// which is the version gap the two constants document.
//
// Values are `str()`-ed, because docker-py stringifies the whole dict before
// posting it (`HostConfig`'s `sysctls` handling, verified against 7.2.0: an int
// 1 is sent as `"1"`). [model.Scalar.String] is that `str()`.
//
// hasFirstInterface is `first_machine_iface is not None`, which is "the device
// has at least one interface" — not "the device has eth0". A device whose
// lowest interface number is 3 still gets the eth0 entry, because Python reads
// `machine.interfaces[0]` by KEY and would have raised first
// (docker-backend.md gotcha 9); [machineService.firstInterface] is where that
// difference is handled.
//
// Errors: [model.Machine.IsIPv6Enabled]'s, and [versionLT]/[versionGTE]'s parse
// failure on an unusable engine version.
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
//
// The empty string is "no limit", which is `mem_limit=None` in Python: the key
// is omitted and `HostConfig.Memory` stays 0.
//
// # Why the digits go through a float
//
// `parse_bytes` is `int(float(digits_part) * units[suffix])`
// (`docker/utils/utils.py:433-441`) — the digits become a BINARY64 before they
// are scaled, so anything above 2^53 is rounded to the nearest representable
// value and `mem=9007199254740993b` posts …992, not …993. An exact big-integer
// parse would post a number Python never sends, so the float64 round trip is
// reproduced and the product is then taken exactly (the unit is a power of two,
// so the multiply is exact in binary and `big.Float.Int` truncates toward zero
// exactly as `int()` does).
//
// # Where it still diverges (DIVERGENCES.md 68)
//
// Python's result is an arbitrary-precision int and `HostConfig.Memory` is an
// int64 here, so a product at or above 2^63 SATURATES instead of being posted:
// Python sends the big value, the daemon fails to decode it and answers 400,
// while the saturated value is a legal int64 the daemon accepts. `float()` of
// digits beyond ~1.8e308 is `inf` in Python and `int(inf)` raises OverflowError;
// this returns the saturated value there too rather than growing an error
// return for an input no scenario writes.
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
//
//	ports['%d/%s' % (guest_port, protocol)] = host_port
//
// Note the inversion — the model keys by `(host_port, protocol)` and the Docker
// payload keys by guest port — and that the protocol travels with the GUEST
// port even though the model attached it to the host one.
//
// The inversion is LOSSY, and Python loses it silently: two `port=` lines that
// name the same guest port and protocol survive the model as two entries and
// collapse to one dict slot here, last writer winning.
//
// docker-py emits `{"55/udp": [{"HostIp": "", "HostPort": "3000"}]}` and,
// separately, an `ExposedPorts` entry per binding built from
// `sorted(port_bindings.keys())` (`_create_container_args`). Both are returned
// here; the sort is docker-py's own and is why the exposed set is deterministic
// where the model's insertion order is not.
//
// Python passes `ports=None` — not `{}` — when the device declares none
// (NILABILITY.tsv), so both results are nil in that case and neither key
// reaches the payload.
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

// ulimitList is `[Ulimit(name=k, soft=v["soft"], hard=v["hard"]) for k, v in
// machine.get_ulimits().items()]` (`DockerMachine.py:229`), in insertion order
// — the list order is visible in `docker inspect`'s `HostConfig.Ulimits`
// (ORDERING.tsv row 32).
//
// Unlike [envList] and [portBindings], the empty case is an empty LIST, not
// nil: the Python expression is a comprehension, so a device with no `ulimit=`
// option passes `ulimits=[]`, and docker-py's `create_host_config` gates on
// `if ulimits is not None` — the empty list goes into the payload and the
// daemon records `"Ulimits": []`. A nil slice would encode as `null`, since the
// SDK field carries no `omitempty`.
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
//
// It is a struct rather than four return values so that a table test can hold
// one and compare fields, which is what PORT_SPEC §9C asks for: assert the
// request payload you build, not the sequence of SDK calls you make.
type createRequest struct {
	Name       string
	Config     *container.Config
	HostConfig *container.HostConfig
	Networking *network.NetworkingConfig
}

// createArgs is the `client.containers.create(...)` call of
// `DockerMachine.create` (`DockerMachine.py:363-384`) translated through
// docker-py's `_create_container_args`.
//
// The one translation nobody expects, and which changes what `docker inspect`
// reports: docker-py assigns `host_config_kwargs['network_mode'] = network`
// AFTER copying the explicit `network_mode` across, so the literal
// `network_mode="bridge"` at `:369` is OVERWRITTEN by the first collision
// domain's Docker network name whenever there is one. `NetworkMode` is
// therefore the kathara network, never "bridge"; only the no-interface case
// keeps its literal, which is "none".
//
// The rest, in Python's own order:
//
//   - `cap_add` is the five [model.MachineCapabilities] unless privileged, in
//     which case it is None and Docker grants everything anyway.
//   - `tty`, `stdin_open` and `detach` are all True. `detach` has no wire
//     representation — it tells docker-py not to wait — so only the first two
//     appear here.
//   - `entrypoint` is `shlex.split(meta['entrypoint'])`, `command` is
//     `meta['args']` shlex-split when it is a string and passed through when it
//     is a list; both are nil when the meta is absent or falsy
//     (NILABILITY.tsv: "`args` also None when meta present but falsy").
//   - `volumes` is an ordered map, so `Binds` comes out in the order shared →
//     hosthome → device volumes (ORDERING.tsv row 36; the list order is
//     golden-visible). The SAME dict also becomes `Config.Volumes`, a set of
//     the guest paths, because docker-py passes it twice (see
//     [machineService.volumeBinds]); `mountPoints` is that second view and is
//     nil exactly when `binds` is, so a device with no volumes posts a null
//     there as Python does.
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
//
// `check_duplicate=True` has no Go counterpart and needs none: the option was
// removed from the Engine API in v1.44 (the daemon rejects duplicate names
// unconditionally now) and docker-py sends it as a query parameter the daemon
// ignores. The behaviour it asked for is the behaviour that happens.
//
// `ipam=IPAMConfig(driver='null')` disables address management, which is what
// makes a Kathará collision domain a pure L2 segment: no subnet, no gateway,
// no addresses handed out. `network.IPAM{Driver: "null"}` is the same request.
func networkCreateOptions(driver string, labels map[string]string) network.CreateOptions {
	return network.CreateOptions{
		Driver: driver,
		IPAM:   &network.IPAM{Driver: "null"},
		Labels: labels,
	}
}
