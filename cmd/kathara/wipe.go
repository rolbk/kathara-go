package main

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

type wipeFlags struct {
	force    bool
	settings bool
	all      bool
}

func newWipeCmd(a *app) *commandSpec {
	cmd := newParser("wipe")
	f := &wipeFlags{}
	flags := cmd.Flags()
	flags.BoolVarP(&f.force, "force", "f", false, "Force the wipe.")
	flags.BoolVarP(&f.settings, "settings", "s", false, "Wipe the stored settings of the current user.")
	flags.BoolVarP(&f.all, "all", "a", false,
		"Wipe all Kathara devices and collision domains of all users. MUST BE ROOT FOR THIS OPTION.")
	cmd.exclusiveGroup("settings", "all")
	registerFormat(cmd, false)

	return &commandSpec{
		Name: "wipe",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			return runWipe(ctx, a, f)
		},
	}
}

// runWipe is `WipeCommand.run`.
func runWipe(ctx context.Context, a *app, f *wipeFlags) (int, error) {
	if !f.force {
		if a.console.Format.Machine() {
			return 1, kerrors.ErrWipeConfirmationRequired
		}
		ok, err := a.prompter.Confirm("Are you sure to wipe Kathara?")
		if err != nil {
			return 1, err
		}
		if !ok {
			// `sys.exit` with no argument: exit **0**, nothing wiped.
			return 0, nil
		}
	}

	out := cliout.WipeResult{AllUsers: f.all}

	if f.settings {
		if err := settings.Wipe(); err != nil {
			return 1, err
		}
		out.SettingsWiped = true
		a.console.Emit(out)
		return 0, nil
	}

	if f.all {
		admin, err := util.IsAdmin()
		if err != nil {
			return 1, err
		}
		if !admin {
			return 1, kerrors.ErrPrivilegeWipeAllUsers
		}
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return 1, err
	}

	if a.console.Format.Machine() {
		// `-a` is what `Manager.wipe` is about to act on, so it is what the
		// inventory has to be read with: a current-user-only listing would
		// under-report `machines` for exactly the invocation that removes
		// everyone's devices.
		running, err := snapshotMachines(ctx, mgr, kathara.LabRef{}, f.all)
		if err != nil {
			return 1, err
		}
		for _, entry := range running {
			if entry.Stats != nil {
				out.Machines = append(out.Machines, entry.Stats.Name)
			}
		}
		slices.Sort(out.Machines)
		if out.Links, err = snapshotLinkNames(ctx, mgr, kathara.LabRef{}, f.all); err != nil {
			return 1, err
		}
	}

	if err := mgr.Wipe(ctx, f.all); err != nil {
		return 1, err
	}
	a.console.Emit(out)
	return 0, nil
}

// newCheckCmd is `CheckCommand.__init__`: `-h` and nothing else.
func newCheckCmd(a *app) *commandSpec {
	cmd := newParser("check")
	registerFormat(cmd, false)
	return &commandSpec{
		Name: "check",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			return runCheck(ctx, a)
		},
	}
}

// runCheck is `CheckCommand.run`.
func runCheck(ctx context.Context, a *app) (int, error) {
	a.console.PrintPanel("System Check", cliout.PanelOptions{Justify: cliout.JustifyCenter})

	mgr, err := a.manager(ctx)
	if err != nil {
		return 1, err
	}

	report := cliout.CheckResult{
		Manager:        mgr.GetFormattedManagerName(),
		RuntimeVersion: runtime.Version(),
		KatharaVersion: version,
		OSVersion:      osVersion(),
		Image:          a.settings.Image,
		OK:             true,
	}

	// One `console.print` per statement, in Python's order: the manager line is
	// on screen before `get_release_version()` is called, so a backend that
	// fails to answer leaves the first line printed.
	a.console.Print(expandTabs(fmt.Sprintf("Current Manager is:\t\t%s", report.Manager)))
	if report.ManagerVersion, err = mgr.GetReleaseVersion(ctx); err != nil {
		return 1, err
	}
	a.console.Print(expandTabs(fmt.Sprintf("Manager version is:\t\t%s", report.ManagerVersion)))
	// Python prints "Python version is:" here.
	a.console.Print(expandTabs(fmt.Sprintf("Go version is:\t\t\t%s", report.RuntimeVersion)))
	a.console.Print(expandTabs(fmt.Sprintf("Kathara version is:\t\t%s", report.KatharaVersion)))
	a.console.Print(expandTabs(fmt.Sprintf("Operating System version is:\t%s", report.OSVersion)))

	// `Setting.open_terminals = False` before the test lab, so that the check
	// never spawns a window.
	a.settings.OpenTerminals = false

	lab := model.NewLab("kathara_test", a.defaults())
	lab.AddOption("hosthome_mount", model.Bool(false))
	machine, err := lab.GetOrNewMachine("hello_world", nil)
	if err != nil {
		return 1, err
	}

	if err := deployAndUndeploy(ctx, mgr, machine); err != nil {
		report.OK = false
		report.Error = err.Error()
		// The glyph is U+00D7 MULTIPLICATION SIGN, not the letter x
		// (`CheckCommand.py:76`).
		a.console.Print(fmt.Sprintf("× Running container failed: %s", err.Error()))
		a.console.Emit(report)
		return 1, nil
	}

	a.console.Print("✓ Container run successfully.")
	a.console.Emit(report)
	return 0, nil
}

// checkTabSize is rich's `tab_size`, which `Console.print` applies to every
// string it renders.
const checkTabSize = 8

// expandTabs is rich's tab handling, the reason the five `check` labels line
// up: `Text.expand_tabs` replaces each tab with spaces up to the next
// `tab_size` stop *before* the segment ever reaches the terminal
// (`rich/text.py`, called from `Console.render_str`). Python therefore never
// emits a tab, and neither does this — the labels are 14, 19 and 28 columns
// wide, and every value starts at column 32.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + checkTabSize)
	column := 0
	for _, r := range s {
		switch r {
		case '\t':
			width := checkTabSize - column%checkTabSize
			b.WriteString(strings.Repeat(" ", width))
			column += width
		case '\n':
			// A tab after a newline measures from the new line's start.
			b.WriteRune(r)
			column = 0
		default:
			b.WriteRune(r)
			column++
		}
	}
	return b.String()
}

// deployAndUndeploy is the body of the check's `try`, which catches every
// exception and turns it into the failure line.
func deployAndUndeploy(ctx context.Context, mgr kathara.Manager, machine *model.Machine) error {
	if err := mgr.DeployMachine(ctx, machine); err != nil {
		return err
	}
	return mgr.UndeployMachine(ctx, machine, false)
}
