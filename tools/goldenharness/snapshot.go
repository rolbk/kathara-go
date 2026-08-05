package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Snapshot file names inside test/goldens/<name>/.
const (
	FileScenario   = "scenario.json"
	FileCommands   = "commands.json"
	FileContainers = "containers.json"
	FileNetworks   = "networks.json"
	FileTeardown   = "teardown.json"
	DirProbes      = "probes"
)

// ScenarioRecord is the snapshot header: what was run and which derived
// identifiers the harness itself re-derived and checked.
type ScenarioRecord struct {
	SchemaVersion int      `json:"schema_version"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Status        string   `json:"status"`
	Note          string   `json:"note,omitempty"`
	LabDir        string   `json:"lab_dir"` // always the token, never a real path
	Flags         []string `json:"flags"`
	Probes        []string `json:"probes"`
	FSRoots       []string `json:"fs_roots,omitempty"`
	DeviceFiles   []string `json:"device_files,omitempty"`
	HostFiles     []string `json:"host_files,omitempty"`
	HostDirs      []string `json:"host_dirs,omitempty"`

	// Derived-identifier assertions. The values themselves are host dependent
	// and are tokenized everywhere else in the snapshot; what is recorded here
	// is whether the binary under test derived them the way Kathara specifies.
	LabHashSource     string   `json:"lab_hash_source"` // "path" | "lab_name"
	LabHashDerivedOK  bool     `json:"lab_hash_derived_ok"`
	UserSlugDerivedOK bool     `json:"user_slug_derived_ok"`
	ContainerNamesOK  bool     `json:"container_names_ok"`
	NetworkNamesOK    bool     `json:"network_names_ok"`
	ExplicitMACCount  int      `json:"explicit_mac_count"`
	ExpectExplicitMAC bool     `json:"expect_explicit_mac"`
	AssertionFailures []string `json:"assertion_failures,omitempty"`
}

// CommandRecord is one invocation of the binary under test.
type CommandRecord struct {
	Step     string   `json:"step"`
	Argv     []string `json:"argv"` // tokenized
	ExitCode int      `json:"exit_code"`
	TimedOut bool     `json:"timed_out,omitempty"`
	Stdout   []string `json:"stdout"`
	Stderr   []string `json:"stderr"`
}

// UlimitRecord mirrors HostConfig.Ulimits.
type UlimitRecord struct {
	Name string `json:"name"`
	Soft int64  `json:"soft"`
	Hard int64  `json:"hard"`
}

// MountRecord is one entry of the container Mounts array.
type MountRecord struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"` // tokenized
	Destination string `json:"destination"`
	Mode        string `json:"mode"`
	RW          bool   `json:"rw"`
	Propagation string `json:"propagation"`
}

// PortBindingRecord is one host-side binding for a container port.
type PortBindingRecord struct {
	HostIP   string `json:"host_ip"`
	HostPort string `json:"host_port"`
}

// EndpointAssertions records the wiring invariants the harness checks per
// endpoint. These replace the (empirically false) deterministic-MAC anchor of
// PORT_SPEC §9; see NORMALIZATION.md.
type EndpointAssertions struct {
	HasKatharaIface         bool  `json:"has_kathara_iface"`
	HasKatharaLink          bool  `json:"has_kathara_link"`
	LinkMatchesNetworkLabel bool  `json:"link_matches_network_label"`
	MACMatchesDriverOpt     *bool `json:"mac_matches_driver_opt,omitempty"`
}

// EndpointRecord is one container/network attachment.
type EndpointRecord struct {
	Network string `json:"network"` // tokenized
	// KatharaNetwork distinguishes a collision-domain attachment from the
	// Docker default-bridge attachment the `bridged` option adds.
	KatharaNetwork  bool               `json:"kathara_network"`
	Iface           int                `json:"iface"`
	Link            string             `json:"link"`
	EndpointSysctls []string           `json:"endpoint_sysctls"`
	OtherDriverOpts map[string]string  `json:"other_driver_opts,omitempty"`
	MACAddress      string             `json:"mac_address,omitempty"`
	MACDriverOpt    string             `json:"mac_driver_opt,omitempty"`
	Assertions      EndpointAssertions `json:"assertions"`
}

// ContainerRecord is the golden subset of `docker inspect <container>`.
type ContainerRecord struct {
	Device        string                         `json:"device"`
	ContainerName string                         `json:"container_name"` // tokenized
	Hostname      string                         `json:"hostname"`
	Image         string                         `json:"image"`
	User          string                         `json:"user"`
	Labels        map[string]string              `json:"labels"` // tokenized values
	ShellLabel    string                         `json:"shell_label"`
	CapAdd        []string                       `json:"cap_add"`
	CapDrop       []string                       `json:"cap_drop,omitempty"`
	Privileged    bool                           `json:"privileged"`
	Sysctls       []string                       `json:"sysctls"` // "k=v", sorted
	Ulimits       []UlimitRecord                 `json:"ulimits"` // sorted by name
	Memory        int64                          `json:"memory"`
	NanoCPUs      int64                          `json:"nano_cpus"`
	Env           []string                       `json:"env"` // Docker order, not sorted
	Entrypoint    []string                       `json:"entrypoint,omitempty"`
	Cmd           []string                       `json:"cmd,omitempty"`
	NetworkMode   string                         `json:"network_mode"` // tokenized
	Mounts        []MountRecord                  `json:"mounts"`       // sorted by destination
	PortBindings  map[string][]PortBindingRecord `json:"port_bindings"`
	ExposedPorts  map[string][]PortBindingRecord `json:"exposed_ports"`
	Endpoints     []EndpointRecord               `json:"endpoints"` // sorted by iface
	State         string                         `json:"state"`
	Running       bool                           `json:"running"`
}

