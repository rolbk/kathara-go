package docker

import (
	"errors"
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// newTestContainer builds the `Container` value a listing would have produced:
// an inspect response with the labels and host config a test needs.
func newTestContainer(deviceName string, hostConfig *container.HostConfig) *Container {
	if hostConfig == nil {
		hostConfig = &container.HostConfig{}
	}
	return &Container{
		ID: "cid-" + deviceName,
		Attrs: container.InspectResponse{
			ContainerJSONBase: &container.ContainerJSONBase{
				Name:       "/kathara_user_" + deviceName + "_" + fixtureHash,
				Image:      "sha256:deadbeef",
				State:      &container.State{Status: "running"},
				HostConfig: hostConfig,
			},
			Config: &container.Config{
				Image: "test_image",
				Labels: map[string]string{
					"name":     deviceName,
					"lab_hash": fixtureHash,
					"user":     "user",
					"app":      "kathara",
					"shell":    "/bin/bash",
				},
			},
			NetworkSettings: &container.NetworkSettings{
				Networks: map[string]*network.EndpointSettings{},
			},
		},
	}
}

func endpoint(iface, link, mac, sysctls string) *network.EndpointSettings {
	opts := map[string]string{"kathara.iface": iface, "kathara.link": link}
	if mac != "" {
		opts["kathara.mac_addr"] = mac
	}
	if sysctls != "" {
		opts["com.docker.network.endpoint.sysctls"] = sysctls
	}
	return &network.EndpointSettings{DriverOpts: opts}
}

func newTestNetwork(dockerName, linkName string) *Network {
	return &Network{
		ID: "nid-" + dockerName,
		Attrs: network.Inspect{
			Name:   dockerName,
			Labels: map[string]string{"name": linkName, "app": "kathara", "external": ""},
		},
	}
}

func TestApplyContainerMetas(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	device, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}

	c := newTestContainer("pc1", &container.HostConfig{
		Privileged: true,
		Resources: container.Resources{
			Memory:   67108864,
			NanoCPUs: 100000000,
		},
		PortBindings: nat.PortMap{
			"55/udp": []nat.PortBinding{{HostPort: "3000"}},
		},
		Sysctls: map[string]string{"sysctl.test": "0"},
	})
	c.Attrs.Config.Env = []string{"test=path"}

	if err := applyContainerMetas(device, c); err != nil {
		t.Fatalf("applyContainerMetas: %v", err)
	}

	if !device.IsPrivileged() {
		t.Error("privileged was not rebuilt")
	}
	if got := device.Meta.Image.String(); got != "test_image" {
		t.Errorf("image = %q", got)
	}
	if got := device.Meta.Shell.String(); got != "/bin/bash" {
		t.Errorf("shell = %q", got)
	}

	// `int(Memory / (1024 ** 2))` with an always-uppercase M suffix, whatever
	// the user originally wrote.
	if got := device.Meta.Mem.String(); got != "64M" {
		t.Errorf("mem = %q, want \"64M\"", got)
	}

	// `NanoCpus / 1e9`, a FLOAT, stored under the meta name `cpu` — not
	// `cpus`, which is the name the model reads. So the limit does not survive
	// a round trip, and the value lands in Extras.
	cpu, ok := device.Meta.Extras.Get("cpu")
	if !ok {
		t.Fatal("cpu meta missing")
	}
	if cpu.Kind() != model.KindFloat || cpu.Value().(float64) != 0.1 {
		t.Errorf("cpu = %v (%v), want the float 0.1", cpu.Value(), cpu.Kind())
	}
	if device.Meta.CPUs.IsSet() {
		t.Error("the `cpus` meta the model reads was set; Python writes `cpu`")
	}

	if got, _ := device.Envs().Get("test"); got != "path" {
		t.Errorf("env test = %q", got)
	}

	// This implementation map is INVERTED: (host, protocol) keys a guest port.
	guest, ok := device.Ports().Get(model.PortKey{HostPort: 3000, Protocol: "udp"})
	if !ok || guest != 55 {
		t.Errorf("ports = %v, want (3000, udp) -> 55", device.Ports().Entries())
	}

	// The sysctls container is ASSIGNED over, bypassing `add_meta` and
	// therefore the `net.*` namespace validation — which is why a key the
	// validator would reject survives here.
	sysctl, ok := device.Sysctls().Get("sysctl.test")
	if !ok || sysctl.String() != "0" {
		t.Errorf("sysctls = %v", device.Sysctls().Entries())
	}
}

