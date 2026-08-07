package docker

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/model"
)

// newFixtureMachine builds the Python suite's fixture device: `Lab("Default
// scenario")` with one device `pc1`, optionally IPv6-enabled and carrying the
// given `sysctl` metas.
func newFixtureMachine(t *testing.T, ipv6 bool, sysctls ...string) *model.Machine {
	t.Helper()

	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	if ipv6 {
		if _, _, err := machine.AddMeta("ipv6", "true"); err != nil {
			t.Fatalf("AddMeta ipv6: %v", err)
		}
	}
	for _, sysctl := range sysctls {
		if _, _, err := machine.AddMeta("sysctl", sysctl); err != nil {
			t.Fatalf("AddMeta sysctl %q: %v", sysctl, err)
		}
	}
	return machine
}

// attach adds an interface and returns it, which is what `_create_driver_opt`
// takes.
func attach(t *testing.T, machine *model.Machine, linkName string, number int, mac string) model.Interface {
	t.Helper()

	link := machine.Lab.GetOrNewLink(linkName)
	iface, err := machine.AddInterface(link, model.AddInterfaceOptions{Number: model.InterfaceNumber(number), MAC: mac})
	if err != nil {
		t.Fatalf("AddInterface: %v", err)
	}
	return iface
}

// TestCreateDriverOpt is EXPECTATIONS-docker.md §1.5 — the six-row
// `_create_driver_opt` matrix, which is the wiring contract with the network
// plugin and the primary Layer A assertion surface (SYNTHESIS §1.2).
//
// The endpoint-sysctls value is compared as a SORTED LIST rather than as a
// string, exactly as the Python suite's own `order_driver_opt` helper does:
// Python joins a set and its element order is hash-randomised per run. The
// accepted ruling on OQ-8 is that the port emits a canonical sorted order, so
// the string equality is asserted separately in
// [TestCreateDriverOptSortsCanonically].
func TestCreateDriverOpt(t *testing.T) {
	tests := []struct {
		name          string
		engineVersion string
		ipv6          bool
		sysctls       []string
		mac           string
		wantOpts      map[string]string
		wantSysctls   []string
	}{
		{
			name:          "engine >= 27, ipv6 off",
			engineVersion: "27.0.0",
			wantOpts:      map[string]string{"kathara.iface": "0", "kathara.link": "A"},
			wantSysctls: []string{
				"net.ipv4.conf.IFNAME.rp_filter=0",
				"net.ipv6.conf.IFNAME.disable_ipv6=1",
			},
		},
		{
			name:          "an explicit MAC adds kathara.mac_addr",
			engineVersion: "27.0.0",
			mac:           "00:00:00:00:00:01",
			wantOpts: map[string]string{
				"kathara.iface": "0", "kathara.link": "A", "kathara.mac_addr": "00:00:00:00:00:01",
			},
			wantSysctls: []string{
				"net.ipv4.conf.IFNAME.rp_filter=0",
				"net.ipv6.conf.IFNAME.disable_ipv6=1",
			},
		},
		{
			name:          "ipv6 on swaps the disable for the enable pair",
			engineVersion: "27.0.0",
			ipv6:          true,
			wantOpts:      map[string]string{"kathara.iface": "0", "kathara.link": "A"},
			wantSysctls: []string{
				"net.ipv4.conf.IFNAME.rp_filter=0",
				"net.ipv6.conf.IFNAME.disable_ipv6=0",
				"net.ipv6.conf.IFNAME.forwarding=1",
			},
		},
		{
			name:          "a device sysctl for this interface is folded in, IFNAME-templated",
			engineVersion: "27.0.0",
			sysctls:       []string{"net.ipv6.neigh.eth0.anycast_delay=50"},
			wantOpts:      map[string]string{"kathara.iface": "0", "kathara.link": "A"},
			wantSysctls: []string{
				"net.ipv4.conf.IFNAME.rp_filter=0",
				"net.ipv6.conf.IFNAME.disable_ipv6=1",
				"net.ipv6.neigh.IFNAME.anycast_delay=50",
			},
		},
		{
			name:          "engine 25 gets no endpoint sysctls at all",
			engineVersion: "25.0.0",
			wantOpts:      map[string]string{"kathara.iface": "0", "kathara.link": "A"},
		},
		{
			name:          "the MAC opt is independent of the engine version",
			engineVersion: "25.0.0",
			mac:           "00:00:00:00:00:01",
			wantOpts: map[string]string{
				"kathara.iface": "0", "kathara.link": "A", "kathara.mac_addr": "00:00:00:00:00:01",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := newFixtureMachine(t, tt.ipv6, tt.sysctls...)
			iface := attach(t, machine, "A", 0, tt.mac)

			got, err := createDriverOpt(tt.engineVersion, machine, iface, ifaceSysctls(machine.Sysctls(), iface.Number))
			if err != nil {
				t.Fatalf("createDriverOpt: %v", err)
			}

			gotSysctls, hasSysctls := got[driverOptSysctls]
			delete(got, driverOptSysctls)
			if !maps.Equal(got, tt.wantOpts) {
				t.Errorf("driver opts = %v, want %v", got, tt.wantOpts)
			}

			if tt.wantSysctls == nil {
				if hasSysctls {
					t.Errorf("endpoint sysctls present (%q) on an engine that cannot take them", gotSysctls)
				}
				return
			}
			if !hasSysctls {
				t.Fatalf("endpoint sysctls missing, want %q", tt.wantSysctls)
			}
			if parts := strings.Split(gotSysctls, ","); !reflect.DeepEqual(parts, tt.wantSysctls) {
				t.Errorf("endpoint sysctls = %q, want %q", parts, tt.wantSysctls)
			}
		})
	}
}

