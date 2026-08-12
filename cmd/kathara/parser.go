// This file is the shared shape of the fourteen `argparse.ArgumentParser`
// constructions: `prog='kathara <cmd>'`, `description=strings[<cmd>]`,
// `epilog=wiki_description`, `add_help=False` plus an explicit `-h/--help`
// (CLI_SURFACE.md, the "Conventions" block).

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// newParser builds one sub-command's flag set.
//
// `add_help=False` followed by an explicit `-h/--help` is not a distinction
// without a difference: it is what puts the help option FIRST in the usage
// line, and what lets the top-level parser spell its help text "Show an help
// message and exit." while every sub-command says "a help".
//
// `SortFlags` is off because argparse lists options in declaration order and
// pflag's default is alphabetical. It is also what makes `VisitAll` walk the
// set in that order, which is what [parser.eachAction] renders from.
func newParser(name string) *parser {
	short := ""
	for _, c := range commandDescriptions {
		if c.Name == name {
			short = c.Short
			break
		}
	}
	cmd := &cobra.Command{
		Use:           "kathara " + name,
		Short:         short,
		Long:          short,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.Flags().SortFlags = false
	cmd.Flags().BoolP("help", "h", false, "Show a help message and exit.")

	p := &parser{
		Command:  cmd,
		metavars: map[string]string{},
		optNames: map[string][]string{},
		aliasFor: map[string]string{},
		listKind: map[string]nargsKind{},
	}
	// argparse's layout, not cobra's: the `usage:` line, the description, the
	// positional and option blocks, then the epilog every sub-command carries.
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		_, _ = fmt.Fprint(c.OutOrStderr(), p.usage())
		return nil
	})
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		_, _ = fmt.Fprint(c.OutOrStdout(), p.usage())
	})
	return p
}

// bindConst declares an `action='store_const'` option: it takes no value, and
// setting it writes the flag's `const` into the tri-state it shares with its
// mutually-exclusive twin.
//
// `NoOptDefVal` is what tells pflag the option is valueless; pflag consults
// that field and not the `IsBoolFlag` interface when it parses a long flag.
func bindConst(flags *pflag.FlagSet, value *tristate, name, shorthand, usage string) {
	flags.VarP(value, name, shorthand, usage)
	flags.Lookup(name).NoOptDefVal = "true"
}

// bindList declares an option argparse gives `nargs='+'` or `nargs='*'`.
//
// [expandGreedy] has already split the value run into one occurrence per value,
// so pflag sees an ordinary repeatable option; the `nargs='*'` case
// additionally needs `NoOptDefVal`, because a bare `-o` is legal there and
// parses as the empty list rather than as absent.
//
// The sentinel that carries that "bare occurrence" is deliberately unprintable,
// so the metavar and the `nargs` are recorded on the parser rather than left
// for pflag's own `FlagUsages` to interpolate into the help text.
func bindList(p *parser, value *stringList, name, shorthand, metavar, usage string, kind greedyKind) {
	flags := p.Flags()
	flags.VarP(value, name, shorthand, usage)
	p.meta(name, metavar)
	if kind == greedyZeroOrMore {
		flags.Lookup(name).NoOptDefVal = emptyListSentinel
		p.listKind[name] = nargsZeroOrMore
		return
	}
	p.listKind[name] = nargsOneOrMore
}

