package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// fakeManager records what a command asked the backend to do.
type fakeManager struct {
	kathara.Manager

	deployedLabs   []*model.Lab
	deployOpts     []kathara.DeployLabOptions
	undeployedRefs []kathara.LabRef
	undeployOpts   []kathara.UndeployLabOptions
	wiped          []bool
	stats          []kathara.MachineStatsEntry
	links          []kathara.LinkStatsEntry
	deployErr      error
}

func (f *fakeManager) DeployLab(_ context.Context, lab *model.Lab, opts kathara.DeployLabOptions) error {
	f.deployedLabs = append(f.deployedLabs, lab)
	f.deployOpts = append(f.deployOpts, opts)
	return f.deployErr
}

func (f *fakeManager) UndeployLab(_ context.Context, ref kathara.LabRef, opts kathara.UndeployLabOptions) error {
	f.undeployedRefs = append(f.undeployedRefs, ref)
	f.undeployOpts = append(f.undeployOpts, opts)
	return nil
}

func (f *fakeManager) Wipe(_ context.Context, allUsers bool) error {
	f.wiped = append(f.wiped, allUsers)
	return nil
}

func (f *fakeManager) GetMachinesStats(context.Context, kathara.LabRef, string, bool) (kathara.MachinesStatsStream, error) {
	return &fakeStatsStream{entries: f.stats}, nil
}

func (f *fakeManager) GetLinksStats(context.Context, kathara.LabRef, string, bool) (kathara.LinksStatsStream, error) {
	return &fakeLinksStream{entries: f.links}, nil
}

type fakeLinksStream struct {
	entries []kathara.LinkStatsEntry
	done    bool
}

func (s *fakeLinksStream) Next(context.Context) ([]kathara.LinkStatsEntry, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return s.entries, nil
}

func (s *fakeLinksStream) Close() error { return nil }

type fakeStatsStream struct {
	entries []kathara.MachineStatsEntry
	done    bool
}

func (s *fakeStatsStream) Next(context.Context) ([]kathara.MachineStatsEntry, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return s.entries, nil
}

func (s *fakeStatsStream) Close() error { return nil }

// withManager wires a fake into the app.
func withManager(a *testApp, m kathara.Manager) {
	a.newManager = func(context.Context) (kathara.Manager, error) { return m, nil }
}

// scenarioDir writes a scenario into a temporary directory.
func scenarioDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLstartHumanOutputMatchesGoldenShape reproduces the two-panel prologue
// every `test/goldens/*/commands.json` records for a scenario with metadata.
func TestLstartHumanOutputMatchesGoldenShape(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": strings.Join([]string{
			`LAB_DESCRIPTION="A simple example showing how to configure static routes"`,
			`LAB_VERSION="3.0"`,
			`LAB_AUTHOR="T. Caiazzi, G. Di Battista, M. Patrignani, M. Pizzonia, F. Ricci, M. Rimondini"`,
			`LAB_EMAIL="contact@kathara.org"`,
			`LAB_WEB="http://www.kathara.org/"`,
			`pc1[0]="A"`,
		}, "\n"),
	})

	a := newTestApp(t)
	withManager(a, &fakeManager{})
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"--noterminals", "-d", dir}); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, a.stdoutString())
	}

	want := []string{
		"┌──────────────────────────────────────────────────────────────────────────────┐",
		"│                          Starting Network Scenario                           │",
		"└──────────────────────────────────────────────────────────────────────────────┘",
		"┌──────────────────────────────────────────────────────────────────────────────┐",
		"│ Description: A simple example showing how to configure static routes         │",
		"│ Version: 3.0                                                                 │",
		"│ Author(s): T. Caiazzi, G. Di Battista, M. Patrignani, M. Pizzonia, F. Ricci, │",
		"│ M. Rimondini                                                                 │",
		"│ Email: contact@kathara.org                                                   │",
		"│ Website: http://www.kathara.org/                                             │",
		"└──────────────────────────────────────────────────────────────────────────────┘",
	}
	got := strings.Split(strings.TrimRight(a.stdoutString(), "\n"), "\n")
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d:\n%s", len(got), len(want), a.stdoutString())
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

func TestLstartDryModeReturnsBeforeDeploy(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "pc1[0]=\"A\"\npc2[0]=\"A\"\n",
		"lab.dep":  "pc2: pc1\n",
	})

	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"--print", "-d", dir}); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, a.stdoutString())
	}
	if len(fake.deployedLabs) != 0 {
		t.Error("dry mode deployed something")
	}
	out := a.stdoutString()
	for _, want := range []string{
		"│                          Checking Network Scenario                           │",
		"✓ lab.conf file is correct.",
		"✓ lab.dep file is correct.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, out)
		}
	}
}

func TestLstartLabExtIsDeferred(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "pc1[0]=\"A\"\n",
		"lab.ext":  "A eth0\n",
	})

	a := newTestApp(t)
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"--format", "json", "--print", "-d", dir}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := `{"error":{"code":"FeatureNotAvailable","message":"lab.ext external links are not supported ` +
		`in this release. Use Kathará 3.8.x.","feature":"lab.ext"}}` + "\n"
	if got := a.stdoutString(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestLstartEmptyScenarioIsEmptyLabError is `LstartCommand.py:185`.
func TestLstartEmptyScenarioIsEmptyLabError(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "LAB_VERSION=\"1.0\"\n"})

	a := newTestApp(t)
	a.console.Level = cliout.LevelDebug
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(a.stdoutString(), "CRITICAL (EmptyLabError) No devices in the current network scenario.") {
		t.Errorf("stdout = %q", a.stdoutString())
	}
}

func TestLstartSelectionFiltersAreAlwaysSets(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\npc2[0]=\"A\"\n"})

	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	if fake.deployOpts[0].SelectedMachines == nil || fake.deployOpts[0].ExcludedMachines == nil {
		t.Error("lstart must pass empty sets, not nil")
	}
}