// TestCreateDriverOptSortsCanonically is the OQ-8 ruling: Python joins a
// `set`, whose order changes between runs of the same command
// (ORDERING.tsv row 40, "KNOWN nondeterministic site"); the port emits a
// canonical SORTED order, and the same input always produces the same string.
func TestCreateDriverOptSortsCanonically(t *testing.T) {
	machine := newFixtureMachine(t, true,
		"net.ipv6.neigh.eth0.anycast_delay=50",
		"net.ipv4.conf.eth0.arp_filter=1",
	)
	iface := attach(t, machine, "A", 0, "")

	got, err := createDriverOpt("27.0.0", machine, iface, ifaceSysctls(machine.Sysctls(), iface.Number))
	if err != nil {
		t.Fatalf("createDriverOpt: %v", err)
	}

	want := strings.Join([]string{
		"net.ipv4.conf.IFNAME.arp_filter=1",
		"net.ipv4.conf.IFNAME.rp_filter=0",
		"net.ipv6.conf.IFNAME.disable_ipv6=0",
		"net.ipv6.conf.IFNAME.forwarding=1",
		"net.ipv6.neigh.IFNAME.anycast_delay=50",
	}, ",")
	if got[driverOptSysctls] != want {
		t.Errorf("endpoint sysctls = %q, want %q", got[driverOptSysctls], want)
	}
}

// TestCreateDriverOptDedupes keeps the other half of the Python `set`: a device
// sysctl that renders to exactly the baseline entry collapses into it rather
// than appearing twice.
func TestCreateDriverOptDedupes(t *testing.T) {
	machine := newFixtureMachine(t, false, "net.ipv4.conf.eth0.rp_filter=0")
	iface := attach(t, machine, "A", 0, "")

	got, err := createDriverOpt("27.0.0", machine, iface, ifaceSysctls(machine.Sysctls(), iface.Number))
	if err != nil {
		t.Fatalf("createDriverOpt: %v", err)
	}
	if got[driverOptSysctls] != "net.ipv4.conf.IFNAME.rp_filter=0,net.ipv6.conf.IFNAME.disable_ipv6=1" {
		t.Errorf("endpoint sysctls = %q, want the duplicate collapsed", got[driverOptSysctls])
	}
}

