package docker

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// baseSysctlsIPv6Off is the profile `DockerMachine.create` builds for a device
// with `enable_ipv6` false, before the device's own sysctls and the engine
// forks (`DockerMachine.py:273-292`).
var baseSysctlsIPv6Off = map[string]string{
	"net.ipv4.conf.all.rp_filter":        "0",
	"net.ipv4.conf.default.rp_filter":    "0",
	"net.ipv4.conf.lo.rp_filter":         "0",
	"net.ipv4.ip_forward":                "1",
	"net.ipv4.icmp_ratelimit":            "0",
	"net.ipv6.conf.default.disable_ipv6": "1",
	"net.ipv6.conf.all.disable_ipv6":     "1",
	"net.ipv6.conf.default.forwarding":   "0",
	"net.ipv6.conf.all.forwarding":       "0",
}

// baseSysctlsIPv6On is the same profile with `enable_ipv6` true: the two
// `disable_ipv6` entries flip to 0 and three forwarding/RA entries replace the
// two off-mode forwarding ones.
var baseSysctlsIPv6On = map[string]string{
	"net.ipv4.conf.all.rp_filter":        "0",
	"net.ipv4.conf.default.rp_filter":    "0",
	"net.ipv4.conf.lo.rp_filter":         "0",
	"net.ipv4.ip_forward":                "1",
	"net.ipv4.icmp_ratelimit":            "0",
	"net.ipv6.conf.all.forwarding":       "1",
	"net.ipv6.conf.all.accept_ra":        "0",
	"net.ipv6.icmp.ratelimit":            "0",
	"net.ipv6.conf.default.disable_ipv6": "0",
	"net.ipv6.conf.all.disable_ipv6":     "0",
}

func withEntries(base map[string]string, extra map[string]string) map[string]string {
	out := maps.Clone(base)
	maps.Copy(out, extra)
	return out
}

