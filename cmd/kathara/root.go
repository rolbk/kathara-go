// The top level is deliberately not a cobra `Execute()`.

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/spf13/pflag"
)

// version defaults to the compatibility baseline and is replaced from the
// release tag by GoReleaser.
var version = util.CurrentVersion

var commandDescriptions = []struct{ Name, Short string }{
	{"vstart", "Start a new Kathara device"},
	{"vclean", "Stop a single Kathara device"},
	{"vconfig", "Manage the network interfaces of a running Kathara device"},
	{"lstart", "Start a Kathara network scenario"},
	{"lclean", "Stop a Kathara network scenario"},
	{"linfo", "Show information about a Kathara network scenario"},
	{"lrestart", "Restart a Kathara network scenario"},
	{"lconfig", "Manage the network interfaces of a running Kathara device in a Kathara network scenario"},
	{"connect", "Connect to a Kathara device"},
	{"exec", "Execute a command in a Kathara device"},
	{"wipe", "Delete all Kathara devices and collision domains, optionally also delete settings"},
	{"list", "Show all running Kathara devices of the current user"},
	{"settings", "Show and edit Kathara settings"},
	{"check", "Check your system environment"},
}

// wikiDescription is `Kathara/strings.py`'s `wiki_description`, the epilog of
// every sub-command parser.
const wikiDescription = "For examples and further information visit: https://github.com/KatharaFramework/Kathara/wiki"

var interruptExempt = map[string]bool{
	"exec":     true,
	"linfo":    true,
	"list":     true,
	"settings": true,
	"config":   true,
}

// commandSpec is one sub-command: its cobra flag set, the argparse `nargs`
// shape [expandGreedy] and [splitRemainder] need, and its body.
type commandSpec struct {
	// Name is the word the dispatcher matches.
	Name string
	// Cmd carries the flags. It is never Execute()d; the dispatcher parses
	// through it and calls Run itself.
	Cmd *parser
	// Greedy lists the options declared with argparse `nargs='+'`/`'*'`.
	Greedy []greedySpec
	// Remainder is `vstart`'s `nargs=argparse.REMAINDER` positional.
	Remainder bool
	// Streaming widens `--format` to accept `jsonl`.
	Streaming bool
	NoParser  bool
	// Run is the command body. positional is what pflag left over; remainder
	// is the REMAINDER capture, nil unless Remainder is set.
	Run func(ctx context.Context, a *app, positional, remainder []string) (int, error)
}

// helpRequested reports whether `-h/--help` was given, which argparse answers
// by printing help to stdout and exiting 0.
func (s *commandSpec) helpRequested() bool {
	v, err := s.Cmd.Flags().GetBool("help")
	return err == nil && v
}

// valueTaking derives, from the parsed flag set, which options consume the
// following token — what [splitRemainder] needs to tell a flag's value from the
// first positional. pflag records it as "has no NoOptDefVal".
func (s *commandSpec) valueTaking() valueTaking {
	vt := valueTaking{long: map[string]bool{}, short: map[string]bool{}}
	s.Cmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.NoOptDefVal != "" {
			return
		}
		vt.long[f.Name] = true
		if f.Shorthand != "" {
			vt.short[f.Shorthand] = true
		}
	})
	return vt
}

func commandTable(a *app) map[string]*commandSpec {
	specs := []*commandSpec{
		newLstartCmd(a),
		newLcleanCmd(a),
		newLrestartCmd(a),
		newLinfoCmd(a),
		newLconfigCmd(a),
		newVstartCmd(a),
		newVcleanCmd(a),
		newVconfigCmd(a),
		newConnectCmd(a),
		newExecCmd(a),
		newListCmd(a),
		newWipeCmd(a),
		newCheckCmd(a),
		newSettingsCmd(a),
		newConfigCmd(a),
	}
	table := make(map[string]*commandSpec, len(specs))
	for _, spec := range specs {
		table[spec.Name] = spec
	}
	return table
}

func dispatch(ctx context.Context, a *app, argv []string) int {
	// Step 1-2: the top level parses one token.
	var word string
	if len(argv) > 1 {
		word = argv[1]
	}

	if word == "--" {
		// argparse strips a bare `--` before matching, so the single token it
		// was given leaves `command` at None (oracle-probed). That is the
		// "no command" case below, not an unrecognized argument.
		word = ""
	}

	switch word {
	case "-h", "--help":
		a.console.Print(topLevelHelp(a.console.Width))
		return 0
	case "-v", "--version":
		// `print('Current version: %s' % CURRENT_VERSION)` — stdout, exit 0,
		// and it happens before the settings check, so a broken settings file
		// does not hide it.
		a.console.Print(fmt.Sprintf("Current version: %s", version))
		return 0
	}

	if word != "" && strings.HasPrefix(word, "-") {
		// argparse: an unknown option in `sys.argv[1:2]` is
		// "unrecognized arguments", usage on stderr, exit 2.
		_, _ = fmt.Fprint(a.console.Err, topLevelUsage(a.console.Width))
		_, _ = fmt.Fprintf(a.console.Err, "kathara: error: unrecognized arguments: %s\n", word)
		return 2
	}

	if !isLower(word) {
		// `args.command is None or not args.command.islower()`: help on
		// stdout, exit **1**. So `kathara LSTART` is not "unrecognized", it is
		// a usage failure with the general help.
		a.console.Print(topLevelHelp(a.console.Width))
		return 1
	}

	// Step 3: the settings check, skipped for any command whose name CONTAINS
	// "settings" — a substring test, not equality (`kathara.py:71`).
	if !strings.Contains(word, "settings") && word != "config" {
		if err := a.checkSettings(); err != nil {
			a.console.Log(cliout.LevelCritical, "(%s) %s", cliout.HumanLabelOf(err), err.Error())
			return 1
		}
	}

	// Step 4: command lookup.
	table := commandTable(a)
	spec, ok := table[word]
	if !ok {
		a.console.Log(cliout.LevelError, "Unrecognized command `%s`.", word)
		a.console.Print(topLevelHelp(a.console.Width))
		return 1
	}
	a.commandName = word
	a.rawArgs = argv[2:]
	a.opCtx = ctx

	// Step 5: the sub-command parses everything from argv[2:].
	return runCommand(ctx, a, spec, argv[2:])
}

