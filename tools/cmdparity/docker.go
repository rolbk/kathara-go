package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const katharaLabel = "app=kathara"

// Docker drives the Docker CLI. Shelling out keeps the recorder independent of
// whatever client library the binary under test links.
type Docker struct{ Bin string }

func NewDocker(bin string) *Docker {
	if bin == "" {
		bin = "docker"
	}
	return &Docker{Bin: bin}
}

func (d *Docker) run(ctx context.Context, args ...string) (string, error) {
	argv := append([]string{d.Bin}, args...)
	res := runProcess(ctx, "", "", argv)
	if res.Err != nil {
		return res.Stdout, fmt.Errorf("run %v: %w", argv, res.Err)
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("command %q failed with exit %d: %s",
			strings.Join(argv, " "), res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res.Stdout, nil
}

func (d *Docker) IDs(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "ps", "-aq", "--filter", "label="+katharaLabel)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (d *Docker) NetworkIDs(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "network", "ls", "-q", "--filter", "label="+katharaLabel)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (d *Docker) AllContainerIDs(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "ps", "-aq")
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

func (d *Docker) DanglingVolumes(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "volume", "ls", "-q", "--filter", "dangling=true")
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

// ForceCleanup removes every Kathara container and network.
func (d *Docker) ForceCleanup(ctx context.Context) error {
	var errs []string
	ids, err := d.IDs(ctx)
	if err != nil {
		errs = append(errs, err.Error())
	}
	if len(ids) > 0 {
		if _, err := d.run(ctx, append([]string{"rm", "-f", "-v"}, ids...)...); err != nil {
			errs = append(errs, err.Error())
		}
	}
	nets, err := d.NetworkIDs(ctx)
	if err != nil {
		errs = append(errs, err.Error())
	}
	if len(nets) > 0 {
		if _, err := d.run(ctx, append([]string{"network", "rm", "-f"}, nets...)...); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("force cleanup: %s", strings.Join(errs, "; "))
	}
	return nil
}

type rawContainer struct {
	ID    string `json:"Id"`
	Name  string `json:"Name"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
	Config struct {
		Hostname   string            `json:"Hostname"`
		User       string            `json:"User"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint []string          `json:"Entrypoint"`
		Image      string            `json:"Image"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		NetworkMode  string                      `json:"NetworkMode"`
		PortBindings map[string][]map[string]any `json:"PortBindings"`
		CapAdd       []string                    `json:"CapAdd"`
		CapDrop      []string                    `json:"CapDrop"`
		Privileged   bool                        `json:"Privileged"`
		Sysctls      map[string]string           `json:"Sysctls"`
		Memory       int64                       `json:"Memory"`
		NanoCpus     int64                       `json:"NanoCpus"`
		Ulimits      []struct {
			Name string `json:"Name"`
			Soft int64  `json:"Soft"`
			Hard int64  `json:"Hard"`
		} `json:"Ulimits"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string `json:"Type"`
		Name        string `json:"Name"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		Mode        string `json:"Mode"`
		RW          bool   `json:"RW"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]struct {
			DriverOpts map[string]string `json:"DriverOpts"`
			MacAddress string            `json:"MacAddress"`
			IPAddress  string            `json:"IPAddress"`
			Aliases    []string          `json:"Aliases"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type rawNetwork struct {
	Name       string            `json:"Name"`
	ID         string            `json:"Id"`
	Scope      string            `json:"Scope"`
	Driver     string            `json:"Driver"`
	Internal   bool              `json:"Internal"`
	Attachable bool              `json:"Attachable"`
	Labels     map[string]string `json:"Labels"`
	Options    map[string]string `json:"Options"`
	Containers map[string]struct {
		Name       string `json:"Name"`
		MacAddress string `json:"MacAddress"`
	} `json:"Containers"`
}

func (d *Docker) InspectContainers(ctx context.Context, ids []string) ([]rawContainer, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := d.run(ctx, append([]string{"inspect", "--type", "container"}, ids...)...)
	if err != nil {
		return nil, err
	}
	var raw []rawContainer
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decode docker inspect: %w", err)
	}
	return raw, nil
}

func (d *Docker) InspectNetworks(ctx context.Context, ids []string) ([]rawNetwork, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := d.run(ctx, append([]string{"network", "inspect"}, ids...)...)
	if err != nil {
		return nil, err
	}
	var raw []rawNetwork
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("decode docker network inspect: %w", err)
	}
	return raw, nil
}

// --- normalized snapshot shapes -------------------------------------------

type ContainerState struct {
	Name         string               `json:"name"`
	Status       string               `json:"status"`
	Image        string               `json:"image"`
	Hostname     string               `json:"hostname"`
	Labels       map[string]string    `json:"labels"`
	Env          []string             `json:"env,omitempty"`
	Cmd          []string             `json:"cmd,omitempty"`
	Entrypoint   []string             `json:"entrypoint,omitempty"`
	NetworkMode  string               `json:"network_mode"`
	Privileged   bool                 `json:"privileged"`
	CapAdd       []string             `json:"cap_add,omitempty"`
	Sysctls      map[string]string    `json:"sysctls,omitempty"`
	Memory       int64                `json:"memory,omitempty"`
	NanoCpus     int64                `json:"nano_cpus,omitempty"`
	Ulimits      []string             `json:"ulimits,omitempty"`
	PortBindings []string             `json:"port_bindings,omitempty"`
	Mounts       []string             `json:"mounts,omitempty"`
	Networks     map[string]NetAttach `json:"networks"`
}

type NetAttach struct {
	DriverOpts map[string]string `json:"driver_opts,omitempty"`
	Aliases    []string          `json:"aliases,omitempty"`
}

type NetworkState struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Scope      string            `json:"scope"`
	Internal   bool              `json:"internal"`
	Attachable bool              `json:"attachable"`
	Labels     map[string]string `json:"labels"`
	Options    map[string]string `json:"options,omitempty"`
	Attached   []string          `json:"attached,omitempty"`
}

type DockerState struct {
	Containers []ContainerState `json:"containers"`
	Networks   []NetworkState   `json:"networks"`
	// Dangling volumes are counted, not named: their names are random.
	DanglingVolumes int `json:"dangling_volumes"`
}

// Snapshot records the Kathara-owned Docker state, normalized.
func (d *Docker) Snapshot(ctx context.Context, n *Normalizer) (*DockerState, error) {
	ids, err := d.IDs(ctx)
	if err != nil {
		return nil, err
	}
	cs, err := d.InspectContainers(ctx, ids)
	if err != nil {
		return nil, err
	}
	netIDs, err := d.NetworkIDs(ctx)
	if err != nil {
		return nil, err
	}
	ns, err := d.InspectNetworks(ctx, netIDs)
	if err != nil {
		return nil, err
	}
	vols, err := d.DanglingVolumes(ctx)
	if err != nil {
		return nil, err
	}

	st := &DockerState{DanglingVolumes: len(vols)}
	for _, c := range cs {
		out := ContainerState{
			Name:        n.Text(strings.TrimPrefix(c.Name, "/")),
			Status:      c.State.Status,
			Image:       c.Config.Image,
			Hostname:    n.Text(c.Config.Hostname),
			Labels:      normMap(n, c.Config.Labels),
			Env:         normSlice(n, c.Config.Env),
			Cmd:         normSlice(n, c.Config.Cmd),
			Entrypoint:  normSlice(n, c.Config.Entrypoint),
			NetworkMode: n.Text(c.HostConfig.NetworkMode),
			Privileged:  c.HostConfig.Privileged,
			CapAdd:      normalizeCaps(c.HostConfig.CapAdd),
			Sysctls:     normMap(n, c.HostConfig.Sysctls),
			Memory:      c.HostConfig.Memory,
			NanoCpus:    c.HostConfig.NanoCpus,
			Networks:    map[string]NetAttach{},
		}
		// A 64-hex image id is not stable across pulls; keep tags only.
		if reHex64.MatchString(out.Image) {
			out.Image = TokContainerID
		}
		for _, u := range c.HostConfig.Ulimits {
			out.Ulimits = append(out.Ulimits, fmt.Sprintf("%s=%d:%d", u.Name, u.Soft, u.Hard))
		}
		sort.Strings(out.Ulimits)
		for port, binds := range c.HostConfig.PortBindings {
			for _, b := range binds {
				out.PortBindings = append(out.PortBindings, fmt.Sprintf("%s->%v:%v", port, b["HostIp"], b["HostPort"]))
			}
		}
		sort.Strings(out.PortBindings)
		for _, m := range c.Mounts {
			name := m.Name
			if name != "" && reHex64.MatchString(name) {
				name = TokVolume
			}
			out.Mounts = append(out.Mounts, fmt.Sprintf("%s %s %s -> %s rw=%v",
				m.Type, name, n.Text(m.Source), m.Destination, m.RW))
		}
		sort.Strings(out.Mounts)
		for name, net := range c.NetworkSettings.Networks {
			opts := normMap(n, net.DriverOpts)
			if v, ok := opts["com.docker.network.endpoint.sysctls"]; ok {
				opts["com.docker.network.endpoint.sysctls"] = sortSysctlOpt(v)
			}
			out.Networks[n.Text(name)] = NetAttach{
				DriverOpts: opts,
				Aliases:    sortedCopy(normSlice(n, net.Aliases)),
			}
		}
		st.Containers = append(st.Containers, out)
	}
	sort.Slice(st.Containers, func(i, j int) bool { return st.Containers[i].Name < st.Containers[j].Name })

	for _, nw := range ns {
		out := NetworkState{
			Name:       n.Text(nw.Name),
			Driver:     nw.Driver,
			Scope:      nw.Scope,
			Internal:   nw.Internal,
			Attachable: nw.Attachable,
			Labels:     normMap(n, nw.Labels),
			Options:    normMap(n, nw.Options),
		}
		for _, c := range nw.Containers {
			out.Attached = append(out.Attached, n.Text(c.Name))
		}
		sort.Strings(out.Attached)
		st.Networks = append(st.Networks, out)
	}
	sort.Slice(st.Networks, func(i, j int) bool { return st.Networks[i].Name < st.Networks[j].Name })
	return st, nil
}

func normMap(n *Normalizer, m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[n.Text(k)] = n.Text(v)
	}
	return out
}

func normSlice(n *Normalizer, s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, 0, len(s))
	for _, v := range s {
		out = append(out, n.Text(v))
	}
	return out
}

func normalizeCaps(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, 0, len(s))
	for _, v := range s {
		out = append(out, strings.TrimPrefix(v, "CAP_"))
	}
	sort.Strings(out)
	return out
}

// sortSysctlOpt sorts the comma-separated value of the
// `com.docker.network.endpoint.sysctls` driver opt. DockerLink builds it by
// joining a Python set, so its order varies between two runs of the oracle
// itself; the set of entries is the assertion, the order is not.
func sortSysctlOpt(v string) string {
	parts := strings.Split(v, ",")
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
