package main

import (
	"context"
	"slices"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/spf13/pflag"
)

type vstartFlags struct {
	// flags is the parsed set, kept because the string options below have to
	// be told apart by *presence* and not by emptiness: `Machine.update_meta`
	// gates each of them on `is not None`, so `--mem ""` records an empty
	// `mem` meta where an absent `--mem` records none.
	flags *pflag.FlagSet

	terminals    [3]*tristate
	numTerms     string
	privileged   *tristate
	name         string
	eths         *stringList
	execCommands *stringList
	mem          string
	cpus         string
	image        string
	hosthomePair [2]*tristate
	terminalEmu  string
	dryMode      bool
	bridged      bool
	ports        *stringList
	sysctls      *stringList
	envs         *stringList
	ulimits      *stringList
	volumes      *stringList
	shell        string
	entrypoint   string
}

func newVstartCmd(a *app) *commandSpec {
	cmd := newParser("vstart")
	f := &vstartFlags{
		privileged:   &tristate{constant: true},
		eths:         &stringList{validate: validateEth},
		execCommands: &stringList{},
		ports:        &stringList{},
		sysctls:      &stringList{},
		envs:         &stringList{},
		ulimits:      &stringList{},
		volumes:      &stringList{validate: volumeSpec},
	}
	flags := cmd.Flags()

	noTerminals := &tristate{constant: false}
	yesTerminals := &tristate{constant: true}
	bindConst(flags, noTerminals, "noterminals", "", "Start the device without opening a terminal window.")
	bindConst(flags, yesTerminals, "terminals", "", "Start the device opening its terminal window.")
	flags.StringVar(&f.numTerms, "num_terms", "", "Choose the number of terminals to open for the device.")
	cmd.meta("num_terms", "NUM_TERMS")
	// MEG-1 is three-way here, not two: `--num_terms` shares the group.
	cmd.exclusiveGroup("noterminals", "terminals", "num_terms")

	bindConst(flags, f.privileged, "privileged", "",
		"Start the device in privileged mode. MUST BE ROOT FOR THIS OPTION.")
	flags.StringVarP(&f.name, "name", "n", "", "Name of the device to be started.")
	cmd.meta("name", "DEVICE_NAME")
	cmd.require("name")
	bindList(cmd, f.eths, "eth", "", "N:CD/MAC", "Set a specific interface on a collision domain.", greedyOneOrMore)
	bindList(cmd, f.execCommands, "exec", "e", "EXEC_COMMANDS",
		"Execute a specific command in the device during startup.", greedyZeroOrMore)
	flags.StringVar(&f.mem, "mem", "", "Limit the amount of RAM available for this device.")
	cmd.meta("mem", "MEM")
	flags.StringVar(&f.cpus, "cpus", "", "Limit the amount of CPU available for this device.")
	cmd.meta("cpus", "CPUS")
	flags.StringVarP(&f.image, "image", "i", "", "Run this device with a specific Docker Image.")
	cmd.meta("image", "IMAGE")

	noHosthome := &tristate{constant: false}
	yesHosthome := &tristate{constant: true}
	bindConst(flags, noHosthome, "no-hosthome", "H", `Do not mount "/hosthome" directory inside the device.`)
	cmd.names("no-hosthome", "--no-hosthome", "-H")
	bindConst(flags, yesHosthome, "hosthome", "", `Mount "/hosthome" directory inside the device.`)
	cmd.exclusiveGroup("no-hosthome", "hosthome")

	flags.StringVar(&f.terminalEmu, "terminal-emu", "",
		"Set a different terminal emulator application (Unix only).")
	cmd.meta("terminal-emu", "TERMINAL_EMU")
	flags.BoolVar(&f.dryMode, "print", false, "Check if the device parameters are correct (dry run).")
	flags.BoolVar(&f.dryMode, "dry-run", false, "Check if the device parameters are correct (dry run).")
	cmd.alias("dry-run", "print")
	cmd.names("print", "--print", "--dry-run")
	flags.BoolVar(&f.bridged, "bridged", false, "Add a bridge interface to the device.")
	bindList(cmd, f.ports, "port", "", "[HOST:]GUEST[/PROTOCOL]",
		"Map localhost port HOST to the internal port GUEST of the device for the specified PROTOCOL.",
		greedyOneOrMore)
	bindList(cmd, f.sysctls, "sysctl", "", "SYSCTL", "Set sysctl option for the device.", greedyOneOrMore)
	bindList(cmd, f.envs, "env", "", "ENV", "Set environment variable for the device.", greedyOneOrMore)
	bindList(cmd, f.ulimits, "ulimit", "", "KEY=SOFT[:HARD]", "Set ulimit for the device.", greedyOneOrMore)
	bindList(cmd, f.volumes, "volume", "", "HOST|GUEST|[MODE]", "Specify a volume to mount.", greedyOneOrMore)
	flags.StringVar(&f.shell, "shell", "",
		"Set the shell (sh, bash, etc.) that should be used inside the device.")
	cmd.meta("shell", "SHELL")
	flags.StringVar(&f.entrypoint, "entrypoint", "", "Specify the entrypoint command of the device.")
	cmd.meta("entrypoint", "ENTRYPOINT")
	registerFormat(cmd, false)
	cmd.pos("ARG", nargsRemainder, "Specify extra arguments for the entrypoint command.")

	f.flags = flags
	f.terminals = [3]*tristate{noTerminals, yesTerminals, nil}
	f.hosthomePair = [2]*tristate{noHosthome, yesHosthome}

	return &commandSpec{
		Name:      "vstart",
		Cmd:       cmd,
		Remainder: true,
		Greedy: []greedySpec{
			{long: "eth", kind: greedyOneOrMore},
			{long: "exec", short: "e", kind: greedyZeroOrMore},
			{long: "port", kind: greedyOneOrMore},
			{long: "sysctl", kind: greedyOneOrMore},
			{long: "env", kind: greedyOneOrMore},
			{long: "ulimit", kind: greedyOneOrMore},
			{long: "volume", kind: greedyOneOrMore},
		},
		Run: func(ctx context.Context, a *app, _, remainder []string) (int, error) {
			result, err := runVstart(ctx, a, f, remainder)
			if err != nil {
				return 1, err
			}
			a.console.Emit(result)
			return 0, nil
		},
	}
}

