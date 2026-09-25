package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/labfile"
	"github.com/KatharaFramework/kathara-go/model"
)

type listFlags struct {
	all   bool
	watch bool
	name  string
}

func newListCmd(a *app) *commandSpec {
	cmd := newParser("list")
	f := &listFlags{}
	flags := cmd.Flags()
	flags.BoolVarP(&f.all, "all", "a", false,
		"Show all running Kathara devices of all users. MUST BE ROOT FOR THIS OPTION.")
	// argparse declares one option with four spellings; pflag needs the long
	// name plus a shorthand, so the two extra aliases are registered
	// separately and folded back onto one row by [parser.alias], which is where
	// argparse would have shown them all.
	flags.BoolVarP(&f.watch, "watch", "w", false, "Watch mode.")
	flags.BoolVarP(&f.watch, "live", "l", false, "Watch mode.")
	_ = flags.MarkHidden("live")
	cmd.alias("live", "watch")
	cmd.names("watch", "-w", "-l", "--watch", "--live")
	flags.StringVarP(&f.name, "name", "n", "", "Show only information about a specified device.")
	cmd.meta("name", "DEVICE_NAME")
	registerFormat(cmd, false)

	return &commandSpec{
		Name: "list",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			return runList(ctx, a, f)
		},
	}
}

// runList is `ListCommand.run`.
func runList(ctx context.Context, a *app, f *listFlags) (int, error) {
	if f.all {
		admin, err := util.IsAdmin()
		if err != nil {
			return 1, err
		}
		if !admin {
			return 1, kerrors.ErrPrivilegeListAllUsers
		}
	}
	if f.watch && a.console.Format.Machine() {

		return 2, errUsage("argument -w/--watch: not allowed with --format %s", a.console.Format)
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return 1, err
	}
	stream, err := mgr.GetMachinesStats(ctx, kathara.LabRef{}, f.name, f.all)
	if err != nil {
		return 1, err
	}
	defer func() { _ = stream.Close() }()

	if f.watch {
		return runListWatch(ctx, a, stream)
	}

	entries, err := stream.Next(ctx)
	if errors.Is(err, io.EOF) {
		a.console.Emit(cliout.ListResult{})
		return 0, nil
	}
	if err != nil {
		return 1, err
	}
	a.console.PrintLines(renderMachinesTable(entries, a.console.Width))
	a.console.Emit(cliout.ListResult{Machines: statsValues(entries)})
	return 0, nil
}

// runListWatch is `ListCommand._get_live_info`: redraw the table until the
// stream ends, which is Python's `while True` around `create_lab_table` broken
// by the `if not table` guard.
func runListWatch(ctx context.Context, a *app, stream kathara.MachinesStatsStream) (int, error) {
	for {
		entries, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			return 0, nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil
			}
			return 1, err
		}
		a.console.ClearScreen()
		a.console.PrintLines(renderMachinesTable(entries, a.console.Width))

		select {
		case <-ctx.Done():
			return 0, nil
		case <-tickerC():
		}
	}
}

// newLinfoCmd is LinfoCommand's parser and dispatcher.
func newLinfoCmd(a *app) *commandSpec {
	cmd := newParser("linfo")
	flags := cmd.Flags()
	var (
		directory string
		watch     bool
		conf      bool
		name      string
		topology  bool
	)
	flags.StringVarP(&directory, "directory", "d", "",
		"Specify the folder containing the network scenario.")
	cmd.meta("directory", "DIRECTORY")
	flags.BoolVarP(&watch, "watch", "w", false,
		"Watch mode, can be used only when a network scenario is launched.")
	flags.BoolVarP(&watch, "live", "l", false,
		"Watch mode, can be used only when a network scenario is launched.")
	_ = flags.MarkHidden("live")
	// One argparse action with four spellings, so `--live` belongs to the
	// mutually-exclusive group `--watch` is in: `linfo -l -c` is a usage error
	// in Python and would otherwise have slipped past it here.
	cmd.alias("live", "watch")
	cmd.names("watch", "-w", "-l", "--watch", "--live")
	flags.BoolVarP(&conf, "conf", "c", false, "Read static information from lab.conf.")
	cmd.exclusiveGroup("watch", "conf")
	flags.StringVarP(&name, "name", "n", "", "Show only information about a specified device.")
	cmd.meta("name", "DEVICE_NAME")
	flags.BoolVarP(&topology, "topology", "t", false, "Get running topology info")
	cmd.exclusiveGroup("name", "topology")

	registerFormat(cmd, false)

	return &commandSpec{
		Name: "linfo",
		Cmd:  cmd,
		Run: func(ctx context.Context, a *app, _, _ []string) (int, error) {
			if watch && a.console.Format.Machine() {
				return 2, errUsage("argument -w/--watch: not allowed with --format %s", a.console.Format)
			}
			result, err := runLinfo(ctx, a, directory, watch, conf, name, topology)
			if err != nil {
				return 1, err
			}
			a.console.Emit(result)
			return 0, nil
		},
	}
}