// TestApplyContainerMetasZeroMeansAbsent is
// `test_get_lab_from_api_lab_name_empty_meta`: a zero Memory or NanoCpus means
// the limit is ABSENT, not zero.
func TestApplyContainerMetasZeroMeansAbsent(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	device, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}

	if err := applyContainerMetas(device, newTestContainer("pc1", nil)); err != nil {
		t.Fatalf("applyContainerMetas: %v", err)
	}

	if device.Meta.Mem.IsSet() {
		t.Errorf("mem = %q, want absent for a zero Memory", device.Meta.Mem.String())
	}
	if _, ok := device.Meta.Extras.Get("cpu"); ok {
		t.Error("cpu meta set for a zero NanoCpus")
	}
	if device.Ports().Len() != 0 {
		t.Errorf("ports = %v, want none", device.Ports().Entries())
	}
}

// TestApplyContainerMetasEmptyPortDataIsAnIndexError: `port_data[0]["HostPort"]`
// on an empty binding list is `IndexError: list index out of range`, which
// carries no index — a KeyError would name one and be the wrong class.
func TestApplyContainerMetasEmptyPortDataIsAnIndexError(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	device, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}

	c := newTestContainer("pc1", &container.HostConfig{
		PortBindings: nat.PortMap{"55/udp": []nat.PortBinding{}},
	})

	// `model` has no IndexError sentinel — the three it exposes are the classes
	// `model` itself raises — so the class is read off the carrier.
	var typed *model.PyRuntimeError
	if err := applyContainerMetas(device, c); !errors.As(err, &typed) {
		t.Fatalf("empty port data gave %v, want the Python exception behaviour", err)
	}
	if typed.Class != "IndexError" || typed.Msg != "list index out of range" {
		t.Errorf("exception = %s: %s, want IndexError: list index out of range", typed.Class, typed.Msg)
	}
}

// TestApplyContainerInterfacesMissingBridgedIfaceLabel:
// `int(container.labels['bridged_iface'])` INDEXES the label dict, so an absent
// label is `KeyError: 'bridged_iface'` — only a present-but-malformed one is
// `int()`'s ValueError.
func TestApplyContainerInterfacesMissingBridgedIfaceLabel(t *testing.T) {
	m := &Manager{}
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	device, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("GetOrNewMachine: %v", err)
	}

	c := newTestContainer("pc1", nil)
	c.Attrs.NetworkSettings.Networks["bridge"] = &network.EndpointSettings{}

	err = m.applyContainerInterfaces(lab, device, c, map[string]*Network{})
	if !errors.Is(err, model.ErrPyKeyError) {
		t.Fatalf("a missing bridged_iface label gave %v, want a KeyError", err)
	}

	// Present but malformed is the other class.
	c.Attrs.Config.Labels["bridged_iface"] = "eth0"
	if err := m.applyContainerInterfaces(lab, device, c, map[string]*Network{}); !errors.Is(err, kerrors.ErrValue) {
		t.Errorf("a malformed bridged_iface label gave %v, want the Value bucket", err)
	}
}

