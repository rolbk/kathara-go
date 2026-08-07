// This file is `DockerMachine._get_iface_sysctls` (:429) and
// `DockerMachine._create_driver_opt` (:448): the per-endpoint DriverOpts map,
// which is the wiring contract with the network plugin and the primary Layer A
// assertion surface (SYNTHESIS §1.2, GOLDEN_CANDIDATES).
//
// It carries two reproduced bugs and one sanctioned divergence, all three
// marked at their line.

package docker

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/model"
)

// The four DriverOpts keys of the frozen schema (PORT_SPEC §0.4).
const (
	driverOptIface    = "kathara.iface"
	driverOptLink     = "kathara.link"
	driverOptMacAddr  = "kathara.mac_addr"
	driverOptSysctls  = "com.docker.network.endpoint.sysctls"
	ifnamePlaceholder = "IFNAME"
)

// engineEndpointSysctls is the engine version at and above which per-endpoint
// sysctls exist (`DockerMachine.py:297,460`). Below it the same settings go
// into the container's own sysctl map, and interface-scoped ones cannot be set
// per endpoint at all.
const engineEndpointSysctls = "27.0.0"

// engineFirstIfaceRPFilter is the engine version BELOW which `create` adds
// `net.ipv4.conf.eth0.rp_filter=0` to the container sysctls
// (`DockerMachine.py:279`). Note the gap: 26.x is below neither fork, so it
// gets neither the container sysctl nor the endpoint ones.
const engineFirstIfaceRPFilter = "26.0.0"

// ifaceSysctlRE is `IFACE_SYSCTL_RE` (`DockerMachine.py:39`), used with
// `re.match` — anchored at the start only — to strip interface-scoped sysctls
// out of the container's sysctl map on engine >= 27.
//
// Two translations, both required for parity:
//
//   - `[4,6]` is a character CLASS containing `4`, `,` and `6`, not the
//     alternation the author meant. `net.ipv,.conf.eth0` matches. Reproduced
//     (docker-backend.md gotcha 5).
//   - Python's `\d` is Unicode for a `str` pattern, so an Arabic-Indic digit
//     counts; RE2's `\d` is ASCII. Spelled `[\p{Nd}]` (RULINGS.md OQ-14a).
var ifaceSysctlRE = regexp.MustCompile(`^net\.ipv[4,6]\.(conf|neigh)\.eth[\p{Nd}]+`)

// ifaceSysctls is `_get_iface_sysctls` (`DockerMachine.py:429`): the device's
// own sysctls that name THIS interface, rewritten with the literal `IFNAME`
// token the engine expands per endpoint, as `key=value` strings.
//
// Python returns a `set`; this returns a sorted slice, which is the OQ-8
// ruling (see [createDriverOpt]) and ORDERING.tsv row 39's "!! return sorted
// []string".
//
// Two bugs are reproduced verbatim (docker-backend.md gotcha 5):
//
//   - The match is `re.match` with no end anchor, so the pattern built for
//     interface 1 also matches `net.ipv4.conf.eth10.rp_filter` and
//     `net.ipv4.conf.eth1x.y`.
//   - The rewrite is `re.sub`, which replaces EVERY occurrence, so that same
//     `eth10` sysctl comes back as `net.ipv4.conf.IFNAME0.rp_filter` — a
//     setting for an interface that does not exist.
//
// The value is interpolated with an f-string, i.e. Python's `str()`, which is
// [model.Scalar.String]: an int sysctl renders as its decimal and a string one
// verbatim.
func ifaceSysctls(sysctls *model.OrderedMap[string, model.Scalar], interfaceNum int) []string {
	num := strconv.Itoa(interfaceNum)
	// `rf"net\.ipv[4,6]\.(conf|neigh)\.eth{interface_num}"`, built per call
	// because the interface number is part of the pattern. QuoteMeta is not
	// needed for a decimal, and Python does not apply it either.
	match := regexp.MustCompile(`^net\.ipv[4,6]\.(conf|neigh)\.eth` + num)
	// `rf'eth{interface_num}'` for the substitution, unanchored on both sides.
	sub := regexp.MustCompile(`eth` + num)

	var out []string
	for _, entry := range sysctls.Entries() {
		if !match.MatchString(entry.Key) {
			continue
		}
		name := sub.ReplaceAllLiteralString(entry.Key, ifnamePlaceholder)
		out = append(out, name+"="+entry.Value.String())
	}

	slices.Sort(out)
	return slices.Compact(out)
}

// createDriverOpt is `_create_driver_opt` (`DockerMachine.py:448`): the
// endpoint DriverOpts for one interface.
//
// The map is always `kathara.iface` (the number, as a string) and
// `kathara.link` (the collision-domain name), plus `kathara.mac_addr` when and
// only when the model carries an explicit MAC — the truthiness test means an
// empty MAC is as absent as a nil one (SYNTHESIS §1.3), and the absence is what
// makes the plugin fall back to a per-deploy random address, which is why no
// golden may assert a derived MAC (SYNTHESIS C-1).
//
// On engine >= 27 it also carries `com.docker.network.endpoint.sysctls`: a
// baseline of `rp_filter=0` plus the IPv6 pair or the IPv6 disable, plus the
// device's own interface-scoped sysctls, joined with commas.
//
// # The one sanctioned divergence
//
// Python joins a `set`, so the element order of that value changes between
// runs of the same command (ORDERING.tsv row 40, "KNOWN nondeterministic
// site"). The accepted ruling on OQ-8 is a CANONICAL SORTED join, which is what
// [ifaceSysctls] and the sort below produce. The set's other property —
// deduplication — is kept: a device sysctl that renders to exactly
// `net.ipv4.conf.IFNAME.rp_filter=0` collapses into the baseline rather than
// appearing twice.
//
// Errors: [model.Machine.IsIPv6Enabled]'s, and [versionGTE]'s parse failure on
// an unusable engine version.
func createDriverOpt(engineVersion string, machine *model.Machine, iface model.Interface, sysctls []string) (map[string]string, error) {
	driverOpt := map[string]string{
		driverOptIface: strconv.Itoa(iface.Number),
		driverOptLink:  iface.Link.Name,
	}

	endpointSysctls, err := versionGTE(engineVersion, engineEndpointSysctls)
	if err != nil {
		return nil, err
	}
	if endpointSysctls {
		opts := []string{"net.ipv4.conf." + ifnamePlaceholder + ".rp_filter=0"}

		ipv6, err := machine.IsIPv6Enabled()
		if err != nil {
			return nil, err
		}
		if ipv6 {
			opts = append(opts,
				"net.ipv6.conf."+ifnamePlaceholder+".disable_ipv6=0",
				"net.ipv6.conf."+ifnamePlaceholder+".forwarding=1",
			)
		} else {
			opts = append(opts, "net.ipv6.conf."+ifnamePlaceholder+".disable_ipv6=1")
		}

		opts = append(opts, sysctls...)

		slices.Sort(opts)
		driverOpt[driverOptSysctls] = strings.Join(slices.Compact(opts), ",")
	}

	if iface.MAC != "" {
		driverOpt[driverOptMacAddr] = iface.MAC
	}

	return driverOpt, nil
}
