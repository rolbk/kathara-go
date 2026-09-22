// The order of operations in [runLstart] is Python's, statement for statement,
// because it is observable: the "Starting Network Scenario" panel is printed
// BEFORE `lab.conf` is parsed, which is why a scenario with a broken lab.conf
// records the panel and then the CRITICAL line
// (`test/goldens/err-malformed-labconf`).

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/labfile"
	"github.com/KatharaFramework/kathara-go/model"
)

type lstartFlags struct {
	terminalPair [2]*tristate
	hosthomePair [2]*tristate
	sharedPair   [2]*tristate

	privileged *tristate
	metadata   *stringList
	excluded   *stringList

	directory   string
	forceLab    bool
	list        bool
	terminalEmu string
	dryMode     bool
	fromArchive string
	archiveName string
	xterm       string
}

func registerLstartFlags(cmd *parser, restart bool) *lstartFlags {
	f := &lstartFlags{
		privileged: &tristate{constant: true},
		metadata:   &stringList{},
		excluded:   &stringList{},
	}
	flags := cmd.Flags()

	noTerminals := &tristate{constant: false}
	yesTerminals := &tristate{constant: true}
	bindConst(flags, noTerminals, "noterminals", "",
		"Start the network scenario without opening terminal windows.")
	bindConst(flags, yesTerminals, "terminals", "",
		"Start the network scenario opening terminal windows.")
	cmd.exclusiveGroup("noterminals", "terminals")

	bindConst(flags, f.privileged, "privileged", "",
		"Start the devices in privileged mode. MUST BE ROOT FOR THIS OPTION.")
	flags.StringVarP(&f.directory, "directory", "d", "",
		"Specify the folder containing the network scenario.")
	cmd.meta("directory", "DIRECTORY")
	flags.BoolVarP(&f.forceLab, "force-lab", "F", false,
		"Force the network scenario to start without a lab.conf or lab.dep file.")
	flags.BoolVarP(&f.list, "list", "l", false,
		"Show information about running devices after the network scenario has been started.")
	bindList(cmd, f.metadata, "pass", "o", "METADATA",
		"Apply metadata to all devices of a network scenario during startup.", greedyZeroOrMore)

	if restart {
		flags.StringVar(&f.xterm, "xterm", "",
			"Set a different terminal emulator application (Unix only).")
		cmd.meta("xterm", "XTERM")
	} else {
		flags.StringVar(&f.terminalEmu, "terminal-emu", "",
			"Set a different terminal emulator application (Unix only).")
		cmd.meta("terminal-emu", "TERMINAL_EMU")
		flags.BoolVar(&f.dryMode, "print", false,
			"Open the lab.conf file and check if it is correct (dry run).")
		flags.BoolVar(&f.dryMode, "dry-mode", false,
			"Open the lab.conf file and check if it is correct (dry run).")
		cmd.alias("dry-mode", "print")
		cmd.names("print", "--print", "--dry-mode")
	}

	noHosthome := &tristate{constant: false}
	yesHosthome := &tristate{constant: true}
	bindConst(flags, noHosthome, "no-hosthome", "H",
		`Do not mount "/hosthome" directory inside devices.`)
	cmd.names("no-hosthome", "--no-hosthome", "-H")
	bindConst(flags, yesHosthome, "hosthome", "",
		`Mount "/hosthome" directory inside devices.`)
	cmd.exclusiveGroup("no-hosthome", "hosthome")

	noShared := &tristate{constant: false}
	yesShared := &tristate{constant: true}
	bindConst(flags, noShared, "no-shared", "S",
		`Do not mount "/shared" directory inside devices.`)
	cmd.names("no-shared", "--no-shared", "-S")
	bindConst(flags, yesShared, "shared", "",
		`Mount "/shared" directory inside devices.`)
	cmd.exclusiveGroup("no-shared", "shared")

	excludeHelp := "Exclude specified devices from startup."
	if restart {
		excludeHelp = "Exclude specified devices."
	}
	bindList(cmd, f.excluded, "exclude", "", "DEVICE_NAME", excludeHelp, greedyOneOrMore)

	if !restart {
		flags.StringVar(&f.fromArchive, "from-archive", "",
			"Deploy a network scenario read as a tar archive from stdin (only `-` is accepted).")
		cmd.meta("from-archive", "ARCHIVE")
		flags.StringVar(&f.archiveName, "name", "",
			"Name of the network scenario deployed with --from-archive.")
		cmd.meta("name", "LAB_NAME")
	}
	posHelp := "Launches only specified devices."
	if restart {
		posHelp = "Restarts only specified devices."
	}
	cmd.pos("DEVICE_NAME", nargsZeroOrMore, posHelp)

	f.terminalPair = [2]*tristate{noTerminals, yesTerminals}
	f.hosthomePair = [2]*tristate{noHosthome, yesHosthome}
	f.sharedPair = [2]*tristate{noShared, yesShared}
	return f
}

