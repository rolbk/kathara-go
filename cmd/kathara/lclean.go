// This file is `cli/command/LcleanCommand.py` (CLI_SURFACE.md §2) plus the
// `--lab-hash`/`--lab-name` addressing of JSON_CLI_CONTRACT.md §8, which
// `lclean` needs in order to tear down a scenario deployed with
// `--from-archive` — one that has no directory to point `-d` at.

package main

import (
	"context"
	"errors"
	"io"
	"slices"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/labfile"
	"github.com/KatharaFramework/kathara-go/model"
)

// lcleanFlags is CLI_SURFACE.md §2 plus §8.
type lcleanFlags struct {
	directory string
	labHash   string
	labName   string
	excluded  *stringList
}

func newLcleanCmd(a *app) *commandSpec {
	cmd := newParser("lclean")
	f := &lcleanFlags{excluded: &stringList{}}
	flags := cmd.Flags()
	flags.StringVarP(&f.directory, "directory", "d", "",
		"Specify the folder containing the network scenario.")
	cmd.meta("directory", "DIRECTORY")
	registerLabRef(cmd, &f.labHash, &f.labName)
	cmd.exclusiveGroup("directory", "lab-hash", "lab-name")
	bindList(cmd, f.excluded, "exclude", "", "DEVICE_NAME",
		"Exclude specified devices from clean.", greedyOneOrMore)
	registerFormat(cmd, false)
	cmd.pos("DEVICE_NAME", nargsZeroOrMore, "Clean only specified devices.")

	return &commandSpec{
		Name:   "lclean",
		Cmd:    cmd,
		Greedy: []greedySpec{{long: "exclude", kind: greedyOneOrMore}},
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			result, err := runLclean(ctx, a, f, positional)
			a.lastLcleanResult = result
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

// runLclean is `LcleanCommand.run`.
func runLclean(ctx context.Context, a *app, f *lcleanFlags, selected []string) (cliout.LcleanResult, error) {
	var out cliout.LcleanResult

	lab, err := a.resolveRunningLab(f.directory, f.labHash, f.labName)
	if err != nil {
		return out, err
	}
	out.Lab = asLabObject(lab, f.labHash == "" && f.labName == "")

	a.console.PrintPanel("Stopping Network Scenario", cliout.PanelOptions{Justify: cliout.JustifyCenter})

	mgr, err := a.manager(ctx)
	if err != nil {
		return out, err
	}

	// The two filters are `is not None`-tested below the manager layer, which
	// inverts what an empty set means: nil is "everything", a non-nil empty
	// set is "nothing". Python spells that with the conditional expressions of
	// `LcleanCommand.py:70-71`, and this is the same conditional.
	opts := kathara.UndeployLabOptions{}
	if len(selected) > 0 {
		opts.SelectedMachines = kathara.NewNameSet(selected...)
	}
	if excluded := f.excluded.Values(); len(excluded) > 0 {
		opts.ExcludedMachines = kathara.NewNameSet(excluded...)
	}

	// The envelope reports what was actually removed, so the inventory is read
	// before the teardown. It is read only in machine formats: an extra API
	// call in human mode would be a change to the order of operations the
	// Layer A goldens record.
	var (
		running []kathara.MachineStatsEntry
		links   []string
	)
	if a.console.Format.Machine() {
		if running, err = snapshotMachines(ctx, mgr, kathara.LabRef{Hash: lab.Hash}, false); err != nil {
			return out, err
		}
		if links, err = snapshotLinkNames(ctx, mgr, kathara.LabRef{Hash: lab.Hash}, false); err != nil {
			return out, err
		}
	}

	if err := mgr.UndeployLab(ctx, kathara.LabRef{Hash: lab.Hash}, opts); err != nil {
		return out, err
	}

	out.Machines, out.Links = undeployedNames(lab, running, links, opts)
	return out, nil
}

// resolveRunningLab is the scenario-addressing rule the three §8 commands
// share: `--lab-hash`/`--lab-name` name a running deployment directly,
// otherwise the directory is parsed with Python's swallow-everything fallback.
//
// That fallback is `except (Exception, IOError)` — genuinely everything — which
// is what makes `kathara lclean` work in a directory that holds no lab.conf at
// all: the scenario is then `Lab(None, path=…)` and its hash comes from the
// path. What it does NOT survive is a path that cannot be opened as a
// filesystem at all: `Lab(None, path=…)` builds an `osfs` over it and
// pyfilesystem raises `CreateFailed` from the constructor, outside the `try`
// (`test/goldens/err-nonexistent-dir` records the resulting exit 1).
func (a *app) resolveRunningLab(directory, labHash, labName string) (*model.Lab, error) {
	switch {
	case labHash != "":
		lab := model.NewLab("", a.defaults())
		lab.Hash = labHash
		return lab, nil
	case labName != "":
		return model.NewLab(labName, a.defaults()), nil
	}

	labPath, err := a.resolveLabPath(directory)
	if err != nil {
		return nil, err
	}
	if err := a.loadCustomConfiguration(labPath); err != nil {
		return nil, err
	}
	if lab, err := labfile.ParseLab(labPath, labfile.DefaultConfName, a.defaults()); err == nil {
		return lab, nil
	}
	return model.NewLabFromPath(labPath, a.defaults())
}

// registerLabRef declares the two §8 flags. They are spelled `--lab-name`, not
// `--name`, so that they cannot collide with the existing `-n/--name`
// device-name flag of `lconfig`.
func registerLabRef(p *parser, labHash, labName *string) {
	p.Flags().StringVar(labHash, "lab-hash", "", "Address the running network scenario by hash.")
	p.meta("lab-hash", "LAB_HASH")
	p.Flags().StringVar(labName, "lab-name", "", "Address the running network scenario by name.")
	p.meta("lab-name", "LAB_NAME")
}

// snapshotMachines takes one step of the inventory stream, which is what
// `kathara list` takes too.
//
// allUsers has to be a parameter and not a constant false: `wipe -a` removes
// every user's devices, so an envelope built from a current-user-only listing
// would under-report exactly the case the flag exists for.
//
// The end of the stream is not an error here. Python's `create_lab_table`
// answers `StopIteration` with `None` and the command still returns 0
// (CLI_SURFACE.md §13), so an exhausted generator is an empty snapshot.
func snapshotMachines(ctx context.Context, mgr kathara.Manager, ref kathara.LabRef, allUsers bool) ([]kathara.MachineStatsEntry, error) {
	stream, err := mgr.GetMachinesStats(ctx, ref, "", allUsers)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	entries, err := stream.Next(ctx)
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	return entries, err
}

// snapshotLinkNames is the collision-domain twin, reduced to the names the
// `links` key of E2 and E4 carries, canonically sorted.
func snapshotLinkNames(ctx context.Context, mgr kathara.Manager, ref kathara.LabRef, allUsers bool) ([]string, error) {
	stream, err := mgr.GetLinksStats(ctx, ref, "", allUsers)
	if err != nil {
		return nil, err
	}
	defer func() { _ = stream.Close() }()
	entries, err := stream.Next(ctx)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Stats != nil {
			names = append(names, entry.Stats.Name)
		}
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// undeployedNames is the E2 envelope's pair, canonically sorted: Python's
// undeploy order is API-listing and pool order, which ORDERING.tsv rules
// nondeterministic (JSON_CLI_CONTRACT.md §3.2).
//
// The collision domains are the ones that were actually deployed, filtered to
// the scenario's view of what the selected devices were attached to when the
// CLI narrowed the undeploy — and then filtered again by what the *surviving*
// devices are still attached to, because `DockerLink.undeploy` reloads every
// candidate network and deletes only the ones with no containers left
// (`DockerLink.py:170`). A collision domain shared with a device that is not
// being removed therefore survives, and §3.2 pins `links` as the names actually
// undeployed.
func undeployedNames(lab *model.Lab, running []kathara.MachineStatsEntry, deployedLinks []string, opts kathara.UndeployLabOptions) (machines, links []string) {
	removed := map[string]struct{}{}
	for _, entry := range running {
		if entry.Stats == nil {
			continue
		}
		name := entry.Stats.Name
		if opts.SelectedMachines != nil && !opts.SelectedMachines.Has(name) {
			continue
		}
		if opts.ExcludedMachines != nil && opts.ExcludedMachines.Has(name) {
			continue
		}
		machines = append(machines, name)
		removed[name] = struct{}{}
	}
	slices.Sort(machines)

	if opts.SelectedMachines == nil && opts.ExcludedMachines == nil {
		// No filter: everything the scenario had deployed goes.
		return machines, deployedLinks
	}

	linksOf := func(name string) []string {
		machine, err := lab.GetMachine(name)
		if err != nil {
			return nil
		}
		var out []string
		for _, iface := range machine.Interfaces() {
			if iface.Link != nil {
				out = append(out, iface.Link.Name)
			}
		}
		return out
	}

	kept := map[string]struct{}{}
	for _, name := range machines {
		for _, link := range linksOf(name) {
			kept[link] = struct{}{}
		}
	}
	for _, entry := range running {
		if entry.Stats == nil {
			continue
		}
		if _, gone := removed[entry.Stats.Name]; gone {
			continue
		}
		for _, link := range linksOf(entry.Stats.Name) {
			delete(kept, link)
		}
	}

	for _, name := range deployedLinks {
		if _, ok := kept[name]; ok {
			links = append(links, name)
		}
	}
	return machines, links
}
