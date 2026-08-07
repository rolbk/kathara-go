package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestGreedyExpansionMatchesArgparse pins [expandGreedy] against the argparse
// oracle. Every expectation below was produced by running the real 3.8.3
// parsers:
//
//	$ python -c "from Kathara.cli.command.LstartCommand import LstartCommand; \
//	             print(vars(LstartCommand().parser.parse_args([...])))"
//
// The results that matter are the two greedy ones: `--exclude a b pc1` swallows
// the positional, and `-o` with nothing after it parses as the EMPTY LIST rather
// than as absent.
func TestGreedyExpansionMatchesArgparse(t *testing.T) {
	specs := []greedySpec{
		{long: "pass", short: "o", kind: greedyZeroOrMore},
		{long: "exclude", kind: greedyOneOrMore},
	}

	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "one option consumes the whole run",
			in:   []string{"--exclude", "a", "b", "c"},
			want: []string{"--exclude=a", "--exclude=b", "--exclude=c"},
		},
		{
			name: "a leading positional survives",
			in:   []string{"pc1", "--exclude", "a", "b"},
			want: []string{"pc1", "--exclude=a", "--exclude=b"},
		},
		{
			name: "a trailing positional is swallowed",
			in:   []string{"--exclude", "a", "b", "pc1"},
			want: []string{"--exclude=a", "--exclude=b", "--exclude=pc1"},
		},
		{
			name: "the run stops at the next option",
			in:   []string{"--exclude", "a", "-d", "/tmp"},
			want: []string{"--exclude=a", "-d", "/tmp"},
		},
		{
			name: "a bare nargs-star option becomes the empty list",
			in:   []string{"-o"},
			want: []string{"--pass=" + emptyListSentinel},
		},
		{
			name: "a shorthand consumes its run too",
			in:   []string{"-o", "k=v", "k2=v2", "pc1"},
			want: []string{"--pass=k=v", "--pass=k2=v2", "--pass=pc1"},
		},
		{
			name: "an attached value is already one value",
			in:   []string{"--exclude=a", "b"},
			want: []string{"--exclude=a", "b"},
		},
		{
			name: "everything after a bare -- is left alone",
			in:   []string{"--exclude", "a", "--", "--exclude", "b"},
			want: []string{"--exclude=a", "--", "--exclude", "b"},
		},
		{
			name: "a negative number is a value, not an option",
			in:   []string{"--exclude", "-1", "-d", "/tmp"},
			want: []string{"--exclude=-1", "-d", "/tmp"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := expandGreedy(tc.in, specs); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("expandGreedy(%q) =\n %q\nwant\n %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestGreedyExpansionRoundTripsThroughPflag checks the other half: after the
// rewrite, pflag really does produce argparse's list, and the positionals
// really are the ones argparse left over.
func TestGreedyExpansionRoundTripsThroughPflag(t *testing.T) {
	tests := []struct {
		args        []string
		wantExclude []string
		wantPass    []string
		wantPassSet bool
		wantPos     []string
	}{
		{
			args:        []string{"--exclude", "a", "b", "c"},
			wantExclude: []string{"a", "b", "c"},
		},
		{
			args:        []string{"pc1", "--exclude", "a", "b"},
			wantExclude: []string{"a", "b"},
			wantPos:     []string{"pc1"},
		},
		{
			args:        []string{"--exclude", "a", "b", "pc1"},
			wantExclude: []string{"a", "b", "pc1"},
		},
		{
			args:        []string{"-o"},
			wantPassSet: true,
		},
		{
			args:        []string{"-o", "k=v", "k2=v2", "pc1"},
			wantPass:    []string{"k=v", "k2=v2", "pc1"},
			wantPassSet: true,
		},
		{
			args:    []string{"pc1", "pc2"},
			wantPos: []string{"pc1", "pc2"},
		},
	}

	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["lstart"]
			if err := spec.Cmd.ParseFlags(expandGreedy(tc.args, spec.Greedy)); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			exclude := spec.Cmd.Flags().Lookup("exclude").Value.(*stringList)
			pass := spec.Cmd.Flags().Lookup("pass").Value.(*stringList)

			if !equalStrings(exclude.Values(), tc.wantExclude) {
				t.Errorf("--exclude = %q, want %q", exclude.Values(), tc.wantExclude)
			}
			if !equalStrings(pass.Values(), tc.wantPass) {
				t.Errorf("-o = %q, want %q", pass.Values(), tc.wantPass)
			}
			if pass.Present() != tc.wantPassSet {
				t.Errorf("-o present = %t, want %t", pass.Present(), tc.wantPassSet)
			}
			if !equalStrings(spec.Cmd.Flags().Args(), tc.wantPos) {
				t.Errorf("positionals = %q, want %q", spec.Cmd.Flags().Args(), tc.wantPos)
			}
		})
	}
}

