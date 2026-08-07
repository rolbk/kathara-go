package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// fakeManager records what a command asked the backend to do.
//
// The embedded nil interface is deliberate: a command that reaches a method
// this fake does not implement panics with a nil dereference, which fails the
// test loudly instead of silently exercising a path nobody asserted.
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

// TestLstartDryModeReturnsBeforeDeploy is CLI_SURFACE.md §1 step 10.
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

// TestLstartLabExtIsDeferred is ERROR_CODES.md §5: the presence of the file is
// reported BEFORE the root and platform gates Python applies, and before the
// dry-mode return.
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

// TestLstartSelectionFiltersAreAlwaysSets is JSON_CLI_CONTRACT.md A11: lstart
// passes possibly-empty sets, never nil, because the deploy path truthiness-
// tests them.
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

// TestLcleanFiltersAreNilWhenEmpty is the inverse (A11, `LcleanCommand.py:70`):
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

// TestLcleanLabHashAddressing is JSON_CLI_CONTRACT.md §8.
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

// TestLrestartXtermReproducesThePythonBug is CLI_SURFACE.md §3's "latent code
// bug (port as-is)": `--xterm` passes lrestart's parser, the clean runs, and
// then lstart's parser rejects it — exit 2 with the scenario already down.
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
		t.Errorf("the clean phase did not run; that is the bug being reproduced")
	}
	if len(fake.deployedLabs) != 0 {
		t.Errorf("the start phase must not have run")
	}
	if !strings.Contains(a.stderrString(), "unknown flag: --xterm") {
		t.Errorf("stderr = %q", a.stderrString())
	}
}

// TestVstartDryModeExitsBeforeValidation is CLI_SURFACE.md §6 step 2: the
// checkmark prints even for flags that could never deploy.
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

// TestVstartNonNumericEthIsASyntaxError is §3.8: a malformed `--eth` VALUE is a
// usage error (exit 2), but a non-numeric interface NUMBER is a `SyntaxError`
// that escapes argparse (exit 1).
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

// TestVstartBuildsAOneDeviceVlab is PORT_SPEC §0.2 #3: the sugar constructs a
// `kathara_vlab` scenario and hands it to the same DeployLab the l-family uses.
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

// TestWipeRequiresForceInJSONMode is JSON_CLI_CONTRACT.md §1.5 and §3.4.
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

// TestWipeReportsWhatWasRunning is §3.4's "canonically sorted names of what was
// removed".
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

// TestListRejectsWatchUnderMachineFormats is §1.1 / A13.
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

// TestExecExitCodeIsTheRemoteCommands is JSON_CLI_CONTRACT.md A4, and the one
// place a Kathara command exits non-zero on success.
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

// TestExecJSONAggregatesTheStreams is §3.6.
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

// TestExecJSONLEmitsOneEventPerNonEmptySide is §4.2's demux ruling.
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

// TestExecCommandPayloadShape is OQ-7b / A12: one token becomes a bare string
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

// TestUTF8DecoderCarriesPartialSequences is the replacement for Python's
// per-chunk `chardet` (§3.6, §4.2): a rune split across two chunks must not
// become two replacement characters.
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

// TestArchiveExtraction is JSON_CLI_CONTRACT.md §7.3-§7.4.
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

// TestFromArchiveRequiresName is §7.1's three usage rules.
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

// TestFromArchiveNameOverridesLabName is §7.2's precedence rule.
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
	// The golden constant of §7.2.
	if !strings.Contains(out, `"hash":"9pe3y6IDMwx4PfOPu5mbNg"`) {
		t.Errorf("the hash was not recomputed from --name: %s", out)
	}
	if !strings.Contains(out, `"path":null`) {
		t.Errorf("an archive deploy must report a null path: %s", out)
	}
}

