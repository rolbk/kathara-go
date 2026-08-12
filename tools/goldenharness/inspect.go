package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// driver-opt keys set by Kathara's DockerMachine._create_driver_opt.
const (
	optIface   = "kathara.iface"
	optLink    = "kathara.link"
	optMACAddr = "kathara.mac_addr"
	optSysctls = "com.docker.network.endpoint.sysctls"
)

// buildContainerRecords converts raw inspect output into the golden subset.
// linkByNetwork maps a Docker network name to the Kathara collision-domain
// name taken from the network's own "name" label, which lets the harness
// assert that the kathara.link driver opt agrees with the network it is on.
func buildContainerRecords(raw []rawContainer, linkByNetwork map[string]string, n *Normalizer) ([]ContainerRecord, []string) {
	var failures []string
	out := make([]ContainerRecord, 0, len(raw))

	for _, c := range raw {
		device := c.Config.Labels["name"]
		if device == "" {
			device = strings.TrimPrefix(c.Name, "/")
		}

		rec := ContainerRecord{
			Device:        device,
			ContainerName: n.Text(strings.TrimPrefix(c.Name, "/")),
			Hostname:      c.Config.Hostname,
			Image:         c.Config.Image,
			User:          c.Config.User,
			Labels:        map[string]string{},
			ShellLabel:    c.Config.Labels["shell"],
			// Canonicalized: the Go SDK rewrites both lists client-side and
			// docker-py does not, so the stored representation differs between
			// the two implementations while the kernel semantics agree. The
			// harness asserts the set, in canonical form, on both sides
			// (NORMALIZATION.md section 10).
			CapAdd:       NormalizeCapabilities(append([]string{}, c.HostConfig.CapAdd...)),
			CapDrop:      NormalizeCapabilities(append([]string{}, c.HostConfig.CapDrop...)),
			Privileged:   c.HostConfig.Privileged,
			Memory:       c.HostConfig.Memory,
			NanoCPUs:     c.HostConfig.NanoCpus,
			Env:          append([]string{}, c.Config.Env...),
			Entrypoint:   append([]string{}, c.Config.Entrypoint...),
			Cmd:          append([]string{}, c.Config.Cmd...),
			NetworkMode:  n.Text(c.HostConfig.NetworkMode),
			State:        c.State.Status,
			Running:      c.State.Running,
			PortBindings: map[string][]PortBindingRecord{},
			ExposedPorts: map[string][]PortBindingRecord{},
		}

		for k, v := range c.Config.Labels {
			rec.Labels[k] = n.Text(v)
		}
		for i, e := range rec.Env {
			rec.Env[i] = n.Text(e)
		}

		// Sysctls: recorded as sorted "k=v" strings. The daemon returns a JSON
		// object whose key order is not meaningful.
		rec.Sysctls = make([]string, 0, len(c.HostConfig.Sysctls))
		for k, v := range c.HostConfig.Sysctls {
			rec.Sysctls = append(rec.Sysctls, k+"="+v)
		}
		sort.Strings(rec.Sysctls)

		rec.Ulimits = make([]UlimitRecord, 0, len(c.HostConfig.Ulimits))
		for _, u := range c.HostConfig.Ulimits {
			rec.Ulimits = append(rec.Ulimits, UlimitRecord{Name: u.Name, Soft: u.Soft, Hard: u.Hard})
		}
		sort.Slice(rec.Ulimits, func(i, j int) bool { return rec.Ulimits[i].Name < rec.Ulimits[j].Name })

		rec.Mounts = make([]MountRecord, 0, len(c.Mounts))
		for _, m := range c.Mounts {
			mr := MountRecord{
				Type:        m.Type,
				Name:        m.Name,
				Source:      n.Text(m.Source),
				Destination: m.Destination,
				Mode:        m.Mode,
				RW:          m.RW,
				Propagation: m.Propagation,
			}
			// Anonymous volumes get a fresh 64-hex id on every deploy.
			if m.Type == "volume" && IsAnonymousVolumeName(m.Name) {
				mr.Name = TokAnonVol
				mr.Source = TokAnonVolPath
			}
			rec.Mounts = append(rec.Mounts, mr)
		}
		sort.Slice(rec.Mounts, func(i, j int) bool {
			if rec.Mounts[i].Destination != rec.Mounts[j].Destination {
				return rec.Mounts[i].Destination < rec.Mounts[j].Destination
			}
			return rec.Mounts[i].Source < rec.Mounts[j].Source
		})

		for port, binds := range c.HostConfig.PortBindings {
			rec.PortBindings[port] = convertBinds(binds)
		}
		for port, binds := range c.NetworkSettings.Ports {
			rec.ExposedPorts[port] = convertBinds(binds)
		}

		// A device with the `bridged` option is additionally attached to
		// Docker's default bridge network, which is not a Kathara collision
		// domain and carries none of the kathara.* driver opts. Kathara sets
		// the bridged_iface label exactly when it does that
		// (DockerMachine.py:355), so the label and the extra endpoint must
		// agree.
		_, wantsBridge := c.Config.Labels["bridged_iface"]
		sawBridgeEndpoint := false

		for netName, ep := range c.NetworkSettings.Networks {
			_, onKatharaNet := linkByNetwork[netName]
			er := EndpointRecord{
				Network:         n.Text(netName),
				KatharaNetwork:  onKatharaNet,
				Iface:           -1,
				EndpointSysctls: []string{},
				OtherDriverOpts: map[string]string{},
			}
			for k, v := range ep.DriverOpts {
				switch k {
				case optIface:
					num, err := strconv.Atoi(v)
					if err != nil {
						failures = append(failures, fmt.Sprintf(
							"%s: endpoint on %s has non-numeric %s=%q", device, netName, optIface, v))
						continue
					}
					er.Iface = num
					er.Assertions.HasKatharaIface = true
				case optLink:
					er.Link = v
					er.Assertions.HasKatharaLink = true
				case optSysctls:
					er.EndpointSysctls = SplitSortCSV(v)
				case optMACAddr:
					er.MACDriverOpt = strings.ToLower(v)
				default:
					er.OtherDriverOpts[k] = v
				}
			}

			if onKatharaNet {
				if !er.Assertions.HasKatharaIface {
					failures = append(failures, fmt.Sprintf(
						"%s: endpoint on %s is missing driver opt %s", device, netName, optIface))
				}
				if !er.Assertions.HasKatharaLink {
					failures = append(failures, fmt.Sprintf(
						"%s: endpoint on %s is missing driver opt %s", device, netName, optLink))
				}
				want := linkByNetwork[netName]
				er.Assertions.LinkMatchesNetworkLabel = want == er.Link
				if !er.Assertions.LinkMatchesNetworkLabel {
					failures = append(failures, fmt.Sprintf(
						"%s: endpoint on %s declares %s=%q but the network's name label is %q",
						device, netName, optLink, er.Link, want))
				}
			} else {
				sawBridgeEndpoint = true
				if !wantsBridge {
					failures = append(failures, fmt.Sprintf(
						"%s: attached to non-Kathara network %s but carries no bridged_iface label",
						device, netName))
				}
				// The bridge attachment is made by plain Docker, so it must
				// carry none of Kathara's endpoint wiring.
				if er.Assertions.HasKatharaIface || er.Assertions.HasKatharaLink {
					failures = append(failures, fmt.Sprintf(
						"%s: endpoint on non-Kathara network %s carries kathara.* driver opts",
						device, netName))
				}
			}

			// MAC ruling: the katharanp_vde plugin derives a deterministic MAC
			// only when kathara.machine is present, which Kathara 3.8.3 never
			// sends. Record the MAC only when it was pinned by the lab through
			// kathara.mac_addr.
			if er.MACDriverOpt != "" {
				er.MACAddress = strings.ToLower(ep.MacAddress)
				match := er.MACAddress == er.MACDriverOpt
				er.Assertions.MACMatchesDriverOpt = &match
				if !match {
					failures = append(failures, fmt.Sprintf(
						"%s: endpoint on %s has %s=%q but the effective MAC is %q",
						device, netName, optMACAddr, er.MACDriverOpt, er.MACAddress))
				}
				n.KeepMAC(er.MACDriverOpt)
			}

			if len(er.OtherDriverOpts) == 0 {
				er.OtherDriverOpts = nil
			}
			rec.Endpoints = append(rec.Endpoints, er)
		}
		if wantsBridge && !sawBridgeEndpoint {
			failures = append(failures, fmt.Sprintf(
				"%s: carries a bridged_iface label but is on no non-Kathara network", device))
		}

		sort.Slice(rec.Endpoints, func(i, j int) bool {
			if rec.Endpoints[i].Iface != rec.Endpoints[j].Iface {
				return rec.Endpoints[i].Iface < rec.Endpoints[j].Iface
			}
			return rec.Endpoints[i].Network < rec.Endpoints[j].Network
		})

		if len(rec.Entrypoint) == 0 {
			rec.Entrypoint = nil
		}
		if len(rec.CapDrop) == 0 {
			rec.CapDrop = nil
		}
		out = append(out, rec)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out, failures
}

func convertBinds(binds []rawPortBind) []PortBindingRecord {
	out := make([]PortBindingRecord, 0, len(binds))
	for _, b := range binds {
		out = append(out, PortBindingRecord(b))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostIP != out[j].HostIP {
			return out[i].HostIP < out[j].HostIP
		}
		return out[i].HostPort < out[j].HostPort
	})
	return out
}

// buildNetworkRecords converts raw network inspect output into the golden
// subset and returns the network-name to collision-domain-name map used for
// the endpoint wiring assertion.
func buildNetworkRecords(raw []rawNetwork, n *Normalizer) ([]NetworkRecord, map[string]string) {
	out := make([]NetworkRecord, 0, len(raw))
	linkByNetwork := make(map[string]string, len(raw))

	for _, net := range raw {
		external, present := net.Labels["external"]
		rec := NetworkRecord{
			Link:            net.Labels["name"],
			NetworkName:     n.Text(net.Name),
			Driver:          net.Driver,
			Scope:           net.Scope,
			Internal:        net.Internal,
			Attachable:      net.Attachable,
			IPAMDriver:      net.IPAM.Driver,
			Labels:          map[string]string{},
			External:        external,
			ExternalPresent: present,
		}
		for k, v := range net.Labels {
			rec.Labels[k] = n.Text(v)
		}
		linkByNetwork[net.Name] = rec.Link
		out = append(out, rec)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Link != out[j].Link {
			return out[i].Link < out[j].Link
		}
		return out[i].NetworkName < out[j].NetworkName
	})
	return out, linkByNetwork
}