// TestLcleanFiltersAreNilWhenEmpty checks the inverse of `LcleanCommand.py:70`:
// an empty CLI list becomes **nil**, because a non-nil empty set would mean
// "undeploy nothing".
func TestLcleanFiltersAreNilWhenEmpty(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\n"})

	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["lclean"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	if fake.undeployOpts[0].SelectedMachines != nil || fake.undeployOpts[0].ExcludedMachines != nil {
		t.Error("lclean must pass nil filters when the CLI gave none")
	}

	a2 := newTestApp(t)
	fake2 := &fakeManager{}
	withManager(a2, fake2)
	spec2 := commandTable(a2.app)["lclean"]
	if code := runCommand(t.Context(), a2.app, spec2, []string{"-d", dir, "pc1"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a2.stdoutString())
	}
	if !fake2.undeployOpts[0].SelectedMachines.Has("pc1") {
		t.Error("the positional device name did not reach the undeploy filter")
	}
}

// TestLcleanFallsBackToAPathLab is `except (Exception, IOError)`: a directory
// with no lab.conf still cleans, addressed by the hash of its path.
func TestLcleanFallsBackToAPathLab(t *testing.T) {
	dir := t.TempDir()

	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["lclean"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	if len(fake.undeployedRefs) != 1 || fake.undeployedRefs[0].Hash == "" {
		t.Errorf("undeploy ref = %+v, want a hash derived from the path", fake.undeployedRefs)
	}
	if !strings.Contains(a.stdoutString(), "│                          Stopping Network Scenario                           │") {
		t.Errorf("the panel is missing:\n%s", a.stdoutString())
	}
}

func TestLcleanLabHashAddressing(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["lclean"]

	if code := runCommand(t.Context(), a.app, spec, []string{"--lab-hash", "abc123", "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	if fake.undeployedRefs[0].Hash != "abc123" {
		t.Errorf("ref = %+v, want hash abc123", fake.undeployedRefs[0])
	}
	// A scenario addressed by identity has no path.
	if !strings.Contains(a.stdoutString(), `"path":null`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}
}

func TestLrestartXtermFailsAfterTheClean(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\n"})

	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	a.commandName = "lrestart"
	a.rawArgs = []string{"--xterm", "foo", "-d", dir}

	spec := commandTable(a.app)["lrestart"]
	code := runCommand(t.Context(), a.app, spec, a.rawArgs)

	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if len(fake.undeployedRefs) != 1 {
		t.Errorf("the clean phase did not run; expected the established behavior")
	}
	if len(fake.deployedLabs) != 0 {
		t.Errorf("the start phase must not have run")
	}
	if !strings.Contains(a.stderrString(), "unknown flag: --xterm") {
		t.Errorf("stderr = %q", a.stderrString())
	}
}

func TestVstartDryModeExitsBeforeValidation(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["vstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-n", "pc1", "--eth", "0:A", "--print"}); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, a.stdoutString())
	}
	out := a.stdoutString()
	for _, want := range []string{
		"│                            Checking Device `pc1`                             │",
		"✓ pc1 configuration is correct.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, out)
		}
	}
}

func TestVstartEthErrorSplit(t *testing.T) {
	t.Run("bad value is exit 2", func(t *testing.T) {
		a := newTestApp(t)
		spec := commandTable(a.app)["vstart"]
		code := runCommand(t.Context(), a.app, spec, []string{"-n", "pc1", "--eth", "0:A/"})
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(a.stderrString(), "invalid interface definition: 0:A/") {
			t.Errorf("stderr = %q", a.stderrString())
		}
	})

	t.Run("non-numeric interface number is exit 1", func(t *testing.T) {
		a := newTestApp(t)
		a.console.Format = cliout.FormatJSON
		withManager(a, &fakeManager{})
		spec := commandTable(a.app)["vstart"]
		code := runCommand(t.Context(), a.app, spec, []string{"-n", "pc1", "--eth", "x:A", "--format", "json"})
		if code != 1 {
			t.Fatalf("exit = %d, want 1\n%s%s", code, a.stdoutString(), a.stderrString())
		}
		if !strings.Contains(a.stdoutString(), `"code":"Syntax"`) {
			t.Errorf("stdout = %s", a.stdoutString())
		}
	})
}

func TestVstartBuildsAOneDeviceVlab(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["vstart"]

	code := runCommand(t.Context(), a.app, spec,
		[]string{"-n", "pc1", "--eth", "0:A", "1:B", "--image", "kathara/frr", "--noterminals", "--", "sleep", "1"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, a.stdoutString(), a.stderrString())
	}

	if len(fake.deployedLabs) != 1 {
		t.Fatalf("deploy count = %d", len(fake.deployedLabs))
	}
	lab := fake.deployedLabs[0]
	if lab.Name() != vlabName {
		t.Errorf("lab name = %q, want %q", lab.Name(), vlabName)
	}
	machine, err := lab.GetMachine("pc1")
	if err != nil {
		t.Fatalf("the device is missing: %v", err)
	}
	if got := machine.GetImage(); got != "kathara/frr" {
		t.Errorf("image = %q, want kathara/frr", got)
	}
	if got := len(machine.Interfaces()); got != 2 {
		t.Errorf("interface count = %d, want 2", got)
	}
	// `shared_mount` is force-disabled for vstart, unconditionally.
	if v, ok := lab.GeneralOption("shared_mount"); !ok {
		t.Error("shared_mount was not set")
	} else if b, _ := v.AsBool(); b {
		t.Error("shared_mount must be false for vstart")
	}
}

