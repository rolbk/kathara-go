package main

import (
	"os"
	"strings"
	"testing"
)

// TestTopLevelHelpMatchesTheOracleByte forByte compares `kathara -h` against a
// recording of the real 3.8.3 parser.
func TestTopLevelHelpMatchesTheOracle(t *testing.T) {
	want, err := os.ReadFile("testdata/toplevel_help.txt")
	if err != nil {
		t.Fatal(err)
	}
	// `parser.print_help()` writes the block plus a trailing newline; this implementation
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