// TestApplyContainerInterfaces is the network half of `get_lab_from_api`:
// DriverOpts back into an interface, the bridge endpoint into `bridged`, and
// the `none` endpoint into "no interfaces at all".
func TestApplyContainerInterfaces(t *testing.T) {
	t.Run("a kathara endpoint becomes an interface", func(t *testing.T) {
		m := &Manager{}
		lab := model.NewLab("Default scenario", model.DefaultDefaults())
		device, err := lab.GetOrNewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}

		c := newTestContainer("pc1", nil)
		c.Attrs.NetworkSettings.Networks["kathara_user_A_h"] = endpoint("0", "A", "00:00:00:00:00:01", "")
		networks := map[string]*Network{"kathara_user_A_h": newTestNetwork("kathara_user_A_h", "A")}

		if err := m.applyContainerInterfaces(lab, device, c, networks); err != nil {
			t.Fatalf("applyContainerInterfaces: %v", err)
		}

		iface, ok := device.Interface(0)
		if !ok {
			t.Fatalf("interface 0 missing; slots = %v", device.Interfaces())
		}
		if iface.Link.Name != "A" {
			t.Errorf("interface 0 link = %q, want A", iface.Link.Name)
		}
		if iface.MAC != "00:00:00:00:00:01" {
			t.Errorf("interface 0 MAC = %q", iface.MAC)
		}
		if _, ok := networkOf(iface.Link.APIObject); !ok {
			t.Error("the collision domain did not get its api_object")
		}
	})

	t.Run("the bridge endpoint sets bridged and is not a collision domain", func(t *testing.T) {
		m := &Manager{}
		lab := model.NewLab("Default scenario", model.DefaultDefaults())
		device, err := lab.GetOrNewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}

		c := newTestContainer("pc1", nil)
		c.Attrs.Config.Labels["bridged_iface"] = "1"
		c.Attrs.NetworkSettings.Networks["bridge"] = &network.EndpointSettings{}
		c.Attrs.NetworkSettings.Networks["kathara_user_A_h"] = endpoint("0", "A", "", "")
		networks := map[string]*Network{"kathara_user_A_h": newTestNetwork("kathara_user_A_h", "A")}

		if err := m.applyContainerInterfaces(lab, device, c, networks); err != nil {
			t.Fatalf("applyContainerInterfaces: %v", err)
		}

		if !device.IsBridged() {
			t.Error("bridged was not detected from the bridge endpoint")
		}
		if got, ok := bridgedIfaceNumber(device); !ok || got != 1 {
			t.Errorf("bridged_iface = (%d, %v), want (1, true)", got, ok)
		}
		if lab.HasLink("bridge") {
			t.Error("the docker bridge became a collision domain")
		}
	})

	t.Run("the none endpoint means no interfaces at all", func(t *testing.T) {
		m := &Manager{}
		lab := model.NewLab("Default scenario", model.DefaultDefaults())
		device, err := lab.GetOrNewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}

		c := newTestContainer("pc1", nil)
		c.Attrs.NetworkSettings.Networks["none"] = &network.EndpointSettings{}

		if err := m.applyContainerInterfaces(lab, device, c, nil); err != nil {
			t.Fatalf("applyContainerInterfaces: %v", err)
		}
		if len(device.Interfaces()) != 0 {
			t.Errorf("interfaces = %v, want none", device.Interfaces())
		}
		if device.IsBridged() {
			t.Error("bridged was set for a device on `none`")
		}
	})

	t.Run("an endpoint on an unlisted network is a KeyError", func(t *testing.T) {
		m := &Manager{}
		lab := model.NewLab("Default scenario", model.DefaultDefaults())
		device, err := lab.GetOrNewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}

		c := newTestContainer("pc1", nil)
		c.Attrs.NetworkSettings.Networks["someone_elses_net"] = endpoint("0", "A", "", "")

		err = m.applyContainerInterfaces(lab, device, c, map[string]*Network{})
		if !errors.Is(err, model.ErrPyKeyError) {
			t.Errorf("err = %v, want the KeyError Python's lab_networks[...] raises", err)
		}
	})
}

// TestApplyEndpointSysctls is the mirror of [ifaceSysctls]: each comma-separated
// entry becomes a `sysctl` meta with the literal IFNAME token replaced by the
// endpoint's real interface name.
func TestApplyEndpointSysctls(t *testing.T) {
	machine := newFixtureMachine(t, false)
	opts := map[string]string{
		"com.docker.network.endpoint.sysctls": "net.ipv4.conf.IFNAME.rp_filter=0,net.ipv6.conf.IFNAME.disable_ipv6=1",
	}

	if err := applyEndpointSysctls(machine, opts, 2); err != nil {
		t.Fatalf("applyEndpointSysctls: %v", err)
	}

	if got, ok := machine.Sysctls().Get("net.ipv4.conf.eth2.rp_filter"); !ok || got.String() != "0" {
		t.Errorf("sysctls = %v, want the IFNAME token expanded to eth2", machine.Sysctls().Entries())
	}
	if _, ok := machine.Sysctls().Get("net.ipv6.conf.eth2.disable_ipv6"); !ok {
		t.Errorf("sysctls = %v, missing the second entry", machine.Sysctls().Entries())
	}
}

// TestApplyEndpointSysctlsIsANoOpWithoutTheOpt: engine < 27 sets no endpoint
// sysctls, and the branch is guarded by the key's presence.
func TestApplyEndpointSysctlsIsANoOpWithoutTheOpt(t *testing.T) {
	machine := newFixtureMachine(t, false)
	if err := applyEndpointSysctls(machine, map[string]string{"kathara.iface": "0"}, 0); err != nil {
		t.Fatalf("applyEndpointSysctls: %v", err)
	}
	if machine.Sysctls().Len() != 0 {
		t.Errorf("sysctls = %v, want none", machine.Sysctls().Entries())
	}
}

func TestSortByIfaceOptIsLexicographic(t *testing.T) {
	endpoints := map[string]*network.EndpointSettings{
		"net2":  endpoint("2", "B", "", ""),
		"net10": endpoint("10", "C", "", ""),
		"net0":  endpoint("0", "A", "", ""),
	}

	got := sortByIfaceOpt(endpoints)
	if !reflect.DeepEqual(got, []string{"net0", "net10", "net2"}) {
		t.Errorf("sortByIfaceOpt = %q, want the lexicographic order (\"10\" < \"2\")", got)
	}
}