// renderMachinesTable is `cli/ui/utils.create_lab_table`: the columns are the
// keys of the backend's own `to_dict()` **minus `FORBIDDEN_TABLE_COLUMNS`**
// (`cli/ui/utils.py:25,79` — the list holds exactly `container_name`),
// upper-cased with underscores turned into spaces, and the rows are `str()` of
// every value.
//
// The two backends therefore disagree about more than one column, because the
// filter is by key and only one of the two dicts has that key. Human mode
// follows each dict; JSON mode does not, and must not — its `container_name` is
// canonical across both backends (JSON_CLI_CONTRACT.md §3.0.2).
//
//   - Docker (`DockerMachineStats.to_dict()`): `network_scenario_id`, `name`,
//     `container_name`, `user`, `status`, `image` — the filter drops
//     `container_name`, leaving four plus the id.
//   - Kubernetes (`KubernetesMachineStats.to_dict()`): `network_scenario_id`,
//     `name`, `pod_name`, `image`, `status`, `assigned_node`. There is no
//     `container_name` key, so the filter drops NOTHING: `POD NAME` is a
//     column, there is no `USER` column at all (the class has no such field),
//     and `IMAGE` precedes `STATUS`. CLI_SURFACE.md §13 describes only the
//     Docker shape; see the erratum row in RULINGS.md.
//
// The resource-sampling keys (`pids`, `cpu_usage`, `mem_usage`, `mem_percent`,
// `net_usage`, `interfaces`) are absent from both because PORT_SPEC §0.3 defers
// them; the inventory keys are not.
func renderMachinesTable(entries []kathara.MachineStatsEntry, width int) []string {
	timestamp := cliout.Timestamp(nowFunc())
	if len(entries) == 0 {
		return cliout.EmptyBlock(timestamp, "No Devices Found", width)
	}

	columns := machineTableColumns(entries[0].Stats)
	headers := make([]string, 0, len(columns))
	for _, key := range columns {
		headers = append(headers, cliout.ColumnHeader(key))
	}

	rows := make([][]string, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, statsRow(entry.Stats, columns))
	}

	table := &cliout.Table{
		Title:     timestamp,
		Box:       &cliout.BoxSquareDoubleHead,
		ShowLines: true,
		Expand:    true,
		Columns:   headers,
		Rows:      rows,
	}
	return table.Render(width)
}

// machineTableColumns picks which `to_dict()` the header row is quoting.
//
// `assigned_node` is the discriminator because it is the one key whose very
// presence is backend-specific: `DockerMachineStats.to_dict()` has no such key
// and the Docker backend leaves [kathara.OptionalString] at its absent zero
// value, while `KubernetesMachineStats.to_dict()` always has it — as a string
// once the pod is scheduled and as null while it is Pending, which is why the
// test is `Present`, not `Value`. Python needs no such test: it reads the keys
// off whichever object the manager handed it.
//
// Python takes the header row from the FIRST record too (`if not table.columns`
// inside the loop, `cli/ui/utils.py:81`), so a stream is described by its head
// in both. A nil head — which an implementation never produces inside a slice
// ([kathara.MachineStatsEntry]) — reads as Docker.
func machineTableColumns(head *kathara.MachineStats) []string {
	if head != nil && head.AssignedNode.Present() {
		return []string{"network_scenario_id", "name", "pod_name", "image", "status", "assigned_node"}
	}
	return []string{"network_scenario_id", "name", "user", "status", "image"}
}

// statsRow renders one inventory record. A nil `user` or `status` prints as
// Python's `str(None)`, i.e. "None": the table stringifies every value with
// `str()` (`cli/ui/utils.py:85`) and does not special-case the two `Optional`
// fields.
func statsRow(s *kathara.MachineStats, columns []string) []string {
	row := make([]string, 0, len(columns))
	for _, key := range columns {
		switch key {
		case "network_scenario_id":
			row = append(row, s.NetworkScenarioID)
		case "name":
			row = append(row, s.Name)
		case "pod_name":
			// `KubernetesMachineStats.pod_name`, which is the same field the
			// canonical JSON key `container_name` carries (§3.0.2).
			row = append(row, s.ContainerName)
		case "user":
			row = append(row, pyStr(s.User))
		case "status":
			row = append(row, pyStr(s.Status))
		case "image":
			row = append(row, s.Image)
		case "assigned_node":
			if v, ok := s.AssignedNode.Value(); ok {
				row = append(row, v)
			} else {
				row = append(row, "None")
			}
		}
	}
	return row
}

// pyStr is `str(x)` for an `Optional[str]`: "None" when it is not there.
func pyStr(s *string) string {
	if s == nil {
		return "None"
	}
	return *s
}

// statsValues flattens the stream entries into the slice the `machine_stats`
// key of E1 carries, sorted by device name (JSON_CLI_CONTRACT.md §3.1).
//
// The stream itself is sorted by the API object's id — the container or pod
// name — which is not the device name, so the sort is not redundant.
func statsValues(entries []kathara.MachineStatsEntry) []*kathara.MachineStats {
	out := make([]*kathara.MachineStats, 0, len(entries))
	for _, entry := range entries {
		if entry.Stats != nil {
			out = append(out, entry.Stats)
		}
	}
	slices.SortStableFunc(out, func(a, b *kathara.MachineStats) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// renderPlainTable is the two-column, boxless `rich.Table` of
// `Kathara.strings.formatted_strings()`.
func renderPlainTable(rows [][]string, width int) []string {
	return cliout.RenderPlainTable(rows, width)
}