func TestWipeRequiresForceInJSONMode(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["wipe"]

	code := runCommand(t.Context(), a.app, spec, []string{"--format", "json"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if len(fake.wiped) != 0 {
		t.Error("nothing may be wiped without --force")
	}
	want := `{"error":{"code":"ConfirmationRequired","message":"Confirmation required: re-run with ` +
		"`--force`" + ` to wipe Kathara."}}` + "\n"
	if got := a.stdoutString(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestWipeForceProceeds is the other half.
func TestWipeForceProceeds(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["wipe"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-f", "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	if len(fake.wiped) != 1 || fake.wiped[0] {
		t.Errorf("wipe calls = %v, want one call with allUsers=false", fake.wiped)
	}
	if got := a.stdoutString(); got != `{"settings_wiped":false,"all_users":false,"machines":[],"links":[]}`+"\n" {
		t.Errorf("stdout = %s", got)
	}
}

func TestWipeReportsWhatWasRunning(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{
		stats: []kathara.MachineStatsEntry{
			{ID: "c2", Stats: &kathara.MachineStats{Name: "r1"}},
			{ID: "c1", Stats: &kathara.MachineStats{Name: "pc1"}},
		},
		links: []kathara.LinkStatsEntry{
			{ID: "n2", Stats: &kathara.LinkStats{Name: "B"}},
			{ID: "n1", Stats: &kathara.LinkStats{Name: "A"}},
		},
	}
	withManager(a, fake)
	spec := commandTable(a.app)["wipe"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-f", "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	want := `{"settings_wiped":false,"all_users":false,"machines":["pc1","r1"],"links":["A","B"]}` + "\n"
	if got := a.stdoutString(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestWipeDeclinedAtThePromptExitsZero is `WipeCommand.py:60`'s bare
// `sys.exit()`.
func TestWipeDeclinedAtThePromptExitsZero(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	a.prompter = &cliout.Prompter{Console: a.console, In: strings.NewReader("n\n")}
	spec := commandTable(a.app)["wipe"]

	if code := runCommand(t.Context(), a.app, spec, nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if len(fake.wiped) != 0 {
		t.Error("declining must wipe nothing")
	}
	if !strings.Contains(a.stdoutString(), "Are you sure to wipe Kathara? [y/n]: ") {
		t.Errorf("stdout = %q", a.stdoutString())
	}
}

func TestListRejectsWatchUnderMachineFormats(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &fakeManager{})
	spec := commandTable(a.app)["list"]

	code := runCommand(t.Context(), a.app, spec, []string{"-w", "--format", "json"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if a.stdoutString() != "" {
		t.Errorf("stdout = %q, want empty", a.stdoutString())
	}
}

// TestListEmptyRendersTheNoDevicesPanel is `create_lab_table`'s empty arm.
func TestListEmptyRendersTheNoDevicesPanel(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &fakeManager{})
	spec := commandTable(a.app)["list"]

	if code := runCommand(t.Context(), a.app, spec, nil); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stdoutString())
	}
	out := a.stdoutString()
	if !strings.Contains(out, "║                               No Devices Found                               ║") {
		t.Errorf("stdout:\n%s", out)
	}
}

// TestMachinesTableHeaderFollowsEachBackendsToDict is `create_lab_table`'s
// column derivation (`cli/ui/utils.py:77-83`) on both backends.
func TestMachinesTableHeaderFollowsEachBackendsToDict(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stats *kathara.MachineStats
		want  []string
	}{
		{
			name: "docker",
			stats: &kathara.MachineStats{
				NetworkScenarioID: "9pe3y6IDMwx4PfOPu5mbNg",
				Name:              "pc1",
				ContainerName:     "kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg",
				User:              ptr("user"),
				Status:            ptr("running"),
				Image:             "kathara/base",
			},
			want: []string{"NETWORK SCENARIO ID", "NAME", "USER", "STATUS", "IMAGE"},
		},
		{
			name: "kubernetes",
			stats: &kathara.MachineStats{
				NetworkScenarioID: "9pe3y6idmwx4pfopu5mbng",
				Name:              "pc1",
				ContainerName:     "pc1-6b7d9f8c4d-hq2xz",
				User:              nil,
				Status:            ptr("Running"),
				Image:             "kathara/base",
				AssignedNode:      kathara.SomeString("node-1"),
			},
			want: []string{"NETWORK SCENARIO ID", "NAME", "POD NAME", "IMAGE", "STATUS", "ASSIGNED NODE"},
		},
		{
			// A Pending pod's `assigned_node` is null, not missing: the column
			// is still there and `str(None)` fills the cell.
			name: "kubernetes, pod not scheduled yet",
			stats: &kathara.MachineStats{
				NetworkScenarioID: "9pe3y6idmwx4pfopu5mbng",
				Name:              "pc1",
				ContainerName:     "pc1-6b7d9f8c4d-hq2xz",
				Status:            ptr("Pending"),
				Image:             "N/A",
				AssignedNode:      kathara.NullString(),
			},
			want: []string{"NETWORK SCENARIO ID", "NAME", "POD NAME", "IMAGE", "STATUS", "ASSIGNED NODE"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderMachinesTable(
				[]kathara.MachineStatsEntry{{ID: tc.stats.ContainerName, Stats: tc.stats}}, 120)

			got := tableHeaderCells(t, lines)
			if !slices.Equal(got, tc.want) {
				t.Errorf("header = %q, want %q\n%s", got, tc.want, strings.Join(lines, "\n"))
			}
		})
	}
}

// tableHeaderCells reads the header row off a rendered table: the first line
// that carries a column separator after the box's top rule, split back into
// cells.
func tableHeaderCells(t *testing.T, lines []string) []string {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(line, "│") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "│"), "│")
		for i, cell := range cells {
			cells[i] = strings.TrimSpace(cell)
		}
		return cells
	}
	t.Fatalf("no header row in:\n%s", strings.Join(lines, "\n"))
	return nil
}

// ptr is `&x` for a literal.
func ptr[T any](v T) *T { return &v }

func TestExecExitCodeIsTheRemoteCommands(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &execManager{
		frames: [][2]string{{"hello\n", ""}, {"", "oops\n"}},
		code:   7,
	})
	spec := commandTable(a.app)["exec"]

	code := runCommand(t.Context(), a.app, spec, []string{"--lab-hash", "h", "pc1", "ls"})
	if code != 7 {
		t.Fatalf("exit = %d, want 7", code)
	}
	if a.stdoutString() != "hello\n" {
		t.Errorf("stdout = %q", a.stdoutString())
	}
	if a.stderrString() != "oops\n" {
		t.Errorf("stderr = %q", a.stderrString())
	}
}

func TestExecJSONAggregatesTheStreams(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &execManager{frames: [][2]string{{"a", ""}, {"b", "e"}}, code: 0})
	spec := commandTable(a.app)["exec"]

	if code := runCommand(t.Context(), a.app, spec,
		[]string{"--lab-hash", "h", "--format", "json", "pc1", "ls"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := a.stdoutString(); got != `{"stdout":"ab","stderr":"e","exit_code":0}`+"\n" {
		t.Errorf("stdout = %s", got)
	}
}

func TestExecJSONLEmitsOneEventPerNonEmptySide(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &execManager{
		frames: [][2]string{{"a", ""}, {"", ""}, {"", "e"}},
		code:   0,
	})
	spec := commandTable(a.app)["exec"]

	if code := runCommand(t.Context(), a.app, spec,
		[]string{"--lab-hash", "h", "--format", "jsonl", "pc1", "ls"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	want := `{"type":"stdout","data":"a"}` + "\n" +
		`{"type":"stderr","data":"e"}` + "\n" +
		`{"type":"exit","code":0}` + "\n"
	if got := a.stdoutString(); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestExecSuppressionFlags are `--no-stdout` / `--no-stderr`.
func TestExecSuppressionFlags(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &execManager{frames: [][2]string{{"out", "err"}}, code: 0})
	spec := commandTable(a.app)["exec"]

	if code := runCommand(t.Context(), a.app, spec,
		[]string{"--lab-hash", "h", "--no-stdout", "--format", "json", "pc1", "ls"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if got := a.stdoutString(); got != `{"stdout":"","stderr":"err","exit_code":0}`+"\n" {
		t.Errorf("stdout = %s", got)
	}
}

// TestExecCommandPayloadShape is command argument behavior: one token becomes a bare string
// the backend shlex-splits, several stay a list.
func TestExecCommandPayloadShape(t *testing.T) {
	tests := []struct {
		args      []string
		wantShell string
		wantArgv  []string
	}{
		{args: []string{"pc1", "ls -la"}, wantShell: "ls -la"},
		// `kathara exec pc1 ls -la` is exit 2 in Python too ("unrecognized
		// arguments: -la"); the separator is how a multi-word command is
		// spelled.
		{args: []string{"pc1", "--", "ls", "-la"}, wantArgv: []string{"ls", "-la"}},
	}
	for _, tc := range tests {
		a := newTestApp(t)
		m := &execManager{frames: nil, code: 0}
		withManager(a, m)
		spec := commandTable(a.app)["exec"]
		full := append([]string{"--lab-hash", "h"}, tc.args...)
		if code := runCommand(t.Context(), a.app, spec, full); code != 0 {
			t.Fatalf("exit = %d (%s)", code, a.stderrString())
		}
		if tc.wantShell != "" {
			line, ok := m.command.Line()
			if !ok || line != tc.wantShell {
				t.Errorf("command = %v/%q, want the string %q", ok, line, tc.wantShell)
			}
			continue
		}
		argv, ok := m.command.Argv()
		if !ok || !equalStrings(argv, tc.wantArgv) {
			t.Errorf("command = %v/%q, want the list %q", ok, argv, tc.wantArgv)
		}
	}
}

// execManager is a fake whose ExecStream replays a fixed set of frames.
type execManager struct {
	kathara.Manager
	frames  [][2]string
	code    int
	command kathara.Command
}

func (m *execManager) ExecStream(_ context.Context, _ string, command kathara.Command, _ kathara.LabRef, _ kathara.WaitPolicy) (kathara.ExecStream, error) {
	m.command = command
	return &fakeExecStream{frames: m.frames, code: m.code}, nil
}

type fakeExecStream struct {
	frames [][2]string
	i      int
	code   int
}

func (s *fakeExecStream) Next(context.Context) ([]byte, []byte, error) {
	if s.i >= len(s.frames) {
		return nil, nil, io.EOF
	}
	frame := s.frames[s.i]
	s.i++
	return []byte(frame[0]), []byte(frame[1]), nil
}

func (s *fakeExecStream) ExitCode(context.Context) (int, error) { return s.code, nil }
func (s *fakeExecStream) Close() error                          { return nil }

func TestUTF8DecoderCarriesPartialSequences(t *testing.T) {
	d := &utf8Decoder{}
	// "é" is C3 A9.
	if got := d.decode([]byte{0xC3}); got != "" {
		t.Errorf("a partial sequence must be held back, got %q", got)
	}
	if got := d.decode([]byte{0xA9, 'x'}); got != "éx" {
		t.Errorf("decode = %q, want %q", got, "éx")
	}
	if got := d.flush(); got != "" {
		t.Errorf("flush = %q, want empty", got)
	}

	// Two bytes that can never begin a sequence are two maximal subparts, so
	// two replacements — not the one a run-collapsing decoder would give.
	// TestUTF8DecoderReplacesMaximalSubparts has the rest of the oracle table.
	d2 := &utf8Decoder{}
	if got := d2.decode([]byte{0xFF, 0xFE}); got != "\uFFFD\uFFFD" {
		t.Errorf("invalid bytes must become U+FFFD, got %q", got)
	}

	d3 := &utf8Decoder{}
	d3.decode([]byte{0xC3})
	if got := d3.flush(); got != "\uFFFD" {
		t.Errorf("an incomplete sequence at end of stream = %q, want U+FFFD", got)
	}
}

func TestArchiveExtraction(t *testing.T) {
	t.Run("a plain scenario extracts at the archive root", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		writeTarFile(t, tw, "lab.conf", "pc1[0]=\"A\"\n")
		writeTarFile(t, tw, "./pc1/etc/hosts", "127.0.0.1 pc1\n")
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		if err := extractScenarioArchive(&buf, dir); err != nil {
			t.Fatalf("extract: %v", err)
		}
		for _, want := range []string{"lab.conf", filepath.Join("pc1", "etc", "hosts")} {
			if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
				t.Errorf("%s missing: %v", want, err)
			}
		}
	})

	t.Run("traversal and absolute paths are refused", func(t *testing.T) {
		for _, name := range []string{"../escape", "/etc/passwd"} {
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			writeTarFile(t, tw, name, "x")
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			err := extractScenarioArchive(&buf, t.TempDir())
			if err == nil || kerrors.Code(err) != kerrors.CodeInvocation {
				t.Errorf("%s: err = %v, want an Invocation error", name, err)
			}
		}
	})

	t.Run("a symlink entry is refused", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := extractScenarioArchive(&buf, t.TempDir()); err == nil {
			t.Error("a symlink must be refused")
		}
	})

	t.Run("a non-tar stream is an OS error", func(t *testing.T) {
		err := extractScenarioArchive(strings.NewReader("not a tar"), t.TempDir())
		if err == nil || kerrors.Code(err) != kerrors.CodeOS {
			t.Errorf("err = %v, want an OS error", err)
		}
	})
}

func writeTarFile(t *testing.T, tw *tar.Writer, name, content string) {
	t.Helper()
	hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
}

func TestFromArchiveUsageRules(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{args: []string{"--from-archive", "-"}, want: "required with --from-archive"},
		{args: []string{"--from-archive", "file.tar", "--name", "x"}, want: "only `-` is accepted"},
		{args: []string{"--name", "x"}, want: "only valid together with --from-archive"},
		{args: []string{"--from-archive", "-", "--name", "x", "-d", "/tmp"},
			want: "not allowed with argument -d/--directory"},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["lstart"]
			if code := runCommand(t.Context(), a.app, spec, tc.args); code != 2 {
				t.Fatalf("exit = %d, want 2\n%s%s", code, a.stdoutString(), a.stderrString())
			}
			if !strings.Contains(a.stderrString(), tc.want) {
				t.Errorf("stderr = %q, want it to mention %q", a.stderrString(), tc.want)
			}
		})
	}
}

