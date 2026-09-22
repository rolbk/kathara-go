// It preserves two compatibility edge cases and one documented divergence,
// all three marked at their line.

package docker

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/model"
)

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
var ifaceSysctlRE = regexp.MustCompile(`^net\.ipv[4,6]\.(conf|neigh)\.eth[\p{Nd}]+`)

// ifaceSysctls is `_get_iface_sysctls` (`DockerMachine.py:429`): the device's
// own sysctls that name THIS interface, rewritten with the literal `IFNAME`
// token the engine expands per endpoint, as `key=value` strings.
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
