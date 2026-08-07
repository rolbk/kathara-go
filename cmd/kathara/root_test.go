package main

import (
	"os"
	"strings"
	"testing"
)

// TestTopLevelHelpMatchesTheOracleByte forByte compares `kathara -h` against a
// recording of the real 3.8.3 parser.
//
// testdata/toplevel_help.txt was produced by running Python's own
// `parser.format_help()` over the entrypoint's parser, with the harness's
// environment (`COLUMNS=80 TERM=dumb NO_COLOR=1`, NORMALIZATION.md §1). It is
// the only piece of `rich` layout the CLI emits that is NOT a panel, and it
// exercises the boxless two-column `Table`, its `ratio_reduce` widths and the
// fold of `lconfig`'s description at column 68.
func TestTopLevelHelpMatchesTheOracle(t *testing.T) {
	want, err := os.ReadFile("testdata/toplevel_help.txt")
	if err != nil {
		t.Fatal(err)
	}
	// `parser.print_help()` writes the block plus a trailing newline; the port
	// prints the block through `Console.Print`, which adds the same one.
	got := topLevelHelp(80) + "\n"
	if got != string(want) {
		gotLines := strings.Split(got, "\n")
		wantLines := strings.Split(string(want), "\n")
		for i := 0; i < max(len(gotLines), len(wantLines)); i++ {
			g, w := "", ""
			if i < len(gotLines) {
				g = gotLines[i]
			}
			if i < len(wantLines) {
				w = wantLines[i]
			}
			if g != w {
				t.Errorf("line %d:\n got %q\nwant %q", i+1, g, w)
			}
		}
	}
}

// TestCommandTableOrderIsTheStringsDict pins the display order of the help,
// which is `Kathara/strings.py`'s insertion order (ORDERING.tsv).
func TestCommandTableOrderIsTheStringsDict(t *testing.T) {
	want := []string{
		"vstart", "vclean", "vconfig", "lstart", "lclean", "linfo", "lrestart",
		"lconfig", "connect", "exec", "wipe", "list", "settings", "check",
	}
	if len(commandDescriptions) != len(want) {
		t.Fatalf("command count = %d, want %d", len(commandDescriptions), len(want))
	}
	for i, name := range want {
		if commandDescriptions[i].Name != name {
			t.Errorf("position %d = %q, want %q", i, commandDescriptions[i].Name, name)
		}
	}
}

// TestEveryDescribedCommandIsRegistered checks the other direction: the help
// lists exactly the commands the dispatcher can run, plus `config`, the new
// command PORT_SPEC §3.2 adds and which Python's `strings` dict cannot know
// about.
func TestEveryDescribedCommandIsRegistered(t *testing.T) {
	a := newTestApp(t)
	table := commandTable(a.app)

	for _, c := range commandDescriptions {
		if _, ok := table[c.Name]; !ok {
			t.Errorf("%q is in the help but is not dispatchable", c.Name)
		}
	}
	described := map[string]bool{"config": true}
	for _, c := range commandDescriptions {
		described[c.Name] = true
	}
	for name := range table {
		if !described[name] {
			t.Errorf("%q is dispatchable but is not in the help", name)
		}
	}
}
