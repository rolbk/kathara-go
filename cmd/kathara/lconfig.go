// This file is `cli/command/LconfigCommand.py` (CLI_SURFACE.md §5) and
// `cli/command/VconfigCommand.py` (§8), which are the same command over two
// scenarios: one parsed from a directory, one the fixed `kathara_vlab`.
//
// §0.2 #3 collapses the second onto the first. What survives the collapse is
// the one behavioural difference between them: `vconfig` fetches the backend's
// own handles for the device and for each collision domain it detaches
// (`VconfigCommand.py:63,85`), while `lconfig` relies on
// `update_lab_from_api` having filled them.

package main

import (
	"context"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/labfile"
	"github.com/KatharaFramework/kathara-go/model"
)

// configFlags is the shared surface of `lconfig` and `vconfig`.
type configFlags struct {
	directory string
	labHash   string
	labName   string
	name      string
	toAdd     *stringList
	toRemove  *stringList
}

// registerConfigFlags declares the rows of CLI_SURFACE.md §5/§8. The
// `--add`/`--rm` group is `required=True`, so omitting both is a usage error.
//
// `--rm` validates its values here, through `alphanumeric`'s
// `ArgumentTypeError`; `--add` does NOT, because `cd_mac` raises a bare
// `SyntaxError` that argparse never catches (JSON_CLI_CONTRACT.md A9).
func registerConfigFlags(cmd *parser, lab bool) *configFlags {
	f := &configFlags{
		toAdd:    &stringList{},
		toRemove: &stringList{validate: alphanumeric},
	}
	flags := cmd.Flags()
	if lab {
		flags.StringVarP(&f.directory, "directory", "d", "",
			"Path of the network scenario to configure, if not specified the current path is used")
		cmd.meta("directory", "LAB_PATH")
		registerLabRef(cmd, &f.labHash, &f.labName)
		cmd.exclusiveGroup("directory", "lab-hash", "lab-name")
		flags.StringVarP(&f.name, "name", "n", "", "Name of the device to configure.")
	} else {
		flags.StringVarP(&f.name, "name", "n", "",
			"Name of the device to be connected on desired collision domains.")
	}
	cmd.meta("name", "DEVICE_NAME")
	cmd.require("name")

	bindList(cmd, f.toAdd, "add", "", "CD/MAC", "Specify the collision domain to add.", greedyOneOrMore)
	bindList(cmd, f.toRemove, "rm", "", "CD", "Specify the collision domain to remove.", greedyOneOrMore)
	cmd.requiredGroup("add", "rm")
	registerFormat(cmd, false)
	return f
}

func newLconfigCmd(a *app) *commandSpec {
	cmd := newParser("lconfig")
	f := registerConfigFlags(cmd, true)
	return &commandSpec{
		Name: "lconfig",
		Cmd:  cmd,
		Greedy: []greedySpec{
			{long: "add", kind: greedyOneOrMore},
			{long: "rm", kind: greedyOneOrMore},
		},
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			result, err := runConfig(ctx, a, f, true)
			if err != nil {
				return 1, err
			}
			a.console.Emit(result)
			return 0, nil
		},
	}
}

func newVconfigCmd(a *app) *commandSpec {
	cmd := newParser("vconfig")
	f := registerConfigFlags(cmd, false)
	return &commandSpec{
		Name: "vconfig",
		Cmd:  cmd,
		Greedy: []greedySpec{
			{long: "add", kind: greedyOneOrMore},
			{long: "rm", kind: greedyOneOrMore},
		},
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			result, err := runConfig(ctx, a, f, false)
			if err != nil {
				return 1, err
			}
			a.console.Emit(result)
			return 0, nil
		},
	}
}

// runConfig is `LconfigCommand.run` and `VconfigCommand.run`.
func runConfig(ctx context.Context, a *app, f *configFlags, isLab bool) (cliout.ConfigResult, error) {
	var out cliout.ConfigResult

	// `--add` values are converted here and not in the flag parser: their
	// failure is a `SyntaxError` with exit 1, not a usage error with exit 2.
	added := make([]cliout.AddedLink, 0, len(f.toAdd.Values()))
	for _, value := range f.toAdd.Values() {
		cd, mac, err := cdMAC(value)
		if err != nil {
			return out, err
		}
		added = append(added, cliout.AddedLink{Link: cd, MAC: mac})
	}

	var lab *model.Lab
	if isLab {
		// No fallback here, unlike `lclean`/`linfo`: a broken or missing
		// lab.conf propagates (`LconfigCommand.py:71`).
		switch {
		case f.labHash != "" || f.labName != "":
			var err error
			if lab, err = a.resolveRunningLab("", f.labHash, f.labName); err != nil {
				return out, err
			}
		default:
			labPath, err := a.resolveLabPath(f.directory)
			if err != nil {
				return out, err
			}
			if err := a.loadCustomConfiguration(labPath); err != nil {
				return out, err
			}
			if lab, err = labfile.ParseLab(labPath, labfile.DefaultConfName, a.defaults()); err != nil {
				return out, err
			}
		}
	} else {
		lab = a.newVlab()
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return out, err
	}
	if err := mgr.UpdateLabFromAPI(ctx, lab); err != nil {
		return out, err
	}

	device, err := lab.GetMachine(f.name)
	if err != nil {
		return out, err
	}
	if !isLab {
		// `device.api_object = Kathara.get_machine_api_object(name,
		// lab_name=lab.name)` — vconfig only.
		obj, err := mgr.GetMachineAPIObject(ctx, f.name, kathara.LabRef{Name: lab.Name()}, false)
		if err != nil {
			return out, err
		}
		device.APIObject = obj
	}

	title := "Updating Device `" + f.name + "`"
	if isLab {
		title = "Updating Network Scenario Device `" + f.name + "`"
	}
	a.console.PrintPanel(title, cliout.PanelOptions{Justify: cliout.JustifyCenter})

	out.Lab = asLabObject(lab, isLab && f.labHash == "" && f.labName == "")
	out.Machine = f.name

	for _, entry := range added {
		line := "+ Adding interface to device `" + f.name + "` on collision domain `" + entry.Link + "`"
		if entry.MAC != "" {
			line += " with MAC Address " + entry.MAC
		}
		a.console.Print(line + "...")

		link := lab.GetOrNewLink(entry.Link)
		if err := mgr.ConnectMachineToLink(ctx, device, link, entry.MAC); err != nil {
			return out, err
		}
	}
	out.Added = added

	for _, cd := range f.toRemove.Values() {
		a.console.Print("- Removing interface on collision domain `" + cd + "` from device `" + f.name + "`...")

		link, err := lab.GetLink(cd)
		if err != nil {
			return out, err
		}
		if !isLab {
			obj, err := mgr.GetLinkAPIObject(ctx, cd, kathara.LabRef{Name: lab.Name()}, false)
			if err != nil {
				return out, err
			}
			link.APIObject = obj
		}
		if err := mgr.DisconnectMachineFromLink(ctx, device, link, false); err != nil {
			return out, err
		}
		out.Removed = append(out.Removed, cd)
	}

	return out, nil
}