// TestConfigRoundTrip is PORT_SPEC §3.2 item 2 and JSON_CLI_CONTRACT.md §3.12.
func TestConfigRoundTrip(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["config"]

	if code := runCommand(t.Context(), a.app, spec, []string{"--format", "json", "get", "image"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, a.stderrString())
	}
	if !strings.Contains(a.stdoutString(), `{"key":"image","value":"kathara/base"}`) {
		t.Errorf("stdout = %s", a.stdoutString())
	}

	a2 := newTestApp(t)
	spec2 := commandTable(a2.app)["config"]
	if code := runCommand(t.Context(), a2.app, spec2, []string{"--format", "json", "get", "nope"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(a2.stdoutString(), `"code":"Settings"`) {
		t.Errorf("stdout = %s", a2.stdoutString())
	}
}

// TestSettingsWithoutATerminalRefuses is the honest degradation the curses menu
// never had.
func TestSettingsWithoutATerminalRefuses(t *testing.T) {
	a := newTestApp(t)
	a.console.Level = cliout.LevelDebug
	spec := commandTable(a.app)["settings"]

	if code := runCommand(t.Context(), a.app, spec, nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(a.stdoutString(), "kathara config get|set|list|reset") {
		t.Errorf("stdout = %q", a.stdoutString())
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

// TestLcleanReportsRunningLinks is §3.2: with no device filter, everything the
// scenario had deployed is reported; with one, only the collision domains the
// selected devices were attached to.
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

// TestLstartListSortsStatsByName is §3.1's `machine_stats` rule: the stream is
// keyed by container name, the envelope by device name.
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

// TestLstartDeployedNamesUseScheduleOrderAndSortedLinks is §3.1: devices in
// deploy schedule order (lab.conf insertion, as reordered by lab.dep), links
// canonically sorted.
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

// TestLconfigAddOrderIsSemantic is §3.10: `added` is in CLI argument order,
// because that order decides interface numbering.
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

// TestLconfigAddSyntaxErrorIsExitOne is the `cd_mac` trap of A9: a malformed
// `--add` value escapes argparse and becomes an error envelope, while a
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

// TestVconfigFetchesTheAPIObjects is the one behavioural difference §0.2 #3's
// collapse had to keep: `vconfig` fetches the device and link handles by hand
// (`VconfigCommand.py:63,85`), `lconfig` does not.
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

// TestLrestartJSONWritesOneObject is the one-object rule across a nested
// command (§1.2, §1.4). `lrestart` runs `lclean` and `lstart` through the
// dispatcher with rebuilt argv, and that argv carries no `--format`; re-reading
// the fresh sub-parser's `human` default would flip the console back for the
// whole clean phase, putting its panel on stdout before the envelope and
// leaving `clean.machines`/`clean.links` empty because the pre-undeploy
// snapshot is gated on the format.
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

// TestInterruptSwallowsTheCommandError is §6.2 read together with §1.4: when
// the failure IS the interrupt, the entrypoint's interrupt arm owns stdout.
// Rendering the cancelled context as an error first would put two objects on a
// json stdout and a CRITICAL line Python never prints in human mode.
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

// TestWipeAllSnapshotsAllUsers is E4: `links` already honoured `-a` and the
// wipe itself does, so `machines` has to as well — otherwise a root all-users
// wipe reports only the caller's own devices.
func TestWipeAllSnapshotsAllUsers(t *testing.T) {
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

// TestListStreamEndIsACleanExit is CLI_SURFACE.md §13: `create_lab_table`
// catches `StopIteration` and answers `None`, and `ListCommand.run` still
// returns 0. `MachinesStatsStream.Next` documents io.EOF as that end, so it
// cannot become an error envelope and exit 1.
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

// TestLcleanKeepsLinksHeldByASurvivingDevice is §3.2's "names actually
// undeployed": `DockerLink.undeploy` reloads each candidate network and deletes
// only the ones with no containers left (`DockerLink.py:170`), so a collision
// domain shared with a device the filter spared is NOT removed.
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