// TestCreateDriverOptEmptyMACIsAbsent is SYNTHESIS §1.3's last bullet: the
// guard is `if interface.mac_address:`, so "" behaves exactly like None and the
// plugin derives an address.
func TestCreateDriverOptEmptyMACIsAbsent(t *testing.T) {
	machine := newFixtureMachine(t, false)
	iface := attach(t, machine, "A", 0, "")

	got, err := createDriverOpt("27.0.0", machine, iface, nil)
	if err != nil {
		t.Fatalf("createDriverOpt: %v", err)
	}
	if _, ok := got[driverOptMacAddr]; ok {
		t.Errorf("an empty MAC produced a kathara.mac_addr opt: %v", got)
	}
}

// TestIfaceSysctls covers `_get_iface_sysctls` including its two reproduced
// bugs (docker-backend.md gotcha 5).
func TestIfaceSysctls(t *testing.T) {
	tests := []struct {
		name    string
		sysctls []string
		number  int
		want    []string
	}{
		{
			name:    "a matching conf sysctl is templated",
			sysctls: []string{"net.ipv4.conf.eth0.rp_filter=1"},
			number:  0,
			want:    []string{"net.ipv4.conf.IFNAME.rp_filter=1"},
		},
		{
			name:    "a neigh sysctl matches too",
			sysctls: []string{"net.ipv6.neigh.eth1.anycast_delay=50"},
			number:  1,
			want:    []string{"net.ipv6.neigh.IFNAME.anycast_delay=50"},
		},
		{
			name:    "another interface's sysctl is not picked up",
			sysctls: []string{"net.ipv4.conf.eth2.rp_filter=1"},
			number:  0,
			want:    nil,
		},
		{
			name:    "a non-interface sysctl is not picked up",
			sysctls: []string{"net.ipv4.ip_forward=1"},
			number:  0,
			want:    nil,
		},
		{
			// BUG (reproduced): the match is `re.match` with no end anchor, so
			// the pattern built for interface 1 also matches eth10 — and the
			// rewrite is `re.sub`, which replaces every occurrence, so eth10
			// comes back as IFNAME0: a setting for an interface that does not
			// exist.
			name:    "eth10 leaks into interface 1 and is corrupted to IFNAME0",
			sysctls: []string{"net.ipv4.conf.eth10.rp_filter=1"},
			number:  1,
			want:    []string{"net.ipv4.conf.IFNAME0.rp_filter=1"},
		},
		{
			name:    "results are sorted, where Python returns a set",
			sysctls: []string{"net.ipv6.neigh.eth0.z=1", "net.ipv4.conf.eth0.a=2"},
			number:  0,
			want:    []string{"net.ipv4.conf.IFNAME.a=2", "net.ipv6.neigh.IFNAME.z=1"},
		},
		{
			// A sysctl value that `str.isnumeric()` accepts is stored as an int
			// and rendered by Python's `str()`; a non-numeric one stays a
			// string. Both reach the opt as their `str()`.
			name:    "an int value renders as its decimal",
			sysctls: []string{"net.ipv4.conf.eth0.rp_filter=0"},
			number:  0,
			want:    []string{"net.ipv4.conf.IFNAME.rp_filter=0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			machine := newFixtureMachine(t, false, tt.sysctls...)
			got := ifaceSysctls(machine.Sysctls(), tt.number)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ifaceSysctls = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIfaceSysctlREAcceptsTheCommaClass pins the `[4,6]` bug in the
// container-side filter: it is a character CLASS containing a literal comma,
// not the alternation the author meant, so `net.ipv,.conf.eth0.x` matches and
// is stripped from the container sysctls on a new engine.
func TestIfaceSysctlREAcceptsTheCommaClass(t *testing.T) {
	for _, key := range []string{
		"net.ipv4.conf.eth0.rp_filter",
		"net.ipv6.neigh.eth12.anycast_delay",
		"net.ipv,.conf.eth0.rp_filter",
	} {
		if !ifaceSysctlRE.MatchString(key) {
			t.Errorf("ifaceSysctlRE did not match %q", key)
		}
	}
	for _, key := range []string{
		"net.ipv4.ip_forward",
		"net.ipv5.conf.eth0.rp_filter",
		"net.ipv4.conf.all.rp_filter",
		"xnet.ipv4.conf.eth0.rp_filter",
	} {
		if ifaceSysctlRE.MatchString(key) {
			t.Errorf("ifaceSysctlRE matched %q", key)
		}
	}
}