func TestFromArchiveNameOverridesLabName(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	writeTarFile(t, tw, "lab.conf", "LAB_NAME=\"from the file\"\npc1[0]=\"A\"\n")
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	a := newTestApp(t)
	a.stdin = &buf
	fake := &fakeManager{}
	withManager(a, fake)
	a.console.Format = cliout.FormatJSON
	spec := commandTable(a.app)["lstart"]

	code := runCommand(t.Context(), a.app, spec,
		[]string{"--from-archive", "-", "--name", "Default scenario", "--format", "json"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, a.stdoutString(), a.stderrString())
	}
	out := a.stdoutString()
	if !strings.Contains(out, `"name":"Default scenario"`) {
		t.Errorf("--name did not win: %s", out)
	}
	if !strings.Contains(out, `"hash":"9pe3y6IDMwx4PfOPu5mbNg"`) {
		t.Errorf("the hash was not recomputed from --name: %s", out)
	}
	if !strings.Contains(out, `"path":null`) {
		t.Errorf("an archive deploy must report a null path: %s", out)
	}
}

// TestManagerConstructionFailurePropagates covers the eager-construction
// timing: a daemon that is not running fails the command, not the parse.
func TestManagerConstructionFailurePropagates(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\n"})

	a := newTestApp(t)
	a.newManager = func(context.Context) (kathara.Manager, error) {
		return nil, kerrors.NewDaemonConnection(errors.New("dial unix: no such file"))
	}
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "--format", "json"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(a.stdoutString(), `"code":"DockerDaemonConnection"`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}
}