// newLstartCmd is `LstartCommand.__init__`.
func newLstartCmd(a *app) *commandSpec {
	cmd := newParser("lstart")
	f := registerLstartFlags(cmd, false)
	registerFormat(cmd, false)

	return &commandSpec{
		Name: "lstart",
		Cmd:  cmd,
		Greedy: []greedySpec{
			{long: "pass", short: "o", kind: greedyZeroOrMore},
			{long: "exclude", kind: greedyOneOrMore},
		},
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			result, err := runLstart(ctx, a, f, positional)
			a.lastLstartResult = result
			if err != nil {
				return 1, err
			}
			if !a.suppressEmit {
				a.console.Emit(result)
			}
			return 0, nil
		},
	}
}

// lstartOutcome is what [runLstart] reports to both renderers.
type lstartOutcome = cliout.LstartResult

// runLstart is `LstartCommand.run`.
func runLstart(ctx context.Context, a *app, f *lstartFlags, selected []string) (lstartOutcome, error) {
	var out lstartOutcome

	labPath, cleanup, err := f.resolveScenario(a)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return out, err
	}

	// The `--from-archive` extraction directory's lifetime.
	// It is therefore removed only on the paths that end with `deployed`
	// false: a usage or extraction failure (handled inside `resolveScenario`
	// and just above), a parse or validation failure before `DeployLab`, and
	// `--dry-mode`, which never deploys at all.
	deployed := false
	if cleanup != nil {
		defer func() {
			if !deployed {
				cleanup()
			}
		}()
	}

	if err := a.loadCustomConfiguration(labPath); err != nil {
		return out, err
	}

	// `Setting.open_terminals` is overridden only when the tri-state was set;
	// `Setting.terminal` only when `--terminal-emu` is truthy.
	if v := f.terminalsValue(); v != nil {
		a.settings.OpenTerminals = *v
	}
	if f.terminalEmu != "" {
		a.settings.Terminal = f.terminalEmu
	}

	title := "Starting Network Scenario"
	if f.dryMode {
		title = "Checking Network Scenario"
	}
	a.console.PrintPanel(title, cliout.PanelOptions{Justify: cliout.JustifyCenter})

	lab, err := labfile.ParseLab(labPath, labfile.DefaultConfName, a.defaults())
	if err != nil {
		// `except IOError` — every file-level failure of `LabParser`, and
		// nothing else. A malformed line still propagates.
		if !f.forceLab || !isIOError(err) {
			return out, err
		}
		if lab, err = labfile.ParseFolder(labPath, a.defaults()); err != nil {
			return out, err
		}
	}
	if f.archiveName != "" {
		lab.SetName(f.archiveName)
	}

	dependencies, err := labfile.ParseDep(labPath)
	if err != nil {
		return out, err
	}
	if len(dependencies) > 0 {
		lab.ApplyDependencies(dependencies)
	}

	if meta := lab.String(); meta != "" {
		a.console.PrintPanel(meta, cliout.PanelOptions{})
	}

	if len(lab.MachineNames()) == 0 {
		return out, kerrors.ErrNoDevicesInScenario
	}

	options, err := labfile.ParseOptions(f.metadata.Values())
	if err != nil {
		return out, err
	}
	for _, entry := range options.Entries() {
		lab.AddGlobalMachineMetadata(entry.Key, model.Str(entry.Value))
	}

	labExtExists := false
	if _, statErr := os.Stat(filepath.Join(labPath, labfile.ExtName)); statErr == nil {
		labExtExists = true
		if err := labfile.CheckExt(labPath); err != nil {
			return out, err
		}
	}

	out.Lab = asLabObject(lab, f.fromArchive == "")
	out.DryRun = f.dryMode

	if f.dryMode {
		a.console.Print("✓ lab.conf file is correct.")
		out.Checks = append(out.Checks, labfile.DefaultConfName)
		if len(dependencies) > 0 {
			a.console.Print("✓ lab.dep file is correct.")
			out.Checks = append(out.Checks, labfile.DepName)
		}
		if labExtExists {
			// Unreachable in 1.0 — CheckExt above already returned — but kept
			// so the shape of the Python branch survives the deferral.
			a.console.Print("✓ lab.ext file is correct.")
		}
		return out, nil
	}

	lab.AddOption("hosthome_mount", tristateScalar(f.hosthomeValue()))
	lab.AddOption("shared_mount", tristateScalar(f.sharedValue()))

	privileged := f.privileged.Get()
	anyPrivileged := false
	for _, m := range lab.Machines() {
		if m.IsPrivileged() {
			anyPrivileged = true
			break
		}
	}
	if (privileged != nil && *privileged) || anyPrivileged {
		admin, err := util.IsAdmin()
		if err != nil {
			return out, err
		}
		if !admin {
			return out, kerrors.ErrPrivilegeLabPrivileged
		}
		if a.settings.OpenTerminals {
			a.console.Print("⚠ Running devices with privileged capabilities, terminals might not open!")
		}
	}
	lab.AddGlobalMachineMetadata("privileged", tristateScalar(privileged))

	mgr, err := a.manager(ctx)
	if err != nil {
		return out, err
	}

	opts := kathara.DeployLabOptions{
		SelectedMachines: kathara.NewNameSet(selected...),
		ExcludedMachines: kathara.NewNameSet(f.excluded.Values()...),
	}
	if err := mgr.DeployLab(ctx, lab, opts); err != nil {
		return out, err
	}
	// From here the scenario is running out of `labPath`, so an archive's
	// extraction directory must survive this function: see the lifetime note
	// at the top.
	deployed = true

	out.Machines, out.Links = deployedNames(lab, opts)

	if f.list {
		stream, err := mgr.GetMachinesStats(ctx, kathara.LabRef{Hash: lab.Hash}, "", false)
		if err != nil {
			return out, err
		}
		defer func() { _ = stream.Close() }()
		entries, err := stream.Next(ctx)
		if err != nil && !errors.Is(err, io.EOF) {
			return out, err
		}
		if err == nil {
			a.console.PrintLines(renderMachinesTable(entries, a.console.Width))
		}
		out.MachineStats = statsValues(entries)
	}

	return out, nil
}