// TestSortByIfaceOptBreaksTiesByName keeps the result independent of Go's map
// iteration, which Python got for free from a list of dict items.
func TestSortByIfaceOptBreaksTiesByName(t *testing.T) {
	endpoints := map[string]*network.EndpointSettings{
		"zzz": endpoint("0", "Z", "", ""),
		"aaa": endpoint("0", "A", "", ""),
	}
	for range 20 {
		if got := sortByIfaceOpt(endpoints); !reflect.DeepEqual(got, []string{"aaa", "zzz"}) {
			t.Fatalf("sortByIfaceOpt = %q, want a stable tie-break", got)
		}
	}
}

// TestIfaceNumberOf reproduces the order of `get_lab_from_api:755`: the
// `int(...["kathara.iface"])` runs before the `is not None` guard two lines
// below, so a nil DriverOpts raises before the guard is reached.
func TestIfaceNumberOf(t *testing.T) {
	got, err := ifaceNumberOf(endpoint("3", "A", "", ""))
	if err != nil || got != 3 {
		t.Fatalf("ifaceNumberOf = (%d, %v), want (3, nil)", got, err)
	}

	if _, err := ifaceNumberOf(&network.EndpointSettings{}); !errors.Is(err, model.ErrPyTypeError) {
		t.Errorf("nil DriverOpts gave %v, want the subscript TypeError", err)
	}
	if _, err := ifaceNumberOf(nil); !errors.Is(err, model.ErrPyTypeError) {
		t.Errorf("nil endpoint gave %v, want the subscript TypeError", err)
	}
	if _, err := ifaceNumberOf(&network.EndpointSettings{DriverOpts: map[string]string{}}); !errors.Is(err, model.ErrPyKeyError) {
		t.Errorf("a DriverOpts without kathara.iface gave %v, want a KeyError", err)
	}
}

// TestIfaceNumberOfIsPythonsInt is [pyInt], not `strconv.Atoi`: CPython's
// `int()` strips its own whitespace table, takes a leading sign and
// underscores between digits, and its failure message is
// `invalid literal for int() with base 10: '…'`.
func TestIfaceNumberOfIsPythonsInt(t *testing.T) {
	for _, in := range []string{" 3 ", "+3", "0_3", "03"} {
		got, err := ifaceNumberOf(endpoint(in, "A", "", ""))
		if err != nil || got != 3 {
			t.Errorf("ifaceNumberOf(%q) = (%d, %v), want (3, nil)", in, got, err)
		}
	}

	_, err := ifaceNumberOf(endpoint("eth0", "A", "", ""))
	if !errors.Is(err, kerrors.ErrValue) {
		t.Fatalf("a non-numeric kathara.iface gave %v, want the Value bucket", err)
	}
	if want := `invalid literal for int() with base 10: 'eth0'`; err.Error() != want {
		t.Errorf("message = %q, want CPython's %q", err, want)
	}
}

func TestIfaceOptNumberIsTotal(t *testing.T) {
	if got := ifaceOptNumber(endpoint("7", "A", "", "")); got != 7 {
		t.Errorf("ifaceOptNumber = %d, want 7", got)
	}
	if got := ifaceOptNumber(nil); got != 0 {
		t.Errorf("ifaceOptNumber(nil) = %d, want the 0 sentinel", got)
	}
	if got := ifaceOptNumber(endpoint("eth0", "A", "", "")); got != 0 {
		t.Errorf("ifaceOptNumber(unparsable) = %d, want the 0 sentinel", got)
	}
}

// TestPyIntReproducesCPython pins the shared helper the label and payload
// conversions use, message included.
func TestPyIntReproducesCPython(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{
		{"3", 3}, {" 3 ", 3}, {"+3", 3}, {"-3", -3}, {"1_0", 10}, {"007", 7},
	} {
		got, err := pyInt(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("pyInt(%q) = (%d, %v), want (%d, nil)", tt.in, got, err, tt.want)
		}
	}

	for _, in := range []string{"", "3.5", "0x10", "1__0"} {
		_, err := pyInt(in)
		if !errors.Is(err, kerrors.ErrValue) {
			t.Errorf("pyInt(%q) = %v, want the Value bucket", in, err)
		}
	}
}

// TestPyBoolString is Python's `str(bool)`, which `add_meta("privileged", …)`
// then feeds to `strtobool`.
func TestPyBoolString(t *testing.T) {
	if pyBoolString(true) != "True" || pyBoolString(false) != "False" {
		t.Error("pyBoolString does not produce Python's capitalised spellings")
	}
}

// TestReconstructedLabName pins the placeholder a hash-addressed
// reconstruction gets, and the fact that the requested hash REPLACES the one
// the placeholder name would produce.
func TestReconstructedLabName(t *testing.T) {
	lab := model.NewLab(reconstructedLabName, model.DefaultDefaults())
	if lab.Name() != "reconstructed_lab" {
		t.Errorf("name = %q", lab.Name())
	}
	lab.Hash = "forced"
	if lab.Hash != "forced" {
		t.Error("assigning Hash directly did not stick")
	}
}
