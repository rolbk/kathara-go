// This file is `cli/command/ListCommand.py` (CLI_SURFACE.md §11) and
// `cli/command/LinfoCommand.py` (§4), the latter as the deferred-feature stub
// PORT_SPEC §0.3 and ERROR_CODES.md §5 require.

package main

import (
	"context"
	"errors"
	"io"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
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

	// Note: NO mutually-exclusive groups here. Unlike `linfo`, `-w` and `-n`
	// combine freely (CLI_SURFACE.md §11).
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
		// Streaming inventory is reserved for the post-1.0 stats envelope
		// (JSON_CLI_CONTRACT.md §1.1, A13).
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
		// `create_lab_table` answers `StopIteration` with `None` and
		// `ListCommand.run` still returns 0 (CLI_SURFACE.md §13). The human
		// table is skipped; json still owes its one object, which is the empty
		// inventory.
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
//
// That guard fires on `StopIteration` and on nothing else — an empty snapshot
// still renders the "No Devices Found" block — so the loop's only clean exit is
// the end of the stream, io.EOF here, and it exits 0.
//
// The alternate-screen `rich.live.Live` is not reproduced. A live screen that
// hides the scrollback is the class of UI PORT_SPEC §3.3 is rebuilding, the
// mode has no golden, and redrawing in place keeps the output usable when
// stdout is a pipe. DIVERGENCES.md records it.
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

// newLinfoCmd is the `linfo` stub.
//
// The command stays in the table and in the help for parity — the whitelist of
// `src/kathara.py:97` names it, so removing it would change the Ctrl-C
// behaviour of a command that no longer exists — and its full flag surface is
// declared so that a script's `linfo -c -n pc1` still fails on the *feature*
// and not on an unknown flag. It answers `FeatureNotAvailable`, exit 1
// (ERROR_CODES.md §5).
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
	// `--format` is declared even though every invocation fails: JSON_CLI_CONTRACT.md
	// §1.1 pins linfo as "errors in every mode", and §5.6 registers `linfo` as a
	// `feature` token of the JSON error envelope — which only exists in the machine
	// formats. Without the flag that registration could never be reached.
	registerFormat(cmd, false)

	return &commandSpec{
		Name: "linfo",
		Cmd:  cmd,
		Run: func(context.Context, *app, []string, []string) (int, error) {
			return 1, kerrors.NewFeatureNotAvailable(kerrors.FeatureLinfo)
		},
	}
}