// TestRemainderMatchesArgparse pins [splitRemainder] against the vstart oracle,
// including the `--` that argparse keeps and `VstartCommand.run` then strips by
// hand.
func TestRemainderMatchesArgparse(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["vstart"]
	taking := spec.valueTaking()
	greedy := spec.Greedy

	tests := []struct {
		name          string
		in            []string
		wantHead      []string
		wantRemainder []string
	}{
		{
			name:          "bare -- is kept in the remainder",
			in:            []string{"-n", "pc1", "--", "echo", "hi"},
			wantHead:      []string{"-n", "pc1"},
			wantRemainder: []string{"--", "echo", "hi"},
		},
		{
			name:          "a plain positional starts the remainder",
			in:            []string{"-n", "pc1", "echo", "hi"},
			wantHead:      []string{"-n", "pc1"},
			wantRemainder: []string{"echo", "hi"},
		},
		{
			name:          "options after the cut are NOT parsed",
			in:            []string{"-n", "pc1", "ls", "--image", "x"},
			wantHead:      []string{"-n", "pc1"},
			wantRemainder: []string{"ls", "--image", "x"},
		},
		{
			name:          "options before the cut are",
			in:            []string{"-n", "pc1", "--image", "x", "--", "ls", "-la"},
			wantHead:      []string{"-n", "pc1", "--image", "x"},
			wantRemainder: []string{"--", "ls", "-la"},
		},
		{
			name:     "no positional means no remainder",
			in:       []string{"-n", "pc1", "--bridged"},
			wantHead: []string{"-n", "pc1", "--bridged"},
		},
		{
			name:     "an attached value does not eat the next token",
			in:       []string{"--name=pc1", "--bridged"},
			wantHead: []string{"--name=pc1", "--bridged"},
		},
		{
			// Oracle: `--eth 0:A 1:B --image x -- sleep 1` gives two interfaces
			// and `args == ['--', 'sleep', '1']`, so a greedy option's whole
			// run stays in the head.
			name:          "a greedy option keeps its whole run out of the remainder",
			in:            []string{"-n", "pc1", "--eth", "0:A", "1:B", "--image", "x", "--", "sleep", "1"},
			wantHead:      []string{"-n", "pc1", "--eth", "0:A", "1:B", "--image", "x"},
			wantRemainder: []string{"--", "sleep", "1"},
		},
		{
			// Oracle: `--sysctl a=1 b=2 ls` puts `ls` in the sysctl list.
			name:     "a greedy option swallows a following positional",
			in:       []string{"-n", "pc1", "--sysctl", "a=1", "b=2", "ls"},
			wantHead: []string{"-n", "pc1", "--sysctl", "a=1", "b=2", "ls"},
		},
		{
			// Oracle: `ls --sysctl x=1` gives `args == ['ls','--sysctl','x=1']`.
			name:          "a positional first makes the rest verbatim remainder",
			in:            []string{"-n", "pc1", "ls", "--sysctl", "x=1"},
			wantHead:      []string{"-n", "pc1"},
			wantRemainder: []string{"ls", "--sysctl", "x=1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			head, remainder := splitRemainder(tc.in, taking, greedy)
			if !equalStrings(head, tc.wantHead) {
				t.Errorf("head = %q, want %q", head, tc.wantHead)
			}
			if !equalStrings(remainder, tc.wantRemainder) {
				t.Errorf("remainder = %q, want %q", remainder, tc.wantRemainder)
			}
		})
	}
}

// TestIsOptionToken is argparse's `_parse_optional`, reduced to the question
// the greedy expansion asks.
func TestIsOptionToken(t *testing.T) {
	for in, want := range map[string]bool{
		"":       false,
		"-":      false,
		"--":     true,
		"-d":     true,
		"--long": true,
		"pc1":    false,
		"-1":     false,
		"-1.5":   false,
		"-1a":    true,
		"- x":    false,
	} {
		if got := isOptionToken(in); got != want {
			t.Errorf("isOptionToken(%q) = %t, want %t", in, got, want)
		}
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestRemainderCutHandlesShorthandClusters is the interaction between
// `nargs=argparse.REMAINDER` and a clustered shorthand. argparse reads
// `vstart -Hn pc1 ls` as `-H` plus `-n pc1`, leaving `['ls']`; a cut that skips
// whole clusters starts the remainder at `pc1` and then hands pflag a `-n` with
// nothing after it.
func TestRemainderCutHandlesShorthandClusters(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["vstart"]
	taking := spec.valueTaking()

	tests := []struct {
		args     []string
		wantHead []string
		wantRest []string
	}{
		{[]string{"-Hn", "pc1", "ls"}, []string{"-Hn", "pc1"}, []string{"ls"}},
		{[]string{"-n", "pc1", "ls"}, []string{"-n", "pc1"}, []string{"ls"}},
		{[]string{"-npc1", "ls"}, []string{"-npc1"}, []string{"ls"}},
		{[]string{"-H", "-n", "pc1"}, []string{"-H", "-n", "pc1"}, nil},
		// A cluster that ends in the greedy `-e` swallows its whole run.
		{[]string{"-He", "a", "b", "--", "ls"}, []string{"-He", "a", "b"}, []string{"--", "ls"}},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			head, rest := splitRemainder(tc.args, taking, spec.Greedy)
			if !slices.Equal(head, tc.wantHead) {
				t.Errorf("head = %q, want %q", head, tc.wantHead)
			}
			if !slices.Equal(rest, tc.wantRest) {
				t.Errorf("remainder = %q, want %q", rest, tc.wantRest)
			}
		})
	}

	// End to end: the cluster parses and the remainder reaches the command.
	b := newTestApp(t)
	fake := &fakeManager{}
	withManager(b, fake)
	vstart := commandTable(b.app)["vstart"]
	if code := runCommand(t.Context(), b.app, vstart, []string{"-Hn", "pc1", "ls"}); code != 0 {
		t.Fatalf("exit = %d\n%s", code, b.stderrString())
	}
	machine, err := fake.deployedLabs[0].GetMachine("pc1")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := machine.Meta.Args.AsStrings()
	if !ok || !slices.Equal(got, []string{"ls"}) {
		t.Errorf("args meta = %v, want [ls]", machine.Meta.Args)
	}
}
