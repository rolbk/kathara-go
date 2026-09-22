package cliout

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/labfile"
)

const (
	hashDefaultScenario = "FwFaxbiuhvSWb2KpN5zw"
	hashNamedScenario   = "9pe3y6IDMwx4PfOPu5mbNg"
)

func labObject() Lab {
	return Lab{Hash: hashDefaultScenario, Path: Str("/labs/default_scenario")}
}

func inventory() *kathara.MachineStats {
	user, status := "user-abcdefgh", "running"
	return &kathara.MachineStats{
		NetworkScenarioID: hashNamedScenario,
		Name:              "pc1",
		ContainerName:     "kathara_user_pc1_" + hashNamedScenario,
		User:              &user,
		Status:            &status,
		Image:             "kathara/base",
	}
}

// emit runs one envelope through a json-mode console and returns stdout.
func emit(t *testing.T, e Envelope) string {
	t.Helper()
	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatJSON, LevelWarning)
	c.Emit(e)
	return out.String()
}

func TestEnvelopesMatchContractExamples(t *testing.T) {
	tests := []struct {
		name     string
		envelope Envelope
		want     string
	}{
		{
			name: "lstart normal deploy",
			envelope: LstartResult{
				Lab:      labObject(),
				Machines: []string{"r1", "pc1", "pc2"},
				Links:    []string{"A", "B"},
			},
			want: `{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"dry_run":false,"machines":["r1","pc1","pc2"],"links":["A","B"]}`,
		},
		{
			name: "lstart dry mode",
			envelope: LstartResult{
				Lab:    labObject(),
				DryRun: true,
				Checks: []string{"lab.conf", "lab.dep"},
			},
			want: `{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"dry_run":true,"checks":[{"file":"lab.conf","ok":true},{"file":"lab.dep","ok":true}]}`,
		},
		{
			name: "lstart with --list appends machine_stats",
			envelope: LstartResult{
				Lab:          labObject(),
				Machines:     []string{"pc1"},
				Links:        []string{},
				MachineStats: []*kathara.MachineStats{inventory()},
			},
			want: `{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"dry_run":false,"machines":["pc1"],"links":[],"machine_stats":[` +
				`{"network_scenario_id":"9pe3y6IDMwx4PfOPu5mbNg","name":"pc1",` +
				`"container_name":"kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg","user":"user-abcdefgh",` +
				`"status":"running","image":"kathara/base"}]}`,
		},
		{
			name: "lab metadata keys are appended when any is set",
			envelope: LstartResult{
				Lab: Lab{
					Name: Str("Default scenario"), Hash: hashNamedScenario, Path: Str("/labs/x"),
					Description: "A lab", Version: "1.0",
				},
				Machines: []string{"pc1"},
				Links:    []string{},
			},
			want: `{"lab":{"name":"Default scenario","hash":"9pe3y6IDMwx4PfOPu5mbNg","path":"/labs/x",` +
				`"description":"A lab","version":"1.0","author":null,"email":null,"web":null},` +
				`"dry_run":false,"machines":["pc1"],"links":[]}`,
		},
		{
			name: "lclean",
			envelope: LcleanResult{
				Lab:      labObject(),
				Machines: []string{"pc1", "r1"},
				Links:    []string{"A", "B"},
			},
			want: `{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"machines":["pc1","r1"],"links":["A","B"]}`,
		},
		{
			name:     "empty arrays are [] and never null",
			envelope: LcleanResult{Lab: labObject()},
			want: `{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"machines":[],"links":[]}`,
		},
		{
			name: "lrestart combines the clean and start envelopes",
			envelope: LrestartResult{
				Clean: LcleanResult{Lab: labObject(), Machines: []string{"pc1"}, Links: []string{"A"}},
				Start: LstartResult{Lab: labObject(), Machines: []string{"pc1"}, Links: []string{"A"}},
			},
			want: `{"clean":{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"machines":["pc1"],"links":["A"]},` +
				`"start":{"lab":{"name":null,"hash":"FwFaxbiuhvSWb2KpN5zw","path":"/labs/default_scenario"},` +
				`"dry_run":false,"machines":["pc1"],"links":["A"]}}`,
		},
		{
			name:     "wipe",
			envelope: WipeResult{Machines: []string{"pc1"}, Links: []string{"A"}},
			want:     `{"settings_wiped":false,"all_users":false,"machines":["pc1"],"links":["A"]}`,
		},
		{
			name:     "wipe --settings",
			envelope: WipeResult{SettingsWiped: true},
			want:     `{"settings_wiped":true,"all_users":false,"machines":[],"links":[]}`,
		},
		{
			name:     "list",
			envelope: ListResult{Machines: []*kathara.MachineStats{inventory()}},
			want: `{"machines":[{"network_scenario_id":"9pe3y6IDMwx4PfOPu5mbNg","name":"pc1",` +
				`"container_name":"kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg","user":"user-abcdefgh",` +
				`"status":"running","image":"kathara/base"}]}`,
		},
		{
			name:     "exec",
			envelope: ExecResult{Stdout: "PING 8.8.8.8\n", ExitCode: 0},
			want:     `{"stdout":"PING 8.8.8.8\n","stderr":"","exit_code":0}`,
		},
		{
			name: "check ok",
			envelope: CheckResult{
				Manager: "Docker (Kathara)", ManagerVersion: "27.3.1", RuntimeVersion: "go1.24.1",
				KatharaVersion: "3.8.3", OSVersion: "Linux-6.12.88-x86_64",
				Image: "kathara/base", OK: true,
			},
			want: `{"manager":"Docker (Kathara)","manager_version":"27.3.1","runtime_version":"go1.24.1",` +
				`"kathara_version":"3.8.3","os_version":"Linux-6.12.88-x86_64",` +
				`"container_test":{"image":"kathara/base","ok":true,"error":null}}`,
		},
		{
			name: "check failed carries the exception string",
			envelope: CheckResult{
				Manager: "Docker (Kathara)", ManagerVersion: "27.3.1", RuntimeVersion: "go1.24.1",
				KatharaVersion: "3.8.3", OSVersion: "Linux-6.12.88-x86_64",
				Image: "kathara/base", OK: false, Error: "no such image",
			},
			want: `{"manager":"Docker (Kathara)","manager_version":"27.3.1","runtime_version":"go1.24.1",` +
				`"kathara_version":"3.8.3","os_version":"Linux-6.12.88-x86_64",` +
				`"container_test":{"image":"kathara/base","ok":false,"error":"no such image"}}`,
		},
		{
			name: "vstart",
			envelope: VstartResult{
				Lab: Lab{Name: Str("kathara_vlab"), Hash: "vlabhash"}, Machine: "pc1",
				Links: []string{"A", "B"},
			},
			want: `{"lab":{"name":"kathara_vlab","hash":"vlabhash","path":null},"machine":"pc1",` +
				`"dry_run":false,"links":["A","B"]}`,
		},
		{
			name: "vstart dry mode stops at dry_run",
			envelope: VstartResult{
				Lab: Lab{Name: Str("kathara_vlab"), Hash: "vlabhash"}, Machine: "pc1", DryRun: true,
			},
			want: `{"lab":{"name":"kathara_vlab","hash":"vlabhash","path":null},"machine":"pc1","dry_run":true}`,
		},
		{
			name: "vclean reports an empty machines for a name that was not running",
			envelope: VcleanResult{
				Lab: Lab{Name: Str("kathara_vlab"), Hash: "vlabhash"}, Machine: "pc1",
			},
			want: `{"lab":{"name":"kathara_vlab","hash":"vlabhash","path":null},"machine":"pc1","machines":[]}`,
		},
		{
			name: "lconfig",
			envelope: ConfigResult{
				Lab: Lab{Hash: "abc", Path: Str("/labs/x")}, Machine: "pc1",
				Added: []AddedLink{{Link: "A"}, {Link: "B", MAC: "00:11:22:33:44:55"}},
			},
			want: `{"lab":{"name":null,"hash":"abc","path":"/labs/x"},"machine":"pc1",` +
				`"added":[{"link":"A","mac":null},{"link":"B","mac":"00:11:22:33:44:55"}],"removed":[]}`,
		},
		{
			name: "vconfig has the same shape over the vlab",
			envelope: ConfigResult{
				Lab: Lab{Name: Str("kathara_vlab"), Hash: "vlabhash"}, Machine: "pc1",
				Added: []AddedLink{{Link: "A"}},
			},
			want: `{"lab":{"name":"kathara_vlab","hash":"vlabhash","path":null},"machine":"pc1",` +
				`"added":[{"link":"A","mac":null}],"removed":[]}`,
		},
		{
			name:     "config get",
			envelope: SettingsGetResult{Key: "image", Value: "kathara/base"},
			want:     `{"key":"image","value":"kathara/base"}`,
		},
		{
			name:     "config set",
			envelope: SettingsSetResult{Key: "image", Value: "kathara/frr", Saved: true},
			want:     `{"key":"image","value":"kathara/frr","saved":true}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := emit(t, tc.envelope)
			if got != tc.want+"\n" {
				t.Errorf("\n got %s\nwant %s\n", got, tc.want)
			}
		})
	}
}

func TestListSortsCanonically(t *testing.T) {
	mk := func(hash, name string) *kathara.MachineStats {
		return &kathara.MachineStats{NetworkScenarioID: hash, Name: name, Image: "i"}
	}
	out := emit(t, ListResult{Machines: []*kathara.MachineStats{
		mk("b", "pc2"), mk("a", "pc9"), mk("b", "pc1"), mk("a", "pc1"),
	}})
	want := `"name":"pc1"`
	if !strings.Contains(out, want) {
		t.Fatalf("unexpected output %s", out)
	}
	order := []string{`"network_scenario_id":"a","name":"pc1"`, `"network_scenario_id":"a","name":"pc9"`,
		`"network_scenario_id":"b","name":"pc1"`, `"network_scenario_id":"b","name":"pc2"`}
	prev := -1
	for _, needle := range order {
		idx := strings.Index(out, needle)
		if idx < 0 {
			t.Fatalf("%q missing from %s", needle, out)
		}
		if idx < prev {
			t.Fatalf("rows are out of canonical order in %s", out)
		}
		prev = idx
	}
}

func TestEncodingRules(t *testing.T) {
	out := emit(t, SettingsGetResult{Key: "image", Value: `a<b>c&d "e"`})
	// `<`, `>` and `&` appear literally; only the quote is escaped, which is
	// JSON's own rule and not `encoding/json`'s HTML escaping.
	want := `{"key":"image","value":"a<b>c&d \"e\""}` + "\n"
	if out != want {
		t.Errorf("\n got %q\nwant %q", out, want)
	}
	if strings.Contains(out, `\u003c`) {
		t.Errorf("HTML escaping is on; got %s", out)
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Errorf("want exactly one trailing newline; got %q", out)
	}
	if strings.Contains(out, "\n  ") {
		t.Errorf("output is not compact: %q", out)
	}
}

func TestErrorEnvelope(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "MachineNotFound carries `machine`",
			err:  kerrors.NewMachineNotFoundQuoted("pc1"),
			want: `{"error":{"code":"MachineNotFound","message":"Device ` + "`pc1`" + ` not found.","machine":"pc1"}}`,
		},
		{
			name: "the plural variant carries a sorted `machines`",
			err:  kerrors.NewMachineNotFoundSet([]string{"pc3", "pc1"}),
			want: `{"error":{"code":"MachineNotFound","message":"The following devices are not in the network scenario: ` +
				`{'pc1', 'pc3'}.","machines":["pc1","pc3"]}}`,
		},
		{
			name: "MachineBinary carries binary then machine",
			err:  kerrors.NewMachineBinary("ip", "pc1"),
			want: `{"error":{"code":"MachineBinary","message":"Binary ` + "`ip`" +
				` not found in device ` + "`pc1`" + `.","binary":"ip","machine":"pc1"}}`,
		},
		{
			name: "NonSequentialMachineInterface carries iface then machine",
			err:  kerrors.NewNonSequentialMachineInterface(1, "pc1"),
			want: `{"error":{"code":"NonSequentialMachineInterface","message":"Interface ` + "`1`" +
				` missing on device ` + "`pc1`" + `.","iface":1,"machine":"pc1"}}`,
		},
		{
			name: "FeatureNotAvailable carries the pinned token",
			err:  kerrors.NewFeatureNotAvailable(kerrors.FeatureLinfo),
			want: `{"error":{"code":"FeatureNotAvailable","message":"The linfo command is not supported in this release. ` +
				`Use Kathará 3.8.x.","feature":"linfo"}}`,
		},
		{
			name: "ConfirmationRequired carries no field",
			err:  kerrors.ErrWipeConfirmationRequired,
			want: `{"error":{"code":"ConfirmationRequired","message":"Confirmation required: re-run with ` +
				"`--force`" + ` to wipe Kathara."}}`,
		},
		{
			name: "a parse failure carries file and line",
			err:  &labfile.ParseError{File: "lab.conf", Line: 3, Msg: "boom", Code: kerrors.CodeSyntax},
			want: `{"error":{"code":"Syntax","message":"boom","file":"lab.conf","line":3}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			c := New(&out, &bytes.Buffer{}, FormatJSON, LevelWarning)
			if code := c.EmitError(tc.err); code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			if got := out.String(); got != tc.want+"\n" {
				t.Errorf("\n got %s\nwant %s\n", got, tc.want)
			}
		})
	}
}

func TestErrorBatchAddsTheErrorsSibling(t *testing.T) {
	joined := errors.Join(kerrors.NewMachineNotRunning("pc1"), kerrors.NewMachineNotRunning("pc2"))
	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatJSON, LevelWarning)
	c.EmitError(joined)

	got := out.String()
	if !strings.HasPrefix(got, `{"error":{"code":"MachineNotRunning"`) {
		t.Errorf("the primary error must come first: %s", got)
	}
	if !strings.Contains(got, `,"errors":[{"code":"MachineNotRunning","message":"Device `+"`pc1`"+` is not running.","machine":"pc1"},`) {
		t.Errorf("the errors sibling is missing or misordered: %s", got)
	}
}

func TestErrorBatchRendersOnlyThePrimaryInTheErrorObject(t *testing.T) {

	joined := errors.Join(
		kerrors.NewMachineNotRunning("pc1"),
		kerrors.NewMachineBinary("frr", "pc2"),
		kerrors.NewMachineNotRunning("pc3"),
	)

	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatJSON, LevelWarning)
	if code := c.EmitError(joined); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}

	want := `{"error":{"code":"MachineNotRunning","message":"Device ` + "`pc1`" + ` is not running.","machine":"pc1"},` +
		`"errors":[` +
		`{"code":"MachineNotRunning","message":"Device ` + "`pc1`" + ` is not running.","machine":"pc1"},` +
		`{"code":"MachineBinary","message":"Binary ` + "`frr`" + ` not found in device ` + "`pc2`" + `.","binary":"frr","machine":"pc2"},` +
		`{"code":"MachineNotRunning","message":"Device ` + "`pc3`" + ` is not running.","machine":"pc3"}` +
		`]}` + "\n"
	if got := out.String(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestErrorBatchHumanLineIsThePrimaryOnly(t *testing.T) {
	joined := errors.Join(
		kerrors.NewMachineNotRunning("pc1"),
		kerrors.NewMachineBinary("frr", "pc2"),
		kerrors.NewMachineNotRunning("pc3"),
	)

	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatHuman, LevelWarning)
	if code := c.EmitError(joined); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}

	want := "CRITICAL (MachineNotRunningError) Device `pc1` is not running.\n"
	if got := out.String(); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestJSONLEvents(t *testing.T) {
	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatJSONL, LevelWarning)

	c.EmitStreamChunk("stdout", "PING 8.8.8.8\n")
	c.EmitStreamChunk("stderr", "oops\n")
	c.EmitStreamChunk("stdout", "")
	c.EmitStreamExit(3)

	want := `{"type":"stdout","data":"PING 8.8.8.8\n"}` + "\n" +
		`{"type":"stderr","data":"oops\n"}` + "\n" +
		`{"type":"exit","code":3}` + "\n"
	if got := out.String(); got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

// TestJSONLErrorEvent checks the streaming error event.
func TestJSONLErrorEvent(t *testing.T) {
	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatJSONL, LevelWarning)
	c.EmitError(kerrors.NewMachineNotRunning("pc1"))

	want := `{"type":"error","error":{"code":"MachineNotRunning","message":"Device ` + "`pc1`" +
		` is not running.","machine":"pc1"}}` + "\n"
	if got := out.String(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
}

func TestJSONLErrorEventOfABatchIsThePrimaryAlone(t *testing.T) {
	joined := errors.Join(
		kerrors.NewMachineNotRunning("pc1"),
		kerrors.NewMachineBinary("frr", "pc2"),
	)

	var out bytes.Buffer
	c := New(&out, &bytes.Buffer{}, FormatJSONL, LevelWarning)
	if code := c.EmitError(joined); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}

	want := `{"type":"error","error":{"code":"MachineNotRunning","message":"Device ` + "`pc1`" +
		` is not running.","machine":"pc1"}}` + "\n"
	if got := out.String(); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}
	if strings.Contains(out.String(), `"errors"`) {
		t.Errorf("the jsonl event must not carry the `errors` sibling: %s", out.String())
	}
}

func TestInterruptEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		format Format
		want   string
	}{
		{FormatJSON, `{"interrupted":true}` + "\n"},
		{FormatJSONL, `{"type":"interrupted"}` + "\n"},
		{FormatHuman, ""},
	} {
		var out bytes.Buffer
		c := New(&out, &bytes.Buffer{}, tc.format, LevelWarning)
		c.EmitInterrupted()
		if got := out.String(); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.format, got, tc.want)
		}
	}
}

func TestHumanModeEmitsNothingToStdoutInJSONMode(t *testing.T) {
	var out, errw bytes.Buffer
	c := New(&out, &errw, FormatJSON, LevelWarning)
	c.Print("a panel line")
	c.PrintPanel("Starting Network Scenario", PanelOptions{Justify: JustifyCenter})
	c.Warning("a warning")

	if out.String() != "" {
		t.Errorf("stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errw.String(), "warning: a warning") {
		t.Errorf("the log record must go to stderr; got %q", errw.String())
	}
}

func TestAssignedNodeIsAbsentOnDockerAndPresentOnKubernetes(t *testing.T) {
	docker := inventory()
	if strings.Contains(emit(t, ListResult{Machines: []*kathara.MachineStats{docker}}), "assigned_node") {
		t.Error("Docker inventory must not carry assigned_node")
	}

	scheduled := inventory()
	scheduled.AssignedNode = kathara.SomeString("node-1")
	if !strings.Contains(emit(t, ListResult{Machines: []*kathara.MachineStats{scheduled}}),
		`"image":"kathara/base","assigned_node":"node-1"`) {
		t.Error("a scheduled pod must carry assigned_node after image")
	}

	pending := inventory()
	pending.AssignedNode = kathara.NullString()
	if !strings.Contains(emit(t, ListResult{Machines: []*kathara.MachineStats{pending}}),
		`"assigned_node":null`) {
		t.Error("a pending pod must carry a null assigned_node")
	}
}