// runVstart is `VstartCommand.run`.
func runVstart(ctx context.Context, a *app, f *vstartFlags, remainder []string) (cliout.VstartResult, error) {
	var out cliout.VstartResult

	title := "Starting Device `" + f.name + "`"
	if f.dryMode {
		title = "Checking Device `" + f.name + "`"
	}
	a.console.PrintPanel(title, cliout.PanelOptions{Justify: cliout.JustifyCenter})

	// Dry mode returns HERE, before the settings overrides, before the lab is
	// built and before any value is validated: it proves only that argparse
	// accepted the flags (`VstartCommand.py:204-206`).
	if f.dryMode {
		a.console.Print("✓ " + f.name + " configuration is correct.")
		out.Lab = cliout.Lab{Name: cliout.Str(vlabName), Hash: a.newVlab().Hash}
		out.Machine = f.name
		out.DryRun = true
		return out, nil
	}

	if v := pickTristate([2]*tristate{f.terminals[0], f.terminals[1]}); v != nil {
		a.settings.OpenTerminals = *v
	}
	if f.terminalEmu != "" {
		a.settings.Terminal = f.terminalEmu
	}
	if f.shell != "" {
		a.settings.DeviceShell = f.shell
	}

	lab := a.newVlab()
	lab.AddOption("hosthome_mount", tristateScalar(pickTristate(f.hosthomePair)))
	// `shared_mount` is force-disabled for vstart, unconditionally.
	lab.AddOption("shared_mount", model.Bool(false))

	privileged := f.privileged.Get()
	if privileged != nil && *privileged {
		admin, err := util.IsAdmin()
		if err != nil {
			return out, err
		}
		if !admin {
			return out, kerrors.ErrPrivilegeDevicePrivileged
		}
		if a.settings.OpenTerminals {
			// Singular "terminal" here, plural in lstart. Both are Python's.
			a.console.Print("⚠ Running devices with privileged capabilities, terminal might not open!")
		}
	}
	lab.AddGlobalMachineMetadata("privileged", tristateScalar(privileged))

	// `if args['args'] and args['args'][0] == "--": args['args'] = args['args'][1:]`
	args := remainder
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}

	opts := f.metaOptions(args, privileged)
	device, err := lab.GetOrNewMachine(f.name, opts)
	if err != nil {
		return out, err
	}

	var links []string
	for _, raw := range f.eths.Values() {
		spec, err := interfaceCDMAC(raw)
		if err != nil {
			// Unreachable: the flag validator already rejected it with exit 2.
			return out, err
		}
		number, err := util.PyInt(spec.Number)
		if err != nil {
			// `except ValueError: raise SyntaxError(…)` — exit 1, not 2.
			suffix := spec.CD
			if spec.MAC != "" {
				suffix = spec.CD + "/" + spec.MAC
			}
			return out, kerrors.NewSyntaxEthNumber(spec.Number, suffix)
		}
		if _, _, err := lab.ConnectMachineToLink(device.Name, spec.CD, model.AddInterfaceOptions{
			Number: model.InterfaceNumber(number),
			MAC:    spec.MAC,
		}); err != nil {
			return out, err
		}
		links = append(links, spec.CD)
	}

	// Volumes are added AFTER the interfaces, as machine metas, because
	// `VstartCommand.run` pops them out of the kwargs and applies them by hand
	// (`VstartCommand.py:230,245-247`).
	for _, v := range f.volumes.Values() {
		if _, _, err := device.AddMeta("volume", v); err != nil {
			return out, err
		}
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return out, err
	}
	if err := mgr.DeployLab(ctx, lab, kathara.DeployLabOptions{}); err != nil {
		return out, err
	}

	if f.bridged {
		links = append(links, model.BridgeLinkName)
	}
	slices.Sort(links)
	links = slices.Compact(links)

	out.Lab = asLabObject(lab, false)
	out.Machine = f.name
	out.Links = links
	return out, nil
}

