package cliout

import (
	"fmt"
	"sort"

	"github.com/KatharaFramework/kathara-go/kathara"
)

type Lab struct {
	// Name is `lab.name`, null for a path-parsed scenario with no `LAB_NAME=`.
	Name *string
	// Hash is `lab.hash`.
	Hash string
	// Path is the realpath-resolved scenario directory, null for
	// `kathara_vlab` and for an archive deploy.
	Path *string

	// Description, Version, Author, Email and Web are the `LAB_*` values. They
	// are emitted together, and only by `lstart`/`lrestart`, which are the
	// commands that parse `lab.conf` in full — and only when at least one is
	// non-empty.
	Description, Version, Author, Email, Web string
}

// hasMeta reports whether the five additive keys are emitted at all.
func (l Lab) hasMeta() bool {
	return l.Description != "" || l.Version != "" || l.Author != "" || l.Email != "" || l.Web != ""
}

func (l Lab) encode(withMeta bool) []byte {
	o := newObj()
	o.NullableStr("name", l.Name)
	o.Str("hash", l.Hash)
	o.NullableStr("path", l.Path)
	if withMeta && l.hasMeta() {
		o.NullableStr("description", nilIfEmpty(l.Description))
		o.NullableStr("version", nilIfEmpty(l.Version))
		o.NullableStr("author", nilIfEmpty(l.Author))
		o.NullableStr("email", nilIfEmpty(l.Email))
		o.NullableStr("web", nilIfEmpty(l.Web))
	}
	return o.Bytes()
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Str is a helper for the many optional strings of these envelopes.
func Str(s string) *string { return &s }

func encodeMachineStats(s *kathara.MachineStats) []byte {
	var tmp jobj
	tmp.writeValue(s)
	return tmp.buf.Bytes()
}

type LstartResult struct {
	// Lab is the scenario, with its metadata keys.
	Lab Lab
	// DryRun is `--print`/`--dry-mode`.
	DryRun bool
	// Checks is the dry-mode file list, in the order Python prints its ✓
	// lines: `lab.conf`, then `lab.dep` when dependencies were parsed.
	Checks []string
	// Machines are the devices deployed, in schedule order.
	Machines []string
	// Links are the collision domains deployed, canonically sorted.
	Links []string
	// MachineStats is the `-l/--list` addition, sorted by name. Nil when the
	// flag was absent; the key is then omitted entirely.
	MachineStats []*kathara.MachineStats
}

func (r LstartResult) encode() []byte {
	o := newObj()
	o.Raw("lab", r.Lab.encode(true))
	o.Bool("dry_run", r.DryRun)
	if r.DryRun {
		checks := make([][]byte, 0, len(r.Checks))
		for _, file := range r.Checks {
			checks = append(checks, newObj().Str("file", file).Bool("ok", true).Bytes())
		}
		o.RawArray("checks", checks)
		return o.Bytes()
	}
	o.Strings("machines", r.Machines)
	o.Strings("links", r.Links)
	if r.MachineStats != nil {
		stats := make([][]byte, 0, len(r.MachineStats))
		for _, s := range r.MachineStats {
			stats = append(stats, encodeMachineStats(s))
		}
		o.RawArray("machine_stats", stats)
	}
	return o.Bytes()
}

type LcleanResult struct {
	Lab      Lab
	Machines []string
	Links    []string
}

func (r LcleanResult) encode() []byte {
	o := newObj()
	o.Raw("lab", r.Lab.encode(false))
	o.Strings("machines", r.Machines)
	o.Strings("links", r.Links)
	return o.Bytes()
}

type LrestartResult struct {
	Clean LcleanResult
	Start LstartResult
}

func (r LrestartResult) encode() []byte {
	o := newObj()
	o.Raw("clean", r.Clean.encode())
	o.Raw("start", r.Start.encode())
	return o.Bytes()
}

type WipeResult struct {
	SettingsWiped bool
	AllUsers      bool
	Machines      []string
	Links         []string
}

func (r WipeResult) encode() []byte {
	o := newObj()
	o.Bool("settings_wiped", r.SettingsWiped)
	o.Bool("all_users", r.AllUsers)
	o.Strings("machines", r.Machines)
	o.Strings("links", r.Links)
	return o.Bytes()
}

type ListResult struct {
	Machines []*kathara.MachineStats
}

func (r ListResult) encode() []byte {
	sorted := make([]*kathara.MachineStats, len(r.Machines))
	copy(sorted, r.Machines)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].NetworkScenarioID != sorted[j].NetworkScenarioID {
			return sorted[i].NetworkScenarioID < sorted[j].NetworkScenarioID
		}
		return sorted[i].Name < sorted[j].Name
	})
	items := make([][]byte, 0, len(sorted))
	for _, s := range sorted {
		items = append(items, encodeMachineStats(s))
	}
	o := newObj()
	o.RawArray("machines", items)
	return o.Bytes()
}

type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r ExecResult) encode() []byte {
	o := newObj()
	o.Str("stdout", r.Stdout)
	o.Str("stderr", r.Stderr)
	o.Int("exit_code", r.ExitCode)
	return o.Bytes()
}