// deployedNames reports the devices actually deployed,
// in **schedule order** (lab.conf insertion order as reordered by `lab.dep`),
// and their collision domains, canonically sorted.
func deployedNames(lab *model.Lab, opts kathara.DeployLabOptions) (machines, links []string) {
	selected := opts.SelectedMachines
	excluded := opts.ExcludedMachines
	linkSet := map[string]struct{}{}

	for _, m := range lab.Machines() {
		if len(selected) > 0 && !selected.Has(m.Name) {
			continue
		}
		if len(excluded) > 0 && excluded.Has(m.Name) {
			continue
		}
		machines = append(machines, m.Name)
		for _, iface := range m.Interfaces() {
			if iface.Link != nil {
				linkSet[iface.Link.Name] = struct{}{}
			}
		}
		if m.IsBridged() {
			linkSet[model.BridgeLinkName] = struct{}{}
		}
	}
	for name := range linkSet {
		links = append(links, name)
	}
	slices.Sort(links)
	return machines, links
}

// resolveScenario answers the directory `lstart` parses, materialising the
// `--from-archive` tar into a private temporary directory when one was given.
func (f *lstartFlags) resolveScenario(a *app) (string, func(), error) {
	if f.fromArchive == "" {
		if f.archiveName != "" {
			return "", nil, errUsage("argument --name: only valid together with --from-archive")
		}
		path, err := a.resolveLabPath(f.directory)
		return path, nil, err
	}
	if f.fromArchive != "-" {
		return "", nil, errUsage("argument --from-archive: only `-` is accepted")
	}
	if f.archiveName == "" {
		return "", nil, errUsage("argument --name: required with --from-archive")
	}
	if f.directory != "" {
		return "", nil, errUsage("argument --from-archive: not allowed with argument -d/--directory")
	}

	dir, err := os.MkdirTemp("", "kathara-archive-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := extractScenarioArchive(a.stdin, dir); err != nil {
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}

// isIOError is Python's `except IOError` at `LstartCommand.py:169`. `IOError`
// is an alias of `OSError` in Python 3, and every file-level failure of
// `LabParser` — missing, empty, unopenable — is raised as one; a malformed
// line is a `SyntaxError` and is NOT caught, so `-F` does not rescue it.
func isIOError(err error) bool { return kerrors.Code(err) == kerrors.CodeOS }

// tristateScalar turns the CLI's three-state flag into the model's, so that a
// `None` stays absent — `Lab.add_option` and `add_global_machine_metadata` both
// gate on `is not None` (`model/Lab.py:437,450`).
func tristateScalar(v *bool) model.Scalar {
	if v == nil {
		return model.Scalar{}
	}
	return model.Bool(*v)
}

func (f *lstartFlags) terminalsValue() *bool { return pickTristate(f.terminalPair) }
func (f *lstartFlags) hosthomeValue() *bool  { return pickTristate(f.hosthomePair) }
func (f *lstartFlags) sharedValue() *bool    { return pickTristate(f.sharedPair) }

// pickTristate collapses an argparse mutually-exclusive `store_const` pair back
// into the single `dest` they share.
func pickTristate(pair [2]*tristate) *bool {
	if v := pair[0].Get(); v != nil {
		return v
	}
	return pair[1].Get()
}