func TestLcleanReportsRunningLinks(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "pc1[0]=\"A\"\npc2[0]=\"B\"\n",
	})
	newFake := func() *fakeManager {
		return &fakeManager{
			stats: []kathara.MachineStatsEntry{
				{ID: "c2", Stats: &kathara.MachineStats{Name: "pc2"}},
				{ID: "c1", Stats: &kathara.MachineStats{Name: "pc1"}},
			},
			links: []kathara.LinkStatsEntry{
				{ID: "n2", Stats: &kathara.LinkStats{Name: "B"}},
				{ID: "n1", Stats: &kathara.LinkStats{Name: "A"}},
			},
		}
	}

	t.Run("no filter reports everything", func(t *testing.T) {
		a := newTestApp(t)
		withManager(a, newFake())
		spec := commandTable(a.app)["lclean"]
		if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "--format", "json"}); code != 0 {
			t.Fatalf("exit = %d\n%s", code, a.stderrString())
		}
		if !strings.Contains(a.stdoutString(), `"machines":["pc1","pc2"],"links":["A","B"]`) {
			t.Errorf("stdout = %s", a.stdoutString())
		}
	})

	t.Run("a device filter narrows both arrays", func(t *testing.T) {
		a := newTestApp(t)
		withManager(a, newFake())
		spec := commandTable(a.app)["lclean"]
		if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "--format", "json", "pc1"}); code != 0 {
			t.Fatalf("exit = %d\n%s", code, a.stderrString())
		}
		if !strings.Contains(a.stdoutString(), `"machines":["pc1"],"links":["A"]`) {
			t.Errorf("stdout = %s", a.stdoutString())
		}
	})
}

func TestLstartListSortsStatsByName(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\nr1[0]=\"A\"\n"})

	a := newTestApp(t)
	withManager(a, &fakeManager{
		stats: []kathara.MachineStatsEntry{
			{ID: "kathara_u_r1_h", Stats: &kathara.MachineStats{Name: "r1", Image: "i"}},
			{ID: "kathara_u_pc1_h", Stats: &kathara.MachineStats{Name: "pc1", Image: "i"}},
		},
	})
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "-l", "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	out := a.stdoutString()
	first := strings.Index(out, `"name":"pc1"`)
	second := strings.Index(out, `"name":"r1"`)
	if first < 0 || second < 0 || first > second {
		t.Errorf("machine_stats is not sorted by device name: %s", out)
	}
}

func TestLstartDeployedNamesUseScheduleOrderAndSortedLinks(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "r1[0]=\"B\"\npc1[0]=\"A\"\npc2[0]=\"A\"\n",
	})

	a := newTestApp(t)
	withManager(a, &fakeManager{})
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	if !strings.Contains(a.stdoutString(), `"machines":["r1","pc1","pc2"],"links":["A","B"]`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}
}

// TestLstartDependenciesReorderTheSchedule is `lab.dep`'s whole point, and the
// order the envelope reports.
func TestLstartDependenciesReorderTheSchedule(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "pc1[0]=\"A\"\npc2[0]=\"A\"\n",
		"lab.dep":  "pc1: pc2\n",
	})

	a := newTestApp(t)
	withManager(a, &fakeManager{})
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	if !strings.Contains(a.stdoutString(), `"machines":["pc2","pc1"]`) {
		t.Errorf("lab.dep did not reorder the schedule: %s", a.stdoutString())
	}
}

// TestLstartForceLabOnlyRescuesIOErrors is `except IOError` at
// `LstartCommand.py:169`: `-F` falls back to the folder parser for a MISSING
// lab.conf, and does nothing at all for a malformed one.
func TestLstartForceLabOnlyRescuesIOErrors(t *testing.T) {
	t.Run("missing lab.conf falls back to the folder parser", func(t *testing.T) {
		dir := scenarioDir(t, map[string]string{"pc1/.keep": ""})
		a := newTestApp(t)
		withManager(a, &fakeManager{})
		spec := commandTable(a.app)["lstart"]
		if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "-F", "--format", "json"}); code != 0 {
			t.Fatalf("exit = %d\n%s%s", code, a.stdoutString(), a.stderrString())
		}
		if !strings.Contains(a.stdoutString(), `"machines":["pc1"]`) {
			t.Errorf("stdout = %s", a.stdoutString())
		}
	})

	t.Run("a malformed lab.conf is not rescued", func(t *testing.T) {
		dir := scenarioDir(t, map[string]string{"lab.conf": "not a valid line\n"})
		a := newTestApp(t)
		withManager(a, &fakeManager{})
		spec := commandTable(a.app)["lstart"]
		if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "-F", "--format", "json"}); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(a.stdoutString(), `"code":"Syntax"`) {
			t.Errorf("stdout = %s", a.stdoutString())
		}
	})
}

// TestLstartGlobalMetadata is `-o/--pass` → `OptionParser` →
// `lab.global_machine_metadata`, whose six known keys beat a device's own meta.
func TestLstartGlobalMetadata(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\npc1[image]=\"kathara/base\"\n"})

	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["lstart"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "-o", "image=kathara/frr"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	machine, err := fake.deployedLabs[0].GetMachine("pc1")
	if err != nil {
		t.Fatal(err)
	}
	if got := machine.GetImage(); got != "kathara/frr" {
		t.Errorf("image = %q, want the global metadata to win", got)
	}
}

func TestLconfigAddOrderIsSemantic(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\n"})

	a := newTestApp(t)
	withManager(a, &configManager{})
	spec := commandTable(a.app)["lconfig"]

	code := runCommand(t.Context(), a.app, spec,
		[]string{"-d", dir, "-n", "pc1", "--add", "Z", "B/00:11:22:33:44:55", "--format", "json"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, a.stdoutString(), a.stderrString())
	}
	if !strings.Contains(a.stdoutString(),
		`"added":[{"link":"Z","mac":null},{"link":"B","mac":"00:11:22:33:44:55"}],"removed":[]`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}
}