type CheckResult struct {
	Manager        string
	ManagerVersion string
	RuntimeVersion string
	KatharaVersion string
	OSVersion      string
	Image          string
	OK             bool
	Error          string
}

func (r CheckResult) encode() []byte {
	test := newObj()
	test.Str("image", r.Image)
	test.Bool("ok", r.OK)
	test.NullableStr("error", nilIfEmpty(r.Error))

	o := newObj()
	o.Str("manager", r.Manager)
	o.Str("manager_version", r.ManagerVersion)
	o.Str("runtime_version", r.RuntimeVersion)
	o.Str("kathara_version", r.KatharaVersion)
	o.Str("os_version", r.OSVersion)
	o.Raw("container_test", test.Bytes())
	return o.Bytes()
}

type VstartResult struct {
	Lab     Lab
	Machine string
	DryRun  bool
	Links   []string
}

func (r VstartResult) encode() []byte {
	o := newObj()
	o.Raw("lab", r.Lab.encode(false))
	o.Str("machine", r.Machine)
	o.Bool("dry_run", r.DryRun)
	if !r.DryRun {
		o.Strings("links", r.Links)
	}
	return o.Bytes()
}

type VcleanResult struct {
	Lab      Lab
	Machine  string
	Machines []string
}

func (r VcleanResult) encode() []byte {
	o := newObj()
	o.Raw("lab", r.Lab.encode(false))
	o.Str("machine", r.Machine)
	o.Strings("machines", r.Machines)
	return o.Bytes()
}

type AddedLink struct {
	Link string
	MAC  string
}

type ConfigResult struct {
	Lab     Lab
	Machine string
	Added   []AddedLink
	Removed []string
}

func (r ConfigResult) encode() []byte {
	added := make([][]byte, 0, len(r.Added))
	for _, a := range r.Added {
		added = append(added, newObj().Str("link", a.Link).NullableStr("mac", nilIfEmpty(a.MAC)).Bytes())
	}
	o := newObj()
	o.Raw("lab", r.Lab.encode(false))
	o.Str("machine", r.Machine)
	o.RawArray("added", added)
	o.Strings("removed", r.Removed)
	return o.Bytes()
}

type SettingsGetResult struct {
	Key   string
	Value any
}

func (r SettingsGetResult) encode() []byte {
	return newObj().Str("key", r.Key).Any("value", r.Value).Bytes()
}

// SettingsSetResult is the `config set` response.
type SettingsSetResult struct {
	Key   string
	Value any
	Saved bool
}

func (r SettingsSetResult) encode() []byte {
	return newObj().Str("key", r.Key).Any("value", r.Value).Bool("saved", r.Saved).Bytes()
}

// SettingsListResult is the `config list` response. Settings must marshal
// itself in file order, which `settings.Settings.MarshalJSON` does.
type SettingsListResult struct {
	Settings any
}

func (r SettingsListResult) encode() []byte {
	return newObj().Any("settings", r.Settings).Bytes()
}

// SettingsResetResult is the `config reset` response.
type SettingsResetResult struct {
	Settings any
	Saved    bool
}

func (r SettingsResetResult) encode() []byte {
	return newObj().Any("settings", r.Settings).Bool("saved", r.Saved).Bytes()
}

// Envelope is what a command hands to [Console.Emit]: any of the result types
// above.
type Envelope interface{ encode() []byte }

var (
	_ Envelope = LstartResult{}
	_ Envelope = LcleanResult{}
	_ Envelope = LrestartResult{}
	_ Envelope = WipeResult{}
	_ Envelope = ListResult{}
	_ Envelope = ExecResult{}
	_ Envelope = CheckResult{}
	_ Envelope = VstartResult{}
	_ Envelope = VcleanResult{}
	_ Envelope = ConfigResult{}
	_ Envelope = SettingsGetResult{}
	_ Envelope = SettingsSetResult{}
	_ Envelope = SettingsListResult{}
	_ Envelope = SettingsResetResult{}
)

func (c *Console) Emit(e Envelope) {
	if c.Format != FormatJSON {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.Out.Write(e.encode())
	_, _ = fmt.Fprintln(c.Out)
}

func (c *Console) EmitInterrupted() {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch c.Format {
	case FormatJSON:
		_, _ = fmt.Fprintln(c.Out, `{"interrupted":true}`)
	case FormatJSONL:
		_, _ = fmt.Fprintln(c.Out, `{"type":"interrupted"}`)
	}
}

func (c *Console) EmitStreamChunk(stream string, data string) {
	if c.Format != FormatJSONL || data == "" {
		return
	}
	obj := newObj().Str("type", stream).Str("data", data).Bytes()
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.Out.Write(obj)
	_, _ = fmt.Fprintln(c.Out)
}

// EmitStreamExit writes the terminal event of a successful `jsonl` stream. The
// process then exits with the same code.
func (c *Console) EmitStreamExit(code int) {
	if c.Format != FormatJSONL {
		return
	}
	obj := newObj().Str("type", "exit").Int("code", code).Bytes()
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.Out.Write(obj)
	_, _ = fmt.Fprintln(c.Out)
}
