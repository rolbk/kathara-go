package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is stamped into every snapshot. Bump it whenever the recorded
// shape changes in a way that invalidates stored goldens.
const SchemaVersion = 1

// Scenario kinds.
const (
	KindDeploy = "deploy" // lstart --noterminals, probe the running lab, lclean
	KindDry    = "dry"    // lstart --print (dry mode): parse only, nothing deployed
	KindError  = "error"  // the command under test is expected to fail; no probes
)

// Scenario statuses.
const (
	// StatusGolden means verify treats any diff as a failure.
	StatusGolden = "golden"
	// StatusReference means the snapshot is recorded from the Python oracle for
	// documentation only; verify reports diffs but does not fail on them. Used
	// for behaviour the Go port is specified to diverge on (e.g. lab.ext, which
	// PORT_SPEC §0.3 defers to post-1.0).
	StatusReference = "reference"
)

// Probe names accepted in a scenario probe set.
const (
	ProbeLink       = "link"        // ip -br link
	ProbeAddr       = "addr"        // ip -j addr
	ProbeRoute      = "route"       // ip route
	ProbeHosts      = "hosts"       // cat /etc/hosts
	ProbeHostname   = "hostname"    // hostname
	ProbeFSTree     = "fstree"      // mounted file tree + sha256 per file
	ProbeStartupLog = "startup_log" // /var/log/startup.log and /var/log/shared.log
	ProbeFiles      = "files"       // per-scenario device_files
)

var knownProbes = map[string]bool{
	ProbeLink: true, ProbeAddr: true, ProbeRoute: true, ProbeHosts: true,
	ProbeHostname: true, ProbeFSTree: true, ProbeStartupLog: true, ProbeFiles: true,
}

// Manifest is the on-disk scenario list (tools/goldenharness/scenarios.yaml).
type Manifest struct {
	Version   int        `yaml:"version"`
	Defaults  Defaults   `yaml:"defaults"`
	Scenarios []Scenario `yaml:"scenarios"`
}

// Defaults are applied to every scenario that does not override them.
type Defaults struct {
	LabsRoot       string   `yaml:"labs_root"`
	TimeoutSeconds int      `yaml:"timeout_seconds"`
	Probes         []string `yaml:"probes"`
	FSRoots        []string `yaml:"fs_roots"`
}

// Scenario is one recorded lab run.
type Scenario struct {
	Name string `yaml:"name"`

	// Dir locates the network scenario. Accepted forms:
	//   labs:<rel>  -> <labs_root>/<rel>
	//   /abs/path   -> used verbatim
	//   <rel>       -> <repo_root>/<rel>
	Dir string `yaml:"dir"`

	// Kind selects the command shape (deploy | dry | error).
	Kind string `yaml:"kind"`

	// Status selects verify strictness (golden | reference).
	Status string `yaml:"status"`

	// Note is copied into the snapshot; use it to explain non-obvious coverage.
	Note string `yaml:"note"`

	// Flags are appended to the lstart argv after the harness-owned flags.
	Flags []string `yaml:"flags"`

	// Probes overrides the default probe set for this scenario.
	Probes []string `yaml:"probes"`

	// FSRoots overrides the in-container directories walked by the fstree probe.
	FSRoots []string `yaml:"fs_roots"`

	// DeviceFiles are absolute in-container paths recorded by the files probe.
	DeviceFiles []string `yaml:"device_files"`

	// HostFiles are lab-dir-relative paths recorded on the host after lclean.
	// This is how *.shutdown side effects are observed.
	HostFiles []string `yaml:"host_files"`

	// ResetFiles are lab-dir-relative paths deleted before the run so that
	// append-style startup markers do not accumulate across recordings.
	ResetFiles []string `yaml:"reset_files"`

	// HostDirs are absolute host directories the lab mounts through the
	// `volume` option. Kathara refuses to start when a volume source is
	// missing (utils.check_directory_permissions raises FileExistsError), so
	// whether the recording exercises the mount or the error path would
	// otherwise depend on host state the harness does not own. The harness
	// creates them before the run and removes the ones it created afterwards,
	// exactly as it does for the lab's `shared/` directory.
	HostDirs []string `yaml:"host_dirs"`

	// ExpectExplicitMAC asserts that at least one endpoint carries a
	// kathara.mac_addr driver opt (see the MAC ruling in NORMALIZATION.md).
	ExpectExplicitMAC bool `yaml:"expect_explicit_mac"`

	// TimeoutSeconds overrides the default per-scenario wall-clock limit.
	TimeoutSeconds int `yaml:"timeout_seconds"`

	// SettleSeconds delays the observation (inspect and in-container probes)
	// after lstart returns. Default 0: the snapshot is taken the instant
	// lstart is done, which is what every scenario wants unless the lab's
	// startup script leaves a *link-layer* state machine converging behind it.
	// Kathara dispatches the startup commands with detach=True
	// (DockerMachine.py:555), so lstart returning says nothing about them
	// having finished, let alone about the kernel having settled.
	//
	// This is deliberately opt-in per scenario rather than a global default:
	// a blanket delay would change what every existing golden records, and the
	// scenarios that need it are the ones whose note says why.
	SettleSeconds int `yaml:"settle_seconds"`

	// resolved fields, filled by Manifest.Resolve.
	labDir  string
	probes  []string
	fsRoots []string
	timeout int
}

// LabDir is the resolved absolute path of the network scenario directory.
func (s *Scenario) LabDir() string { return s.labDir }

// ProbeSet is the resolved probe set.
func (s *Scenario) ProbeSet() []string { return s.probes }