// metaOptions is the `**args` that `lab.get_or_new_machine(name, **args)`
// receives after `name`, `volumes` and `eths` have been popped.
func (f *vstartFlags) metaOptions(args []string, privileged *bool) *model.MetaOptions {
	opts := &model.MetaOptions{
		ExecCommands: f.execCommands.Values(),
		Ports:        f.ports.Values(),
		Sysctls:      f.sysctls.Values(),
		Envs:         f.envs.Values(),
		Ulimits:      f.ulimits.Values(),
		Args:         args,
		Privileged:   privileged,
	}
	// `is not None`, not truthiness: an explicitly empty value is a value.
	// `--num_terms ""` therefore reaches `Machine.update_meta` and fails there
	// as a `MachineOptionError`, and `--image ""` deploys with an empty image
	// name — both of which are Python's answers.
	if f.flags.Changed("num_terms") {
		opts.NumTerms = &f.numTerms
	}
	if f.flags.Changed("mem") {
		opts.Mem = &f.mem
	}
	if f.flags.Changed("cpus") {
		opts.CPUs = &f.cpus
	}
	if f.flags.Changed("image") {
		opts.Image = &f.image
	}
	if f.flags.Changed("shell") {
		opts.Shell = &f.shell
	}
	if f.flags.Changed("entrypoint") {
		opts.Entrypoint = &f.entrypoint
	}
	if f.bridged {
		bridged := true
		opts.Bridged = &bridged
	}
	return opts
}

// newVcleanCmd is `VcleanCommand.__init__` — two flags, both of them options.
func newVcleanCmd(a *app) *commandSpec {
	cmd := newParser("vclean")
	var name string
	cmd.Flags().StringVarP(&name, "name", "n", "", "The name of the device to clean.")
	cmd.meta("name", "DEVICE_NAME")
	cmd.require("name")
	registerFormat(cmd, false)

	return &commandSpec{
		Name: "vclean",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			result, err := runVclean(ctx, a, name)
			if err != nil {
				return 1, err
			}
			a.console.Emit(result)
			return 0, nil
		},
	}
}

// runVclean is `VcleanCommand.run`.
func runVclean(ctx context.Context, a *app, name string) (cliout.VcleanResult, error) {
	lab := a.newVlab()
	out := cliout.VcleanResult{Lab: asLabObject(lab, false), Machine: name}

	a.console.PrintPanel("Stopping Device `"+name+"`", cliout.PanelOptions{Justify: cliout.JustifyCenter})

	mgr, err := a.manager(ctx)
	if err != nil {
		return out, err
	}

	if a.console.Format.Machine() {
		running, err := snapshotMachines(ctx, mgr, kathara.LabRef{Name: vlabName}, false)
		if err != nil {
			return out, err
		}
		for _, entry := range running {
			if entry.Stats != nil && entry.Stats.Name == name {
				out.Machines = append(out.Machines, name)
			}
		}
	}

	err = mgr.UndeployLab(ctx, kathara.LabRef{Name: vlabName}, kathara.UndeployLabOptions{
		SelectedMachines: kathara.NewNameSet(name),
	})
	return out, err
}
