package main

import (
	"context"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/model"
)

func TestMachineDeployedSubscriberOrder(t *testing.T) {
	a := newTestApp(t)
	a.console.TTY = true
	a.settings.OpenTerminals = true

	var order []string
	a.terminalOpener = func(context.Context, *model.Machine) error {
		order = append(order, "terminal")
		return nil
	}
	a.registerEvents()

	// Wrap the bar's own subscriber by watching its counter through the
	// rendered line: the bar is what writes first.
	if err := event.Dispatch(a.dispatcher, event.MachinesDeployStarted{}); err != nil {
		t.Fatal(err)
	}
	before := a.stdoutString()

	lab := model.NewLab("l", a.defaults())
	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Machine: machine}); err != nil {
		t.Fatal(err)
	}

	if len(order) != 1 || order[0] != "terminal" {
		t.Fatalf("the terminal handler did not run: %v", order)
	}
	if a.stdoutString() == before {
		t.Error("the progress bar did not advance")
	}
	if !strings.Contains(a.stdoutString(), "[Deploying devices]") {
		t.Errorf("stdout does not carry the bar: %q", a.stdoutString())
	}
}

// TestNoTerminalsPathIsComplete is the whole of `--noterminals` and of
// `open_terminals: false`: the second `machine_deployed` subscriber does
// nothing, so no terminal backend is reached at all.
func TestNoTerminalsPathIsComplete(t *testing.T) {
	a := newTestApp(t)
	a.settings.OpenTerminals = false
	opened := 0
	a.terminalOpener = func(context.Context, *model.Machine) error { opened++; return nil }
	a.registerEvents()

	lab := model.NewLab("l", a.defaults())
	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Machine: machine}); err != nil {
		t.Fatal(err)
	}
	if opened != 0 {
		t.Errorf("opened %d terminals with open_terminals=false", opened)
	}
}

func TestTerminalsAreNeverOpenedInMachineFormats(t *testing.T) {
	a := newTestApp(t)
	a.console.Format = cliout.FormatJSON
	a.settings.OpenTerminals = true
	opened := 0
	a.terminalOpener = func(context.Context, *model.Machine) error { opened++; return nil }
	a.registerEvents()

	lab := model.NewLab("l", a.defaults())
	machine, err := lab.GetOrNewMachine("pc1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Machine: machine}); err != nil {
		t.Fatal(err)
	}
	if opened != 0 {
		t.Errorf("opened %d terminals under --format json", opened)
	}
	if a.stdoutString() != "" {
		t.Errorf("the progress bar wrote to stdout under --format json: %q", a.stdoutString())
	}
}

// TestKubernetesMachineDeployedCarriesNoObject is the per-backend payload
// variance of `event.MachineDeployed`: Megalos dispatches the device *name*
// with no object, for which Python raises while accessing
// `item.get_num_terms()`.
func TestKubernetesMachineDeployedCarriesNoObject(t *testing.T) {
	a := newTestApp(t)
	a.settings.OpenTerminals = true
	opened := 0
	a.terminalOpener = func(context.Context, *model.Machine) error { opened++; return nil }
	a.registerEvents()

	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{Name: "pc1"}); err != nil {
		t.Fatalf("a name-only payload must not fail: %v", err)
	}
	if opened != 0 {
		t.Error("there is no device object to open a terminal onto")
	}
}

// TestUnregisterIsIdempotent is `register.py:37,44`, which unsubscribes
// `machine_deployed` twice and relies on the second call being a no-op.
func TestUnregisterIsIdempotent(t *testing.T) {
	a := newTestApp(t)
	a.registerEvents()
	a.unregisterEvents()
	a.unregisterEvents()

	// A dispatch after teardown must reach nobody and must not fail.
	if err := event.Dispatch(a.dispatcher, event.MachinesDeployStarted{}); err != nil {
		t.Fatalf("dispatch after teardown: %v", err)
	}
	if err := event.Dispatch(a.dispatcher, event.MachineDeployed{}); err != nil {
		t.Fatalf("dispatch after teardown: %v", err)
	}
}

