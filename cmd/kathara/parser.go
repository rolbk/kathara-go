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
func bindConst(flags *pflag.FlagSet, value *tristate, name, shorthand, usage string) {
	flags.VarP(value, name, shorthand, usage)
	flags.Lookup(name).NoOptDefVal = "true"
}

// bindList declares an option argparse gives `nargs='+'` or `nargs='*'`.
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