// TestLconfigCdMacTrap checks that a malformed `--add` value becomes an error
// envelope, while a
// malformed `--rm` value is a usage error.
func TestLconfigCdMacTrap(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\n"})

	t.Run("--add is exit 1 with code Syntax", func(t *testing.T) {
		a := newTestApp(t)
		withManager(a, &configManager{})
		spec := commandTable(a.app)["lconfig"]
		code := runCommand(t.Context(), a.app, spec,
			[]string{"-d", dir, "-n", "pc1", "--add", "A/b/c", "--format", "json"})
		if code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(a.stdoutString(), `"code":"Syntax"`) {
			t.Errorf("stdout = %s", a.stdoutString())
		}
	})

	t.Run("--rm is exit 2 with usage text", func(t *testing.T) {
		a := newTestApp(t)
		withManager(a, &configManager{})
		spec := commandTable(a.app)["lconfig"]
		code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "-n", "pc1", "--rm", "a-b"})
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(a.stderrString(), "invalid alphanumeric value") {
			t.Errorf("stderr = %q", a.stderrString())
		}
	})
}

// configManager is the fake `lconfig`/`vconfig` drive.
type configManager struct {
	kathara.Manager
	connected    []string
	disconnected []string
}

func (m *configManager) UpdateLabFromAPI(context.Context, *model.Lab) error { return nil }

func (m *configManager) ConnectMachineToLink(_ context.Context, _ *model.Machine, link *model.Link, _ string) error {
	m.connected = append(m.connected, link.Name)
	return nil
}

func (m *configManager) DisconnectMachineFromLink(_ context.Context, _ *model.Machine, link *model.Link, _ bool) error {
	m.disconnected = append(m.disconnected, link.Name)
	return nil
}

func (m *configManager) GetMachineAPIObject(context.Context, string, kathara.LabRef, bool) (any, error) {
	return struct{}{}, nil
}

func (m *configManager) GetLinkAPIObject(context.Context, string, kathara.LabRef, bool) (any, error) {
	return struct{}{}, nil
}

func TestVconfigFetchesTheAPIObjects(t *testing.T) {
	a := newTestApp(t)
	m := &configManager{}
	withManager(a, m)
	spec := commandTable(a.app)["vconfig"]

	// `update_lab_from_api` is faked as a no-op, so the device has to be there
	// already; a vlab starts empty, which is exactly the MachineNotFound path.
	code := runCommand(t.Context(), a.app, spec, []string{"-n", "pc1", "--add", "A", "--format", "json"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (the device is not in the reconstructed vlab)", code)
	}
	if !strings.Contains(a.stdoutString(), `"code":"MachineNotFound"`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}
}

// TestVcleanUndeploysFromTheVlab is `VcleanCommand.py:46`.
func TestVcleanUndeploysFromTheVlab(t *testing.T) {
	a := newTestApp(t)
	fake := &fakeManager{}
	withManager(a, fake)
	spec := commandTable(a.app)["vclean"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-n", "pc1"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	if fake.undeployedRefs[0].Name != vlabName {
		t.Errorf("ref = %+v, want the vlab by name", fake.undeployedRefs[0])
	}
	if !fake.undeployOpts[0].SelectedMachines.Has("pc1") {
		t.Error("the device filter is missing")
	}
	if !strings.Contains(a.stdoutString(), "│                            Stopping Device `pc1`                             │") {
		t.Errorf("stdout:\n%s", a.stdoutString())
	}
}

func TestLrestartJSONWritesOneObject(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "pc1[0]=\"A\"\n"})
	fake := &fakeManager{
		stats: []kathara.MachineStatsEntry{{ID: "c1", Stats: &kathara.MachineStats{Name: "pc1"}}},
		links: []kathara.LinkStatsEntry{{ID: "n1", Stats: &kathara.LinkStats{Name: "A"}}},
	}

	a := newTestApp(t)
	withManager(a, fake)
	a.app.rawArgs = []string{"-d", dir, "--format", "json"}
	spec := commandTable(a.app)["lrestart"]
	if code := runCommand(t.Context(), a.app, spec, a.app.rawArgs); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}

	out := a.stdoutString()
	if !strings.HasPrefix(out, "{") {
		t.Fatalf("stdout does not start with the envelope:\n%q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("stdout is not exactly one object:\n%q", out)
	}
	if !strings.Contains(out, `"machines":["pc1"],"links":["A"]`) {
		t.Errorf("the clean phase reported nothing:\n%s", out)
	}
	// The console is back to json for the caller.
	if a.app.console.Format != cliout.FormatJSON {
		t.Errorf("console format = %q, want json", a.app.console.Format)
	}
}

func TestInterruptSwallowsTheCommandError(t *testing.T) {
	for _, format := range []cliout.Format{cliout.FormatHuman, cliout.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			a := newTestApp(t)
			a.console.Format = format
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			spec := &commandSpec{
				Name: "lstart",
				Cmd:  newParser("lstart"),
				Run: func(ctx context.Context, _ *app, _, _ []string) (int, error) {
					return 1, ctx.Err()
				},
			}
			if code := runCommand(ctx, a.app, spec, nil); code != 1 {
				t.Fatalf("exit = %d, want 1 (finish turns it into 0)", code)
			}
			if out := a.stdoutString(); out != "" {
				t.Errorf("stdout = %q, want empty: the error must not be rendered", out)
			}
			if errOut := a.stderrString(); errOut != "" {
				t.Errorf("stderr = %q, want empty", errOut)
			}

			a.commandName = "lstart"
			if code := a.finish(1, true); code != 0 {
				t.Errorf("finish = %d, want 0", code)
			}
			if format == cliout.FormatJSON && a.stdoutString() != `{"interrupted":true}`+"\n" {
				t.Errorf("stdout = %q, want only the interrupt envelope", a.stdoutString())
			}
		})
	}
}

// TestWipeAllSnapshotsAllUsers checks that `links` honors `-a` and the
// wipe itself does, so `machines` has to as well — otherwise a root all-users
// wipe reports only the caller's own devices.
func TestWipeAllSnapshotsAllUsers(t *testing.T) {
	// `wipe -a` refuses below root before it reaches the manager — that check
	// is itself matching behavior (`WipeCommand.py`), so a non-root run cannot
	// exercise the all-users snapshot. CI runners are non-root; the root VM
	// this suite was written on runs it.
	if os.Geteuid() != 0 {
		t.Skip("wipe -a requires root; skipping on non-root runner")
	}
	fake := &statsCallRecorder{}
	a := newTestApp(t)
	withManager(a, fake)
	a.console.Format = cliout.FormatJSON

	f := &wipeFlags{force: true, all: true}
	if code, err := runWipe(t.Context(), a.app, f); err != nil || code != 0 {
		t.Fatalf("runWipe = (%d, %v)", code, err)
	}
	if len(fake.machineAllUsers) != 1 || !fake.machineAllUsers[0] {
		t.Errorf("GetMachinesStats allUsers = %v, want [true]", fake.machineAllUsers)
	}
	if len(fake.linkAllUsers) != 1 || !fake.linkAllUsers[0] {
		t.Errorf("GetLinksStats allUsers = %v, want [true]", fake.linkAllUsers)
	}
}