// TestUnregisterStopsTheProgressBars is what the teardown hooks are for: the
// entrypoint runs `unregister_cli_events()` on every exit path, Ctrl-C
// included, and that is what closes the bar.
func TestUnregisterStopsTheProgressBars(t *testing.T) {
	a := newTestApp(t)
	a.console.TTY = true
	a.registerEvents()

	if err := event.Dispatch(a.dispatcher, event.MachinesDeployStarted{
		Machines: []*model.Machine{nil, nil, nil},
	}); err != nil {
		t.Fatal(err)
	}
	a.unregisterEvents()

	if !strings.Contains(a.stdoutString(), "0/3") {
		t.Errorf("the bar never opened with a total: %q", a.stdoutString())
	}
	if !strings.HasSuffix(a.stdoutString(), "\n") {
		t.Errorf("the bar was not closed with a newline: %q", a.stdoutString())
	}
}

func TestVolumePromptAutoAnswersYesInMachineFormats(t *testing.T) {
	a := newTestApp(t)
	a.console.Format = cliout.FormatJSON
	// The notice is an INFO record, and INFO is `debug_level`'s default
	// (`setting/Setting.py:30`).
	a.console.Level = cliout.LevelInfo
	a.settings.VolumeMountPolicy = cliout.PolicyPrompt
	a.registerEvents()

	lab := model.NewLab("l", a.defaults())
	if err := event.Dispatch(a.dispatcher, event.MachinesWithVolumes{Lab: lab}); err != nil {
		t.Fatal(err)
	}
	if _, ok := lab.GeneralOption("_mount_volumes"); ok {
		t.Error("json mode must not decline the mount")
	}
	if !strings.Contains(a.stderrString(), "mounting the declared volumes") {
		t.Errorf("the notice is missing from stderr: %q", a.stderrString())
	}
	if a.stdoutString() != "" {
		t.Errorf("stdout = %q, want empty", a.stdoutString())
	}
}

// TestVolumePromptDeclinedSetsMountVolumesFalse is the human arm
// (`MountDevicesVolumes.py:36-39`).
func TestVolumePromptDeclinedSetsMountVolumesFalse(t *testing.T) {
	a := newTestApp(t)
	a.settings.VolumeMountPolicy = cliout.PolicyPrompt
	a.prompter = &cliout.Prompter{Console: a.console, In: strings.NewReader("n\n")}
	a.registerEvents()

	lab := model.NewLab("l", a.defaults())
	if err := event.Dispatch(a.dispatcher, event.MachinesWithVolumes{Lab: lab}); err != nil {
		t.Fatal(err)
	}
	value, ok := lab.GeneralOption("_mount_volumes")
	if !ok {
		t.Fatal("_mount_volumes was not set")
	}
	if b, _ := value.AsBool(); b {
		t.Error("_mount_volumes must be false after declining")
	}
	if !strings.Contains(a.stdoutString(), "Continue with volume mounting? [y/n]: ") {
		t.Errorf("stdout = %q", a.stdoutString())
	}
}

func TestImageUpdatePromptAutoAnswersNoInMachineFormats(t *testing.T) {
	a := newTestApp(t)
	a.console.Format = cliout.FormatJSON
	a.console.Level = cliout.LevelInfo
	a.settings.ImageUpdatePolicy = cliout.PolicyPrompt
	a.registerEvents()

	puller := &fakePuller{}
	if err := event.Dispatch(a.dispatcher, event.DockerImageUpdateFound{
		Image: puller, ImageName: "kathara/base",
	}); err != nil {
		t.Fatal(err)
	}
	if puller.pulled != 0 {
		t.Errorf("pulled %d images; json mode must auto-answer no", puller.pulled)
	}
	if !strings.Contains(a.stderrString(), "not pulling it") {
		t.Errorf("the notice is missing: %q", a.stderrString())
	}
}

// TestImageUpdatePolicyAlwaysPulls is `UpdateDockerImage.py:26-27`, unchanged
// in every mode.
func TestImageUpdatePolicyAlwaysPulls(t *testing.T) {
	a := newTestApp(t)
	a.console.Format = cliout.FormatJSON
	a.settings.ImageUpdatePolicy = cliout.PolicyAlways
	a.registerEvents()

	puller := &fakePuller{}
	if err := event.Dispatch(a.dispatcher, event.DockerImageUpdateFound{
		Image: puller, ImageName: "kathara/base",
	}); err != nil {
		t.Fatal(err)
	}
	if puller.pulled != 1 {
		t.Errorf("pulled %d images, want 1", puller.pulled)
	}
}

type fakePuller struct{ pulled int }

func (p *fakePuller) Pull(string) error { p.pulled++; return nil }