// runCommand is `command_object.run(os.getcwd(), sys.argv[2:])` with argparse's
// parse-then-dispatch split made explicit.
func runCommand(ctx context.Context, a *app, spec *commandSpec, args []string) int {
	// The REMAINDER cut comes first and is greedy-aware: after it, the head is
	// options only and can be rewritten freely, while the remainder must stay
	// byte-for-byte what the user typed.
	head, remainder := args, []string(nil)
	if !spec.NoParser {
		if spec.Remainder {
			head, remainder = splitRemainder(args, spec.valueTaking(), spec.Greedy)
		}
		head = expandGreedy(head, spec.Greedy)

		if err := spec.Cmd.ParseFlags(head); err != nil {
			return a.usageError(spec, err)
		}
		if spec.helpRequested() {
			a.console.Print(strings.TrimRight(spec.Cmd.UsageString(), "\n"))
			return 0
		}
		// argparse's own order, which cobra's ValidateFlagGroups reproduces
		// neither the whole nor the sequence of: a mutually-exclusive conflict,
		// then the `required=True` sweep, then the required groups, then the
		// leftover positionals.
		if err := spec.Cmd.validate(head); err != nil {
			return a.usageError(spec, err)
		}
	}
	if !a.suppressEmit {
		if err := a.applyFormat(spec); err != nil {
			return a.usageError(spec, err)
		}
	}

	code, err := spec.Run(ctx, a, spec.Cmd.Flags().Args(), remainder)
	if err != nil {
		if ctx.Err() != nil {
			// The failure IS the interrupt.
			return 1
		}
		if usage := asUsageError(err); usage != nil {
			return a.usageError(spec, usage)
		}
		return a.console.EmitError(err)
	}

	// Every other terminal mode has already opened its windows during the
	// deploy, which leaves this a no-op with an empty slice.
	if !a.suppressEmit {
		if err := a.runPendingTerminals(ctx); err != nil {
			if ctx.Err() != nil {
				return 1
			}
			return a.console.EmitError(err)
		}
	}
	return code
}

func (a *app) usageError(spec *commandSpec, err error) int {
	_, _ = fmt.Fprint(a.console.Err, spec.Cmd.usageBlock())
	_, _ = fmt.Fprintf(a.console.Err, "kathara %s: error: %s\n", spec.Name, err.Error())
	return 2
}

// usageError marks an error the command body raises that is nonetheless a
// usage failure — `--format jsonl` on a command that does not stream, `-w`
// under json, a `--from-archive` without `--name`. They exit 2 rather than
// producing an error envelope.
type usageErrorValue struct{ err error }

func (u *usageErrorValue) Error() string { return u.err.Error() }
func (u *usageErrorValue) Unwrap() error { return u.err }

// errUsage wraps err as a usage failure.
func errUsage(format string, args ...any) error {
	return &usageErrorValue{err: fmt.Errorf(format, args...)}
}

func asUsageError(err error) error {
	var u *usageErrorValue
	if errors.As(err, &u) {
		return u.err
	}
	return nil
}

// isLower is CPython's `str.islower()`: every cased character is lower case and
// there is at least one of them. The empty string is False, and so is "123" —
// which is why `kathara 42` prints the general help and exits 1 rather than
// reporting an unrecognized command.
func isLower(s string) bool {
	cased := false
	for _, r := range s {
		switch {
		case unicode.IsUpper(r), unicode.IsTitle(r):
			return false
		case unicode.IsLower(r):
			cased = true
		}
	}
	return cased
}

// topLevelUsage is argparse's `usage: ` block for the root parser: the literal
// `description_msg` of `src/kathara.py:20-24`, which embeds the rich command
// table.
func topLevelUsage(width int) string {
	var b strings.Builder
	b.WriteString("usage: kathara [-h] [-v] <command> [<args>]\n")
	b.WriteString("\n")
	b.WriteString("Possible Kathara commands are:\n")
	b.WriteString("\n")
	for _, line := range commandTableLines(width) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// topLevelHelp is `parser.print_help()` on the root parser.
func topLevelHelp(width int) string {
	var b strings.Builder
	b.WriteString(topLevelUsage(width))
	b.WriteString("\n")
	b.WriteString("A network emulation tool.\n")
	b.WriteString("\n")
	b.WriteString("positional arguments:\n")
	b.WriteString("  command        Command to run.\n")
	b.WriteString("\n")
	b.WriteString("options:\n")
	b.WriteString("  -h, --help     Show an help message and exit.\n")
	b.WriteString("  -v, --version  Print the current Kathara version.")
	return b.String()
}

// commandTableLines is `Kathara.strings.formatted_strings()`: a two-column
// `rich.Table` with no box, no header and no edge, laid out to the console
// width.
func commandTableLines(width int) []string {
	rows := make([][]string, 0, len(commandDescriptions))
	for _, c := range commandDescriptions {
		rows = append(rows, []string{c.Name, c.Short})
	}
	return renderPlainTable(rows, width)
}