// statsCallRecorder records the allUsers argument of the two stats calls.
type statsCallRecorder struct {
	fakeManager
	machineAllUsers []bool
	linkAllUsers    []bool
}

func (r *statsCallRecorder) GetMachinesStats(_ context.Context, _ kathara.LabRef, _ string, allUsers bool) (kathara.MachinesStatsStream, error) {
	r.machineAllUsers = append(r.machineAllUsers, allUsers)
	return &fakeStatsStream{}, nil
}

func (r *statsCallRecorder) GetLinksStats(_ context.Context, _ kathara.LabRef, _ string, allUsers bool) (kathara.LinksStatsStream, error) {
	r.linkAllUsers = append(r.linkAllUsers, allUsers)
	return &fakeLinksStream{}, nil
}

func TestListStreamEndIsACleanExit(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &exhaustedManager{})
	a.console.Format = cliout.FormatJSON

	spec := commandTable(a.app)["list"]
	if code := runCommand(t.Context(), a.app, spec, []string{"--format", "json"}); code != 0 {
		t.Fatalf("exit = %d, want 0 (stdout %q, stderr %q)", code, a.stdoutString(), a.stderrString())
	}
	if got := a.stdoutString(); got != `{"machines":[]}`+"\n" {
		t.Errorf("stdout = %q, want the empty inventory envelope", got)
	}
}

// exhaustedManager's stats generator has already ended.
type exhaustedManager struct{ fakeManager }

func (m *exhaustedManager) GetMachinesStats(context.Context, kathara.LabRef, string, bool) (kathara.MachinesStatsStream, error) {
	return &fakeStatsStream{done: true}, nil
}

// TestUTF8DecoderReplacesMaximalSubparts pins the decoder against CPython's
// `bytes.decode('utf-8', 'replace')`, which replaces one *maximal subpart* and
// not one byte: a truncated three-byte lead is a single U+FFFD, while two
// stray continuation bytes are two.
func TestUTF8DecoderReplacesMaximalSubparts(t *testing.T) {
	// Every row was measured against the CPython oracle.
	tests := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xE2, 0x82}, "�"},
		{[]byte{0xE2, 0x82, 'A'}, "�A"},
		{[]byte{0xF0, 0x9F, 0x98}, "�"},
		{[]byte{0xF0, 0x9F, 0x98, 'A'}, "�A"},
		{[]byte{0xFF, 0xFF}, "��"},
		{[]byte{0xE2, 0x82, 0xAC}, "€"},
		{[]byte{0xC0, 0xAF}, "��"},
		{[]byte{0xED, 0xA0, 0x80}, "���"},
		{[]byte{0xF5, 0x80, 0x80, 0x80}, "����"},
		{[]byte{0x80}, "�"},
		{[]byte{0xE0, 0x80, 0x80}, "���"},
		{[]byte{0xE0, 0x80}, "��"},
		{[]byte{0xF4, 0x90}, "��"},
		{[]byte{0xF4, 0x8F}, "�"},
		{[]byte{0xE2, 0x82, 0xE2, 0x82, 0xAC}, "�€"},
		{[]byte{0xC2}, "�"},
	}
	for _, tc := range tests {
		t.Run(string(tc.want), func(t *testing.T) {
			d := &utf8Decoder{}
			got := d.decode(tc.in) + d.flush()
			if got != tc.want {
				t.Errorf("decode(% x) = %q, want %q", tc.in, got, tc.want)
			}
			// The same bytes fed one at a time must decode identically: the
			// carry-over is what a chunked transport needs.
			d2 := &utf8Decoder{}
			var b strings.Builder
			for _, one := range tc.in {
				b.WriteString(d2.decode([]byte{one}))
			}
			b.WriteString(d2.flush())
			if b.String() != tc.want {
				t.Errorf("byte-at-a-time decode(% x) = %q, want %q", tc.in, b.String(), tc.want)
			}
		})
	}
}

func TestLcleanKeepsLinksHeldByASurvivingDevice(t *testing.T) {
	dir := scenarioDir(t, map[string]string{
		"lab.conf": "pc1[0]=\"A\"\npc2[0]=\"A\"\npc1[1]=\"B\"\n",
	})
	a := newTestApp(t)
	withManager(a, &fakeManager{
		stats: []kathara.MachineStatsEntry{
			{ID: "c1", Stats: &kathara.MachineStats{Name: "pc1"}},
			{ID: "c2", Stats: &kathara.MachineStats{Name: "pc2"}},
		},
		links: []kathara.LinkStatsEntry{
			{ID: "n1", Stats: &kathara.LinkStats{Name: "A"}},
			{ID: "n2", Stats: &kathara.LinkStats{Name: "B"}},
		},
	})
	spec := commandTable(a.app)["lclean"]
	if code := runCommand(t.Context(), a.app, spec, []string{"-d", dir, "--format", "json", "pc1"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	// `A` still has pc2 on it; only `B` goes.
	if !strings.Contains(a.stdoutString(), `"machines":["pc1"],"links":["B"]`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}
}

// TestVstartEmptyStringOptionsAreStillPresent is `Machine.update_meta`'s
// `is not None`: an explicitly empty value is a value, so `--num_terms ""`
// records an empty `num_terms` meta — which `GetNumTerms` then rejects — where
// an absent `--num_terms` records nothing at all. Telling the two apart by
// emptiness silently drops the first.
func TestVstartEmptyStringOptionsAreStillPresent(t *testing.T) {
	run := func(t *testing.T, args []string) *model.Machine {
		t.Helper()
		a := newTestApp(t)
		fake := &fakeManager{}
		withManager(a, fake)
		spec := commandTable(a.app)["vstart"]
		if code := runCommand(t.Context(), a.app, spec, args); code != 0 {
			t.Fatalf("exit = %d\n%s", code, a.stderrString())
		}
		if len(fake.deployedLabs) != 1 {
			t.Fatalf("deploys = %d, want 1", len(fake.deployedLabs))
		}
		machine, err := fake.deployedLabs[0].GetMachine("pc1")
		if err != nil {
			t.Fatal(err)
		}
		return machine
	}

	t.Run("an empty value is recorded", func(t *testing.T) {
		machine := run(t, []string{"-n", "pc1", "--num_terms", "", "--image", ""})
		if !machine.Meta.NumTerms.IsSet() {
			t.Error("num_terms meta is absent; --num_terms \"\" must set it")
		}
		if _, err := machine.GetNumTerms(); err == nil {
			t.Error("GetNumTerms = nil error, want MachineOptionError for an empty value")
		}
		if !machine.Meta.Image.IsSet() {
			t.Error("image meta is absent; --image \"\" must set it")
		}
	})

	t.Run("an absent value is not", func(t *testing.T) {
		machine := run(t, []string{"-n", "pc1"})
		if machine.Meta.NumTerms.IsSet() {
			t.Error("num_terms meta is set without --num_terms")
		}
		if n, err := machine.GetNumTerms(); err != nil || n != 1 {
			t.Errorf("GetNumTerms = (%d, %v), want (1, nil)", n, err)
		}
	})
}

func TestFromArchiveExtractionDirectoryLifetime(t *testing.T) {
	// run drives one archive deploy with its own private TMPDIR, and answers
	// the extraction directories still on disk when the command returned.
	run := func(t *testing.T, fake kathara.Manager, labConf string, args ...string) (int, []string) {
		t.Helper()
		tmp := t.TempDir()
		// os.MkdirTemp("", …) reads os.TempDir(), which is TMPDIR on unix and
		// TMP/TEMP on Windows.
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			t.Setenv(key, tmp)
		}

		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		writeTarFile(t, tw, "lab.conf", labConf)
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}

		a := newTestApp(t)
		a.stdin = &buf
		withManager(a, fake)
		spec := commandTable(a.app)["lstart"]
		code := runCommand(t.Context(), a.app, spec, append([]string{
			"--from-archive", "-", "--name", "Archive scenario", "--format", "json",
		}, args...))

		left, err := filepath.Glob(filepath.Join(tmp, "kathara-archive-*"))
		if err != nil {
			t.Fatal(err)
		}
		return code, left
	}

	t.Run("a successful deploy keeps it", func(t *testing.T) {
		code, left := run(t, &fakeManager{}, "pc1[0]=\"A\"\n")
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if len(left) != 1 {
			t.Fatalf("extraction directories left = %v, want exactly one for the run", left)
		}
		// It is the lab's host path, so the scenario has to still be in it.
		if _, err := os.Stat(filepath.Join(left[0], "lab.conf")); err != nil {
			t.Errorf("lab.conf is gone from the live lab's host path: %v", err)
		}
	})

	t.Run("--dry-mode removes it", func(t *testing.T) {
		code, left := run(t, &fakeManager{}, "pc1[0]=\"A\"\n", "--dry-mode")
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if len(left) != 0 {
			t.Errorf("extraction directories left = %v, want none after a dry run", left)
		}
	})

	t.Run("a failed deploy removes it", func(t *testing.T) {
		fake := &fakeManager{deployErr: errTestf("the daemon said no")}
		code, left := run(t, fake, "pc1[0]=\"A\"\n")
		if code == 0 {
			t.Fatal("exit = 0, want a failure")
		}
		if len(left) != 0 {
			t.Errorf("extraction directories left = %v, want none when nothing deployed", left)
		}
	})

	t.Run("a scenario with no devices removes it", func(t *testing.T) {
		code, left := run(t, &fakeManager{}, "LAB_DESCRIPTION=\"empty\"\n")
		if code == 0 {
			t.Fatal("exit = 0, want the empty-scenario failure")
		}
		if len(left) != 0 {
			t.Errorf("extraction directories left = %v, want none when nothing deployed", left)
		}
	})
}

