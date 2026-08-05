package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// katharaLabel is the label every Kathara container and network carries.
const katharaLabel = "app=kathara"

// Docker drives the Docker CLI. The harness deliberately shells out instead of
// linking the Docker SDK so it stays independent of whatever client library the
// binary under test uses.
type Docker struct {
	Bin string
}

// NewDocker returns a Docker driver using the given binary (default "docker").
func NewDocker(bin string) *Docker {
	if bin == "" {
		bin = "docker"
	}
	return &Docker{Bin: bin}
}

func (d *Docker) run(ctx context.Context, args ...string) (string, error) {
	argv := append([]string{d.Bin}, args...)
	res := runProcess(ctx, "", nil, argv)
	if res.Err != nil {
		return res.Stdout, fmt.Errorf("run %v: %w", argv, res.Err)
	}
	if res.ExitCode != 0 {
		return res.Stdout, &ErrCommandFailed{Argv: argv, Code: res.ExitCode, Stderr: res.Stderr}
	}
	return res.Stdout, nil
}

// Exec runs a command inside a container and returns its result verbatim.
func (d *Docker) Exec(ctx context.Context, container string, argv ...string) CmdResult {
	full := append([]string{d.Bin, "exec", container}, argv...)
	return runProcess(ctx, "", nil, full)
}

// IDs returns the ids of every Kathara container (running or not).
func (d *Docker) IDs(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "ps", "-aq", "--filter", "label="+katharaLabel)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

// NetworkIDs returns the ids of every Kathara network.
func (d *Docker) NetworkIDs(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "network", "ls", "-q", "--filter", "label="+katharaLabel)
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

// AllContainerIDs returns every container on the host, Kathara or not. Used by
// the pre-flight cleanliness assertion.
func (d *Docker) AllContainerIDs(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "ps", "-aq")
	if err != nil {
		return nil, err
	}
	return splitNonEmptyLines(out), nil
}

// ForceCleanup removes every Kathara container and network. It is the recovery
// path after a failed or timed-out scenario; leaked state would poison every
// later recording.
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

// rawContainer is the subset of `docker inspect` the harness decodes.
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
		NetworkMode  string                   `json:"NetworkMode"`
		PortBindings map[string][]rawPortBind `json:"PortBindings"`
		CapAdd       []string                 `json:"CapAdd"`
		CapDrop      []string                 `json:"CapDrop"`
		Privileged   bool                     `json:"Privileged"`
		Sysctls      map[string]string        `json:"Sysctls"`
		Memory       int64                    `json:"Memory"`
		NanoCpus     int64                    `json:"NanoCpus"`
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
		Propagation string `json:"Propagation"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Ports    map[string][]rawPortBind `json:"Ports"`
		Networks map[string]struct {
			DriverOpts        map[string]string `json:"DriverOpts"`
			NetworkID         string            `json:"NetworkID"`
			EndpointID        string            `json:"EndpointID"`
			MacAddress        string            `json:"MacAddress"`
			IPAddress         string            `json:"IPAddress"`
			GlobalIPv6Address string            `json:"GlobalIPv6Address"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

type rawPortBind struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// rawNetwork is the subset of `docker network inspect` the harness decodes.
type rawNetwork struct {
	Name       string            `json:"Name"`
	ID         string            `json:"Id"`
	Scope      string            `json:"Scope"`
	Driver     string            `json:"Driver"`
	Internal   bool              `json:"Internal"`
	Attachable bool              `json:"Attachable"`
	Labels     map[string]string `json:"Labels"`
	IPAM       struct {
		Driver string `json:"Driver"`
	} `json:"IPAM"`
}

// InspectContainers decodes the Kathara subset of `docker inspect`.
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

// InspectNetworks decodes the Kathara subset of `docker network inspect`.
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

// Names returns the tokenizable names of the given containers.
func containerNames(raw []rawContainer) []string {
	out := make([]string, 0, len(raw))
	for _, c := range raw {
		out = append(out, strings.TrimPrefix(c.Name, "/"))
	}
	sort.Strings(out)
	return out
}

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