// FSRootSet is the resolved fstree root list.
func (s *Scenario) FSRootSet() []string { return s.fsRoots }

// TimeoutSecs is the resolved per-scenario timeout.
func (s *Scenario) TimeoutSecs() int { return s.timeout }

// SettleDelay is how long to wait after lstart before observing anything.
func (s *Scenario) SettleDelay() time.Duration {
	if s.SettleSeconds <= 0 {
		return 0
	}
	return time.Duration(s.SettleSeconds) * time.Second
}

// HasProbe reports whether the resolved probe set contains name.
func (s *Scenario) HasProbe(name string) bool {
	for _, p := range s.probes {
		if p == name {
			return true
		}
	}
	return false
}

const (
	defaultTimeoutSeconds = 240
	defaultLabsRoot       = "/root/kathara/Kathara-Labs"
)

var (
	defaultProbes  = []string{ProbeLink, ProbeAddr, ProbeHosts, ProbeHostname, ProbeFSTree}
	defaultFSRoots = []string{"/hostlab", "/shared"}
)

// LoadManifest reads and resolves the scenario manifest.
func LoadManifest(path, repoRoot, labsRootOverride string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	if err := m.Resolve(repoRoot, labsRootOverride); err != nil {
		return nil, err
	}
	return &m, nil
}

// Resolve fills every scenario's derived fields and validates the manifest.
func (m *Manifest) Resolve(repoRoot, labsRootOverride string) error {
	labsRoot := m.Defaults.LabsRoot
	if labsRootOverride != "" {
		labsRoot = labsRootOverride
	}
	if labsRoot == "" {
		labsRoot = defaultLabsRoot
	}
	if m.Defaults.TimeoutSeconds <= 0 {
		m.Defaults.TimeoutSeconds = defaultTimeoutSeconds
	}
	if len(m.Defaults.Probes) == 0 {
		m.Defaults.Probes = append([]string(nil), defaultProbes...)
	}
	if len(m.Defaults.FSRoots) == 0 {
		m.Defaults.FSRoots = append([]string(nil), defaultFSRoots...)
	}

	seen := make(map[string]bool, len(m.Scenarios))
	for i := range m.Scenarios {
		s := &m.Scenarios[i]
		if s.Name == "" {
			return fmt.Errorf("scenario %d: empty name", i)
		}
		if seen[s.Name] {
			return fmt.Errorf("scenario %q: duplicate name", s.Name)
		}
		seen[s.Name] = true
		if strings.ContainsAny(s.Name, "/\\ ") {
			return fmt.Errorf("scenario %q: name must be a single path-safe segment", s.Name)
		}
		if s.Dir == "" {
			return fmt.Errorf("scenario %q: empty dir", s.Name)
		}

		switch {
		case strings.HasPrefix(s.Dir, "labs:"):
			s.labDir = filepath.Join(labsRoot, strings.TrimPrefix(s.Dir, "labs:"))
		case filepath.IsAbs(s.Dir):
			s.labDir = filepath.Clean(s.Dir)
		default:
			s.labDir = filepath.Join(repoRoot, s.Dir)
		}

		if s.Kind == "" {
			s.Kind = KindDeploy
		}
		switch s.Kind {
		case KindDeploy, KindDry, KindError:
		default:
			return fmt.Errorf("scenario %q: unknown kind %q", s.Name, s.Kind)
		}

		if s.Status == "" {
			s.Status = StatusGolden
		}
		switch s.Status {
		case StatusGolden, StatusReference:
		default:
			return fmt.Errorf("scenario %q: unknown status %q", s.Name, s.Status)
		}

		s.probes = s.Probes
		if len(s.probes) == 0 {
			s.probes = m.Defaults.Probes
		}
		if s.Kind != KindDeploy {
			s.probes = nil
		}
		for _, p := range s.probes {
			if !knownProbes[p] {
				return fmt.Errorf("scenario %q: unknown probe %q", s.Name, p)
			}
		}
		if len(s.DeviceFiles) > 0 && !s.HasProbe(ProbeFiles) {
			s.probes = append(append([]string(nil), s.probes...), ProbeFiles)
		}

		s.fsRoots = s.FSRoots
		if len(s.fsRoots) == 0 {
			s.fsRoots = m.Defaults.FSRoots
		}

		for _, d := range s.HostDirs {
			if !filepath.IsAbs(d) || strings.Contains(d, "..") {
				return fmt.Errorf("scenario %q: host_dirs entry %q must be an absolute path with no parent traversal", s.Name, d)
			}
		}

		s.timeout = s.TimeoutSeconds
		if s.timeout <= 0 {
			s.timeout = m.Defaults.TimeoutSeconds
		}
	}
	return nil
}

// Select returns the scenarios whose names match one of the given patterns
// (filepath.Match syntax). An empty pattern list selects everything.
func (m *Manifest) Select(patterns []string) ([]Scenario, error) {
	if len(patterns) == 0 {
		return m.Scenarios, nil
	}
	var out []Scenario
	matched := make(map[string]bool, len(patterns))
	for _, s := range m.Scenarios {
		for _, p := range patterns {
			ok, err := filepath.Match(p, s.Name)
			if err != nil {
				return nil, fmt.Errorf("bad scenario pattern %q: %w", p, err)
			}
			if ok {
				out = append(out, s)
				matched[p] = true
				break
			}
		}
	}
	for _, p := range patterns {
		if !matched[p] {
			return nil, fmt.Errorf("scenario pattern %q matched nothing", p)
		}
	}
	return out, nil
}
