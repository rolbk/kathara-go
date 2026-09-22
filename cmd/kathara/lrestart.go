package main

import (
	"context"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
)

func newLrestartCmd(a *app) *commandSpec {
	cmd := newParser("lrestart")
	f := registerLstartFlags(cmd, true)
	registerFormat(cmd, false)

	return &commandSpec{
		Name: "lrestart",
		Cmd:  cmd,
		Greedy: []greedySpec{
			{long: "pass", short: "o", kind: greedyZeroOrMore},
			{long: "exclude", kind: greedyOneOrMore},
		},
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			return runLrestart(ctx, a, f, positional)
		},
	}
}

// runLrestart is `LrestartCommand.run`.
func runLrestart(ctx context.Context, a *app, f *lstartFlags, positional []string) (int, error) {
	// `lclean_argv = ['-d', directory] if directory else []`, then the
	// positional device names, then `--exclude` with its own list. Note what
	// is NOT forwarded: every other flag, including `--noterminals`, which is
	// why lclean cannot be told anything about terminals.
	var lcleanArgv []string
	if f.directory != "" {
		lcleanArgv = append(lcleanArgv, "-d", f.directory)
	}
	lcleanArgv = append(lcleanArgv, positional...)
	if excluded := f.excluded.Values(); len(excluded) > 0 {
		lcleanArgv = append(lcleanArgv, "--exclude")
		lcleanArgv = append(lcleanArgv, excluded...)
	}

	a.suppressEmit = true
	defer func() { a.suppressEmit = false }()

	clean := newLcleanCmd(a)
	if code := runCommand(ctx, a, clean, lcleanArgv); code != 0 {
		return code, nil
	}
	cleanResult := a.lastLcleanResult

	// `LstartCommand().run(current_path, argv)` — the RAW argv, re-parsed by
	// lstart's parser. This is where `--xterm` dies.
	start := newLstartCmd(a)
	if code := runCommand(ctx, a, start, a.rawArgs); code != 0 {
		return code, nil
	}
	startResult := a.lastLstartResult

	a.suppressEmit = false
	a.console.Emit(cliout.LrestartResult{Clean: cleanResult, Start: startResult})
	return 0, nil
}