// NetworkRecord is the golden subset of `docker network inspect <network>`.
type NetworkRecord struct {
	Link            string            `json:"link"`
	NetworkName     string            `json:"network_name"` // tokenized
	Driver          string            `json:"driver"`
	Scope           string            `json:"scope"`
	Internal        bool              `json:"internal"`
	Attachable      bool              `json:"attachable"`
	IPAMDriver      string            `json:"ipam_driver"`
	Labels          map[string]string `json:"labels"` // tokenized values
	External        string            `json:"external"`
	ExternalPresent bool              `json:"external_label_present"`
}

// FSEntry is one node of a mounted file tree.
type FSEntry struct {
	Kind   string `json:"kind"` // "f" | "d" | "l"
	Path   string `json:"path"`
	SHA256 string `json:"sha256,omitempty"`
	Target string `json:"target,omitempty"`
}

// FileProbeRecord is the result of probing one in-container path.
type FileProbeRecord struct {
	Path    string   `json:"path"`
	Exists  bool     `json:"exists"`
	SHA256  string   `json:"sha256,omitempty"`
	Content []string `json:"content,omitempty"`
}

// HostFileRecord is the result of probing one host-side path after lclean.
type HostFileRecord struct {
	Path    string   `json:"path"` // lab-dir relative
	Exists  bool     `json:"exists"`
	SHA256  string   `json:"sha256,omitempty"`
	Content []string `json:"content,omitempty"`
}

// DeviceProbes is the in-container probe bundle for one device.
type DeviceProbes struct {
	Device     string               `json:"device"`
	IPBrLink   []string             `json:"ip_br_link,omitempty"`
	IPAddr     []map[string]any     `json:"ip_addr,omitempty"`
	IPRoute    []string             `json:"ip_route,omitempty"`
	EtcHosts   []string             `json:"etc_hosts,omitempty"`
	Hostname   string               `json:"hostname,omitempty"`
	FSTrees    map[string][]FSEntry `json:"fs_trees,omitempty"`
	StartupLog map[string][]string  `json:"startup_logs,omitempty"`
	Files      []FileProbeRecord    `json:"files,omitempty"`
	Errors     map[string]string    `json:"probe_errors,omitempty"`
}

// TeardownRecord is the post-lclean state assertion.
type TeardownRecord struct {
	KatharaContainers []string         `json:"kathara_containers"` // tokenized names, must be empty
	KatharaNetworks   []string         `json:"kathara_networks"`   // tokenized names, must be empty
	Clean             bool             `json:"clean"`
	HostFiles         []HostFileRecord `json:"host_files,omitempty"`
}

// Snapshot is the whole recorded state of one scenario run.
type Snapshot struct {
	Scenario   ScenarioRecord
	Commands   []CommandRecord
	Containers []ContainerRecord
	Networks   []NetworkRecord
	Probes     []DeviceProbes
	Teardown   TeardownRecord
}

// writeCanonicalJSON writes v as canonical JSON: two-space indent, HTML
// escaping off, map keys sorted by encoding/json, trailing newline.
func writeCanonicalJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", path, err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Write serializes the snapshot into dir, replacing any previous content.
func (s *Snapshot) Write(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := writeCanonicalJSON(filepath.Join(dir, FileScenario), s.Scenario); err != nil {
		return err
	}
	if err := writeCanonicalJSON(filepath.Join(dir, FileCommands), nonNilCommands(s.Commands)); err != nil {
		return err
	}
	if err := writeCanonicalJSON(filepath.Join(dir, FileContainers), nonNilContainers(s.Containers)); err != nil {
		return err
	}
	if err := writeCanonicalJSON(filepath.Join(dir, FileNetworks), nonNilNetworks(s.Networks)); err != nil {
		return err
	}
	for _, p := range s.Probes {
		path := filepath.Join(dir, DirProbes, p.Device+".json")
		if err := writeCanonicalJSON(path, p); err != nil {
			return err
		}
	}
	return writeCanonicalJSON(filepath.Join(dir, FileTeardown), s.Teardown)
}

func nonNilCommands(v []CommandRecord) []CommandRecord {
	if v == nil {
		return []CommandRecord{}
	}
	return v
}

func nonNilContainers(v []ContainerRecord) []ContainerRecord {
	if v == nil {
		return []ContainerRecord{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i].Device < v[j].Device })
	return v
}

func nonNilNetworks(v []NetworkRecord) []NetworkRecord {
	if v == nil {
		return []NetworkRecord{}
	}
	sort.Slice(v, func(i, j int) bool { return v[i].Link < v[j].Link })
	return v
}