// runLinfo parses the scenario when possible, but keeps the original
// LinfoCommand fallback to an empty Lab when lab.conf is missing or malformed.
func runLinfo(ctx context.Context, a *app, directory string, watch, conf bool, name string, topology bool) (cliout.LinfoResult, error) {
	var out cliout.LinfoResult
	path, err := a.resolveLabPath(directory)
	if err != nil {
		return out, err
	}
	if err := a.loadCustomConfiguration(path); err != nil {
		return out, err
	}
	lab, err := labfile.ParseLab(path, labfile.DefaultConfName, a.defaults())
	if err != nil {
		lab, err = model.NewLabFromPath(path, a.defaults())
		if err != nil {
			return out, err
		}
	}
	out.Lab = asLabObject(lab, true)

	if conf {
		out.Mode = "conf"
		if name != "" {
			machine, err := lab.GetMachine(name)
			if err != nil {
				return out, err
			}
			out.MachineText = machine.String()
			a.console.PrintPanel(out.MachineText, cliout.PanelOptions{Title: name + " Information"})
			return out, nil
		}
		if meta := lab.String(); meta != "" {
			a.console.PrintPanel(meta, cliout.PanelOptions{Title: "Network Scenario Information"})
		}
		out.DeviceCount = len(lab.Machines())
		out.LinkCount = len(lab.Links())
		if lab.HasLink(model.BridgeLinkName) {
			out.LinkCount--
		}
		a.console.PrintPanel(fmt.Sprintf("There are %d devices.\nThere are %d collision domains.",
			out.DeviceCount, out.LinkCount), cliout.PanelOptions{Title: "Topology Information"})
		return out, nil
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return out, err
	}
	for {
		switch {
		case name != "":
			out.Mode = "machine"
			stream := mgr.GetMachineStats(ctx, name, kathara.LabRef{Hash: lab.Hash}, false)
			out.Machine, err = stream.Next(ctx)
			_ = stream.Close()
			if errors.Is(err, io.EOF) {
				err = nil
			}
			if err != nil {
				return out, err
			}
			if out.Machine == nil {
				a.console.PrintPanel("Device `"+name+"` Not Found.", cliout.PanelOptions{Title: name + " Information"})
			} else {
				a.console.PrintPanel(formatLinfoMachine(out.Machine), cliout.PanelOptions{Title: name + " Information"})
			}
		case topology:
			out.Mode = "topology"
			if err := mgr.UpdateLabFromAPI(ctx, lab); err != nil {
				return out, err
			}
			out.Links = linfoLinks(lab)
			a.console.PrintLines(renderTopologyTable(out.Links, a.console.Width))
		case true:
			out.Mode = "lab"
			entries, err := snapshotMachines(ctx, mgr, kathara.LabRef{Hash: lab.Hash}, false)
			if err != nil {
				return out, err
			}
			out.Machines = statsValues(entries)
			a.console.PrintLines(renderMachinesTable(entries, a.console.Width))
		}
		if !watch {
			return out, nil
		}
		select {
		case <-ctx.Done():
			return out, nil
		case <-tickerC():
			a.console.ClearScreen()
		}
	}
}

func formatLinfoMachine(s *kathara.MachineStats) string {
	status := "None"
	if s.Status != nil {
		status = *s.Status
	}
	return fmt.Sprintf("Network Scenario ID: %s\nDevice Name: %s\nContainer Name: %s\nStatus: %s\nImage: %s",
		s.NetworkScenarioID, s.Name, s.ContainerName, status, s.Image)
}

func linfoLinks(lab *model.Lab) []cliout.LinfoLink {
	links := lab.Links()
	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })
	out := make([]cliout.LinfoLink, 0, len(links))
	for _, link := range links {
		out = append(out, cliout.LinfoLink{Name: link.Name, Machines: link.MachineNames()})
	}
	return out
}

func renderTopologyTable(links []cliout.LinfoLink, width int) []string {
	timestamp := cliout.Timestamp(nowFunc())
	if len(links) == 0 {
		return cliout.EmptyBlock(timestamp, "No Collision Domains Found", width)
	}
	rows := make([][]string, 0, len(links))
	for _, link := range links {
		rows = append(rows, []string{link.Name, strings.Join(link.Machines, ", ")})
	}
	return (&cliout.Table{Title: timestamp, Box: &cliout.BoxSquareDoubleHead,
		ShowLines: true, Columns: []string{"LINK NAME", "DEVICES"}, Rows: rows}).Render(width)
}