func TestLabHashAddressingReportsANullName(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &fakeManager{})
	spec := commandTable(a.app)["lclean"]

	code := runCommand(t.Context(), a.app, spec, []string{"--lab-hash", "abc123", "--format", "json"})
	if code != 0 {
		t.Fatalf("exit = %d\n%s%s", code, a.stdoutString(), a.stderrString())
	}
	want := `"lab":{"name":null,"hash":"abc123","path":null}`
	if !strings.Contains(a.stdoutString(), want) {
		t.Errorf("stdout = %s\nwant it to contain %s", a.stdoutString(), want)
	}
}

func TestWipeDoesNotCollapseSameNamedCollisionDomains(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &fakeManager{
		stats: []kathara.MachineStatsEntry{
			{ID: "c1", Stats: &kathara.MachineStats{Name: "pc1"}},
			{ID: "c2", Stats: &kathara.MachineStats{Name: "pc1"}},
		},
		links: []kathara.LinkStatsEntry{
			{ID: "n1", Stats: &kathara.LinkStats{Name: "A"}},
			{ID: "n2", Stats: &kathara.LinkStats{Name: "A"}},
			{ID: "n3", Stats: &kathara.LinkStats{Name: "B"}},
		},
	})
	spec := commandTable(a.app)["wipe"]

	if code := runCommand(t.Context(), a.app, spec, []string{"-f", "--format", "json"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	want := `{"settings_wiped":false,"all_users":false,"machines":["pc1","pc1"],"links":["A","A","B"]}` + "\n"
	if got := a.stdoutString(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

// TestCheckReportExpandsTabsLikeRich pins the five report labels to the columns
// rich puts them in.
func TestCheckReportExpandsTabsLikeRich(t *testing.T) {
	a := newTestApp(t)
	withManager(a, &fakeCheckManager{name: "Docker (Kathara)", release: "29.7.1"})
	spec := commandTable(a.app)["check"]

	if code := runCommand(t.Context(), a.app, spec, nil); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}

	out := a.stdoutString()
	if strings.ContainsRune(out, '\t') {
		t.Errorf("the report emitted a raw tab; rich never does:\n%q", out)
	}
	for _, label := range []string{
		"Current Manager is:", "Manager version is:", "Go version is:",
		"Kathara version is:", "Operating System version is:",
	} {
		line := reportLine(t, out, label)
		value := strings.TrimLeft(line[len(label):], " ")
		if column := len(line) - len(value); column != 32 {
			t.Errorf("%q value starts at column %d, want 32 (line %q)", label, column, line)
		}
	}
}

// reportLine finds the `check` report line beginning with label.
func reportLine(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, label) {
			return line
		}
	}
	t.Fatalf("no %q line in:\n%s", label, out)
	return ""
}

// TestExpandTabsIsRichsRule covers the stop arithmetic itself, including the
// case a tab lands exactly on a stop and must still advance a full eight.
func TestExpandTabsIsRichsRule(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"no tabs", "no tabs"},
		{"a\tb", "a       b"},
		{"1234567\tb", "1234567 b"},
		{"12345678\tb", "12345678        b"},
		{"Operating System version is:\tX", "Operating System version is:    X"},
		{"a\tb\nc\td", "a       b\nc       d"},
	} {
		if got := expandTabs(tc.in); got != tc.want {
			t.Errorf("expandTabs(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeCheckManager answers the four calls `check` makes.
type fakeCheckManager struct {
	kathara.Manager

	name    string
	release string
}

func (f *fakeCheckManager) GetFormattedManagerName() string { return f.name }

func (f *fakeCheckManager) GetReleaseVersion(context.Context) (string, error) {
	return f.release, nil
}

func (f *fakeCheckManager) DeployMachine(context.Context, *model.Machine) error { return nil }

func (f *fakeCheckManager) UndeployMachine(context.Context, *model.Machine, bool) error { return nil }