// TestContainerSysctls covers the engine forks of EXPECTATIONS-docker.md §1.1
// (`test_create_interface_old_engine`) and the merge precedence of
// ORDERING.tsv row 35.
func TestContainerSysctls(t *testing.T) {
	tests := []struct {
		name          string
		engineVersion string
		ipv6          bool
		sysctls       []string
		hasFirstIface bool
		want          map[string]string
	}{
		{
			name: "engine 27, no interfaces, ipv6 off", engineVersion: "27.0.0",
			want: baseSysctlsIPv6Off,
		},
		{
			name: "engine 27, ipv6 on", engineVersion: "27.0.0", ipv6: true,
			want: baseSysctlsIPv6On,
		},
		{
			// `version_lt(engine, "26.0.0")` and a first interface: eth0's
			// rp_filter goes into the CONTAINER sysctls, because an engine that
			// old cannot set per-endpoint ones.
			name: "engine 25 with an interface adds eth0 rp_filter", engineVersion: "25.0.0", hasFirstIface: true,
			want: withEntries(baseSysctlsIPv6Off, map[string]string{"net.ipv4.conf.eth0.rp_filter": "0"}),
		},
		{
			name: "engine 25 without an interface does not", engineVersion: "25.0.0",
			want: baseSysctlsIPv6Off,
		},
		{
			// 26.x is below neither fork: no eth0 entry and no stripping.
			name: "engine 26 is in the gap", engineVersion: "26.0.0", hasFirstIface: true,
			want: baseSysctlsIPv6Off,
		},
		{
			name: "a device sysctl overrides the baseline", engineVersion: "27.0.0",
			sysctls: []string{"net.ipv4.ip_forward=0"},
			want:    withEntries(baseSysctlsIPv6Off, map[string]string{"net.ipv4.ip_forward": "0"}),
		},
		{
			// engine >= 27 strips every interface-scoped sysctl from the
			// container: they move to per-endpoint DriverOpts.
			name: "engine 27 strips interface-scoped device sysctls", engineVersion: "27.0.0",
			sysctls: []string{"net.ipv4.conf.eth0.rp_filter=1", "net.ipv6.neigh.eth1.anycast_delay=50"},
			want:    baseSysctlsIPv6Off,
		},
		{
			// On the old engine they stay, and the eth0 override wins over the
			// device's own value — it is merged LAST.
			name: "engine 25 keeps them and the eth0 override wins", engineVersion: "25.0.0", hasFirstIface: true,
			sysctls: []string{"net.ipv4.conf.eth0.rp_filter=1", "net.ipv6.neigh.eth1.anycast_delay=50"},
			want: withEntries(baseSysctlsIPv6Off, map[string]string{
				"net.ipv4.conf.eth0.rp_filter":      "0",
				"net.ipv6.neigh.eth1.anycast_delay": "50",
			}),
		},
		{
			// A non-numeric sysctl value stays a string through Python's
			// `str()`, which is what docker-py posts.
			name: "a string sysctl value survives", engineVersion: "27.0.0",
			sysctls: []string{"net.ipv4.conf.all.arp_ignore=abc"},
			want:    withEntries(baseSysctlsIPv6Off, map[string]string{"net.ipv4.conf.all.arp_ignore": "abc"}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := newFixtureMachine(t, tt.ipv6, tt.sysctls...)
			got, err := containerSysctls(tt.engineVersion, machine, tt.hasFirstIface)
			if err != nil {
				t.Fatalf("containerSysctls: %v", err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("containerSysctls =\n %v\nwant\n %v", got, tt.want)
			}
		})
	}
}

// TestContainerSysctlsRejectsAnUnusableEngineVersion is the ValueError
// Python's `int()` raises for an empty component, which is what a daemon reporting "v27.0.0" produces:
// `parse_docker_engine_version` answers "" and the comparison raises.
func TestContainerSysctlsRejectsAnUnusableEngineVersion(t *testing.T) {
	machine := newFixtureMachine(t, false)
	if _, err := containerSysctls("", machine, false); err == nil {
		t.Fatal("an empty engine version was accepted")
	}
}

// TestContainerSysctlsEvaluatesTheEngineForkFirst pins the precedence
// `DockerMachine.py:277-282` fixes: with a first interface, `version_lt` runs
// BEFORE `is_ipv6_enabled`, so when BOTH the engine version and the `ipv6` meta
// are unusable the version's ValueError is the one that escapes. Reading the
// ipv6 meta first surfaces the other error for the same input.
func TestContainerSysctlsEvaluatesTheEngineForkFirst(t *testing.T) {
	machine := newFixtureMachine(t, false)
	machine.Meta.IPv6 = model.Str("not-a-bool")

	// Sanity: the ipv6 meta really is broken, and it wins when there is no
	// first interface (`sysctl_first_interface` is not computed at all then).
	if _, err := containerSysctls("27.0.0", machine, false); !errors.Is(err, kerrors.ErrMachineOption) {
		t.Fatalf("a broken ipv6 meta gave %v, want the Option bucket", err)
	}

	_, err := containerSysctls("", machine, true)
	if errors.Is(err, kerrors.ErrMachineOption) {
		t.Fatal("the ipv6 meta was read before the engine-version fork")
	}
	if want := `invalid literal for int() with base 10: ''`; err == nil || err.Error() != want {
		t.Errorf("error = %v, want the engine version's %q", err, want)
	}
}

// TestParseMemory is docker-py's `parse_bytes` over the strings
// [model.Machine.GetMem] produces, checked against the values the oracle
// returns for the same inputs.
func TestParseMemory(t *testing.T) {
	tests := []struct {
		mem  string
		want int64
	}{
		{"", 0}, // `mem_limit=None`: the key is omitted
		{"64m", 67108864},
		{"100m", 104857600},
		{"5b", 5},
		{"2g", 2147483648},
		{"3k", 3072},
		{"0m", 0},
		{"-5m", -5242880}, // the model does not reject a negative; the daemon does
	}

	for _, tt := range tests {
		t.Run(tt.mem, func(t *testing.T) {
			if got := parseMemory(tt.mem); got != tt.want {
				t.Errorf("parseMemory(%q) = %d, want %d", tt.mem, got, tt.want)
			}
		})
	}
}

// TestParseMemoryRoundsThroughFloat64 is `int(float(digits_part) * unit)`:
// docker-py parses the digits as a BINARY64 before scaling them, so anything
// above 2^53 is rounded to the nearest representable value. Every expectation
// here is what the oracle's `parse_bytes` returns for the same string.
func TestParseMemoryRoundsThroughFloat64(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		// 2^53+1 is not representable: it rounds DOWN to 2^53.
		{"9007199254740993b", 9007199254740992},
		// 2^53+3 rounds UP, which an exact big-integer parse would not do.
		{"9007199254740995b", 9007199254740996},
		{"1234567890123456789b", 1234567890123456768},
		{"8796093022207999k", 9007199254740990976},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseMemory(tt.in); got != tt.want {
				t.Errorf("parseMemory(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// TestParseMemorySaturates records DIVERGENCES.md 68: Python's result is an
// arbitrary-precision int, so it posts a `Memory` the daemon cannot decode into
// its int64 and answers 400 to; the port saturates to the ceiling instead, and
// the daemon ACCEPTS that. `9223372036854775807b` is the smallest reachable
// input that differs — `float()` rounds it up to 2^63, one past the ceiling.
func TestParseMemorySaturates(t *testing.T) {
	if got := parseMemory("99999999999999999999999g"); got != math.MaxInt64 {
		t.Errorf("parseMemory(huge) = %d, want the int64 ceiling", got)
	}
	if got := parseMemory("9223372036854775807b"); got != math.MaxInt64 {
		t.Errorf("parseMemory(2^63-1) = %d, want the ceiling (Python posts 2^63)", got)
	}
	// `float()` of digits past ~1.8e308 is `inf` and `int(inf)` is Python's
	// OverflowError; the port saturates rather than growing an error return.
	if got := parseMemory(strings.Repeat("9", 400) + "b"); got != math.MaxInt64 {
		t.Errorf("parseMemory(inf) = %d, want the ceiling", got)
	}
}

// TestPortBindings is the inversion of `DockerMachine.create:231-236` plus
// docker-py's `convert_port_bindings`: the model keys by (host, protocol) and
// the payload keys by GUEST port, with the protocol travelling with the guest
// port.
func TestPortBindings(t *testing.T) {
	machine := newFixtureMachine(t, false)
	if _, _, err := machine.AddMeta("port", "3000:55/udp"); err != nil {
		t.Fatalf("AddMeta port: %v", err)
	}
	if _, _, err := machine.AddMeta("port", "8080:80"); err != nil {
		t.Fatalf("AddMeta port: %v", err)
	}

	bindings, exposed := portBindings(machine.Ports())

	want := nat.PortMap{
		"55/udp": []nat.PortBinding{{HostIP: "", HostPort: "3000"}},
		"80/tcp": []nat.PortBinding{{HostIP: "", HostPort: "8080"}},
	}
	if !reflect.DeepEqual(bindings, want) {
		t.Errorf("PortBindings = %v, want %v", bindings, want)
	}
	if len(exposed) != 2 {
		t.Errorf("ExposedPorts = %v, want one entry per binding", exposed)
	}
	for port := range want {
		if _, ok := exposed[port]; !ok {
			t.Errorf("ExposedPorts missing %q", port)
		}
	}
}

// TestPortBindingsDuplicateGuestPortLastWins is the lossy half of the
// inversion. The model keys `meta['ports']` by `(host, protocol)`, so two
// `port=` lines naming the same guest port both survive it; Python's
// `ports['%d/%s' % (guest, proto)] = host_port` is a DICT ASSIGNMENT and keeps
// only the last, and `convert_port_bindings` wraps that one value in a
// one-element list. Appending would bind BOTH host ports.
func TestPortBindingsDuplicateGuestPortLastWins(t *testing.T) {
	machine := newFixtureMachine(t, false)
	for _, spec := range []string{"3000:55/udp", "4000:55/udp"} {
		if _, _, err := machine.AddMeta("port", spec); err != nil {
			t.Fatalf("AddMeta port %q: %v", spec, err)
		}
	}

	bindings, exposed := portBindings(machine.Ports())

	want := nat.PortMap{"55/udp": []nat.PortBinding{{HostIP: "", HostPort: "4000"}}}
	if !reflect.DeepEqual(bindings, want) {
		t.Errorf("PortBindings = %v, want the LAST host port only", bindings)
	}
	if len(exposed) != 1 {
		t.Errorf("ExposedPorts = %v, want a single guest port", exposed)
	}
}

// TestPortBindingsAbsentIsNil is the NILABILITY row: Python passes
// `ports=None`, not `{}`, so neither key reaches the payload.
func TestPortBindingsAbsentIsNil(t *testing.T) {
	machine := newFixtureMachine(t, false)
	bindings, exposed := portBindings(machine.Ports())
	if bindings != nil || exposed != nil {
		t.Errorf("empty ports produced %v / %v, want nil / nil", bindings, exposed)
	}
}

// TestEnvListPreservesInsertionOrder: `Config.Env` order is visible in
// `docker inspect` and the model preserves lab.conf order.
func TestEnvListPreservesInsertionOrder(t *testing.T) {
	machine := newFixtureMachine(t, false)
	for _, env := range []string{"Z=last", "A=first", "M=middle"} {
		if _, _, err := machine.AddMeta("env", env); err != nil {
			t.Fatalf("AddMeta env: %v", err)
		}
	}

	got := envList(machine.Envs())
	want := []string{"Z=last", "A=first", "M=middle"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("envList = %q, want %q", got, want)
	}

	if envList(newFixtureMachine(t, false).Envs()) != nil {
		t.Error("an empty env map should produce a nil list, not an empty one")
	}
}

// TestUlimitList is ORDERING.tsv row 32: the list order is insertion order and
// is visible in `HostConfig.Ulimits`.
func TestUlimitList(t *testing.T) {
	machine := newFixtureMachine(t, false)
	for _, ulimit := range []string{"nofile=1024:2048", "nproc=100"} {
		if _, _, err := machine.AddMeta("ulimit", ulimit); err != nil {
			t.Fatalf("AddMeta ulimit: %v", err)
		}
	}

	got := ulimitList(machine.Ulimits())
	want := []*container.Ulimit{
		{Name: "nofile", Soft: 1024, Hard: 2048},
		// A ulimit with no explicit hard limit gets the soft one for both
		// (`model/Machine.py`), which is what the model already resolved.
		{Name: "nproc", Soft: 100, Hard: 100},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ulimitList = %+v, want %+v", got[0], want[0])
	}
}

// TestUlimitListEmptyIsAnEmptyList is the NILABILITY exception the other list
// builders of this file do NOT have: `DockerMachine.py:229` builds the ulimit
// list with a comprehension, so a device with no `ulimit=` option passes
// `ulimits=[]` — not `None` — and docker-py's `create_host_config` gates on
// `if ulimits is not None`, so the empty list is put in the payload and the
// daemon records `"Ulimits": []`.
//
// A nil slice here would serialize as `"Ulimits": null` (the SDK field carries
// no `omitempty`), which is the shape a live diff caught against a
// Python-created container.
func TestUlimitListEmptyIsAnEmptyList(t *testing.T) {
	got := ulimitList(newFixtureMachine(t, false).Ulimits())
	if got == nil {
		t.Fatal("ulimitList = nil for a device with no ulimits, want an empty list — docker-py sends []")
	}
	if len(got) != 0 {
		t.Fatalf("ulimitList = %+v, want an empty list", got)
	}

	// The observable is the request body, so assert the encoding rather than
	// the Go nilness alone (PORT_SPEC §9C).
	payload, err := json.Marshal(container.HostConfig{Resources: container.Resources{Ulimits: got}})
	if err != nil {
		t.Fatalf("marshal HostConfig: %v", err)
	}
	if !bytes.Contains(payload, []byte(`"Ulimits":[]`)) {
		t.Errorf("HostConfig encodes as %s, want it to carry \"Ulimits\":[]", payload)
	}
}

// TestBind is docker-py's `convert_volume_binds`, verified against 7.2.0's own
// output for the same input.
func TestBind(t *testing.T) {
	if got := bind("/h", "/shared", "rw"); got != "/h:/shared:rw" {
		t.Errorf("bind = %q", got)
	}
}

// TestCreateArgsNetworkModeOverride is the docker-py translation nobody
// expects: `_create_container_args` assigns `host_config_kwargs['network_mode']
// = network` AFTER copying the explicit `network_mode` across, so the literal
// "bridge" at `DockerMachine.py:369` is overwritten by the collision domain's
// Docker network name. `NetworkMode` is never "bridge".
func TestCreateArgsNetworkModeOverride(t *testing.T) {
	endpoint := &network.EndpointSettings{DriverOpts: map[string]string{"kathara.iface": "0"}}

	got := createArgs("kathara_user_pc1_h", "kathara/base", "pc1", false,
		"kathara_user_A_h", endpoint,
		nil, nil, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil)

	if string(got.HostConfig.NetworkMode) != "kathara_user_A_h" {
		t.Errorf("NetworkMode = %q, want the collision domain's network name", got.HostConfig.NetworkMode)
	}
	if got.Networking == nil || got.Networking.EndpointsConfig["kathara_user_A_h"] != endpoint {
		t.Errorf("EndpointsConfig = %v, want the endpoint keyed by the network name", got.Networking)
	}
}

// TestCreateArgsWithoutInterfaces is the other arm: no first network means
// `network=None`, docker-py's `if network:` is false, and the explicit
// `network_mode="none"` survives with no networking config at all.
func TestCreateArgsWithoutInterfaces(t *testing.T) {
	got := createArgs("kathara_user_pc1_h", "kathara/base", "pc1", false,
		"", nil,
		nil, nil, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil)

	if string(got.HostConfig.NetworkMode) != "none" {
		t.Errorf("NetworkMode = %q, want \"none\"", got.HostConfig.NetworkMode)
	}
	if got.Networking != nil {
		t.Errorf("Networking = %v, want nil", got.Networking)
	}
}

// TestCreateArgsCapabilities is EXPECTATIONS-docker.md §7 item 1: the exact
// capability set, or none at all when privileged — Docker grants everything
// then, and Python passes `cap_add=None`.
func TestCreateArgsCapabilities(t *testing.T) {
	unprivileged := createArgs("n", "i", "h", false, "", nil, nil, nil, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil)
	if !reflect.DeepEqual([]string(unprivileged.HostConfig.CapAdd), model.MachineCapabilities()) {
		t.Errorf("CapAdd = %v, want %v", unprivileged.HostConfig.CapAdd, model.MachineCapabilities())
	}
	if unprivileged.HostConfig.Privileged {
		t.Error("Privileged set on an unprivileged device")
	}

	privileged := createArgs("n", "i", "h", true, "", nil, nil, nil, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil)
	if privileged.HostConfig.CapAdd != nil {
		t.Errorf("CapAdd = %v on a privileged device, want nil", privileged.HostConfig.CapAdd)
	}
	if !privileged.HostConfig.Privileged {
		t.Error("Privileged not set on a privileged device")
	}
}

// TestCreateArgsInteractiveFlags pins the three flags every Kathará container
// carries: `tty=True`, `stdin_open=True` and `detach=True`. The third has no
// wire representation — it tells docker-py not to wait — so only the first two
// appear in the payload.
func TestCreateArgsInteractiveFlags(t *testing.T) {
	got := createArgs("n", "kathara/base", "pc1", false, "", nil, nil, nil, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil)
	if !got.Config.Tty || !got.Config.OpenStdin {
		t.Errorf("Tty=%v OpenStdin=%v, want both true", got.Config.Tty, got.Config.OpenStdin)
	}
	if got.Config.Hostname != "pc1" {
		t.Errorf("Hostname = %q, want the device name", got.Config.Hostname)
	}
	if got.Config.Image != "kathara/base" {
		t.Errorf("Image = %q", got.Config.Image)
	}
}

// TestCreateArgsVolumes is the docker-py translation that shows up in
// `docker inspect .Config.Volumes`: `_create_container_args` passes the SAME
// `volumes` dict twice — once as `HostConfig.Binds` and once as
// `create_kwargs['volumes'] = [v.get('bind') for v in volumes.values()]`, which
// `ContainerConfig` turns into `{"/shared": {}, …}`. Posting only the binds
// leaves `Config.Volumes` null where Python fills it, on essentially every
// deployed container (`shared_mount` defaults on).
func TestCreateArgsVolumes(t *testing.T) {
	binds := []string{"/h/shared:/shared:rw", "/home/u:/hosthome:rw"}
	mountPoints := []string{"/shared", "/hosthome"}

	got := createArgs("n", "kathara/base", "pc1", false, "", nil,
		nil, nil, 0, 0, nil, nil, binds, mountPoints, nil, nil, nil, nil)

	if !reflect.DeepEqual([]string(got.HostConfig.Binds), binds) {
		t.Errorf("Binds = %q, want %q", got.HostConfig.Binds, binds)
	}
	want := map[string]struct{}{"/shared": {}, "/hosthome": {}}
	if !maps.Equal(got.Config.Volumes, want) {
		t.Errorf("Config.Volumes = %v, want %v", got.Config.Volumes, want)
	}

	// `create_kwargs['volumes']` is set only `if volumes:`, so a device with no
	// mounts posts `'Volumes': None` — a nil map, not an empty one.
	none := createArgs("n", "kathara/base", "pc1", false, "", nil,
		nil, nil, 0, 0, nil, nil, nil, nil, nil, nil, nil, nil)
	if none.Config.Volumes != nil {
		t.Errorf("Config.Volumes = %v with no binds, want nil", none.Config.Volumes)
	}
}

// TestNetworkCreateOptions is EXPECTATIONS-docker.md §3.2's `test_create`:
// the plugin driver and a NULL IPAM driver, which is what makes a collision
// domain a pure L2 segment with no addresses handed out.
func TestNetworkCreateOptions(t *testing.T) {
	labels := map[string]string{"name": "A", "app": "kathara"}
	got := networkCreateOptions("kathara/katharanp_vde:amd64", labels)

	if got.Driver != "kathara/katharanp_vde:amd64" {
		t.Errorf("Driver = %q", got.Driver)
	}
	if got.IPAM == nil || got.IPAM.Driver != "null" {
		t.Errorf("IPAM = %v, want the null driver", got.IPAM)
	}
	if !maps.Equal(got.Labels, labels) {
		t.Errorf("Labels = %v, want %v", got.Labels, labels)
	}
}
