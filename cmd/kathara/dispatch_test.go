package main

import (
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

func TestDispatchSequence(t *testing.T) {
	tests := []struct {
		name       string
		argv       []string
		wantCode   int
		wantStdout []string
		wantStderr []string
		wantNot    []string
	}{
		{
			name:       "no command prints the help and exits 1",
			argv:       []string{"kathara"},
			wantCode:   1,
			wantStdout: []string{"usage: kathara [-h] [-v] <command> [<args>]", "A network emulation tool."},
		},
		{
			name:       "--version prints the version and exits 0",
			argv:       []string{"kathara", "--version"},
			wantCode:   0,
			wantStdout: []string{"Current version: " + version},
		},
		{
			name:       "-v is the same",
			argv:       []string{"kathara", "-v"},
			wantCode:   0,
			wantStdout: []string{"Current version: " + version},
		},
		{
			name:       "-h prints the help and exits 0",
			argv:       []string{"kathara", "-h"},
			wantCode:   0,
			wantStdout: []string{"Possible Kathara commands are:", " lstart    Start a Kathara network scenario"},
		},
		{
			name:       "an uppercase command is the help, not an unrecognized command",
			argv:       []string{"kathara", "LSTART"},
			wantCode:   1,
			wantStdout: []string{"usage: kathara [-h] [-v] <command> [<args>]"},
			wantNot:    []string{"Unrecognized command"},
		},
		{
			name:       "a command with no cased characters is the help too",
			argv:       []string{"kathara", "42"},
			wantCode:   1,
			wantStdout: []string{"usage: kathara"},
			wantNot:    []string{"Unrecognized command"},
		},
		{
			name:       "an unknown lowercase command is an ERROR plus the help",
			argv:       []string{"kathara", "bogus"},
			wantCode:   1,
			wantStdout: []string{"ERROR    Unrecognized command `bogus`.", "usage: kathara"},
		},
		{
			name:       "an unknown top-level option is a usage error on stderr",
			argv:       []string{"kathara", "-x"},
			wantCode:   2,
			wantStderr: []string{"unrecognized arguments: -x"},
		},
		{
			name:     "only argv[1] is parsed at the top level",
			argv:     []string{"kathara", "lstart", "-v"},
			wantCode: 2,
			// `-v` reaches lstart's parser, which has no such flag; the global
			// version flag is unreachable from here.
			wantStderr: []string{"unknown shorthand flag: 'v'"},
			wantNot:    []string{"Current version"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp(t)
			a.console.Level = cliout.LevelDebug
			code := dispatch(t.Context(), a.app, tc.argv)
			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, a.stdoutString(), a.stderrString())
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(a.stdoutString(), want) {
					t.Errorf("stdout does not contain %q:\n%s", want, a.stdoutString())
				}
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(a.stderrString(), want) {
					t.Errorf("stderr does not contain %q:\n%s", want, a.stderrString())
				}
			}
			for _, unwanted := range tc.wantNot {
				if strings.Contains(a.stdoutString()+a.stderrString(), unwanted) {
					t.Errorf("output contains %q but should not", unwanted)
				}
			}
		})
	}
}

// TestSettingsCheckSkipIsASubstringTest is `src/kathara.py:71`'s
// `if "settings" not in args.command`. It is a substring test, so a command
// merely *containing* "settings" skips the check — which is observable through
// an unknown command, since the check runs before the lookup.
func TestSettingsCheckSkipIsASubstringTest(t *testing.T) {
	tests := []struct {
		command   string
		wantCheck bool
	}{
		{"lstart", true},
		{"list", true},
		{"check", true},
		{"settings", false},
		// A command name that merely contains the substring skips it too.
		{"mysettingsfoo", false},
		// `config` is the new non-deploying settings command and is skipped for
		// the same reason `settings` is.
		{"config", false},
	}
	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			a := newTestApp(t)
			checked := false
			a.checkSettings = func() error { checked = true; return nil }
			dispatch(t.Context(), a.app, []string{"kathara", tc.command, "-h"})
			if checked != tc.wantCheck {
				t.Errorf("settings check ran = %t, want %t", checked, tc.wantCheck)
			}
		})
	}
}

// TestSettingsCheckFailureExits1 is step 3's error arm: a `SettingsError` is
// logged as `CRITICAL (SettingsError) …` and the process exits 1 without ever
// looking the command up.
func TestSettingsCheckFailureExits1(t *testing.T) {
	a := newTestApp(t)
	a.checkSettings = func() error { return kerrors.ErrSettingsManagerType }

	code := dispatch(t.Context(), a.app, []string{"kathara", "lstart"})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	want := "CRITICAL (SettingsError) Settings file is not valid: Manager Type not allowed. Fix it or delete it before launching."
	if got := strings.TrimRight(a.stdoutString(), "\n"); got != want {
		t.Errorf("stdout = %q\nwant     %q", got, want)
	}
}

func TestInterruptContract(t *testing.T) {
	const warning = "You interrupted Kathara during a command."

	tests := []struct {
		command     string
		format      cliout.Format
		wantWarning bool
		wantStdout  string
	}{
		{command: "lstart", format: cliout.FormatHuman, wantWarning: true},
		{command: "wipe", format: cliout.FormatHuman, wantWarning: true},
		{command: "exec", format: cliout.FormatHuman},
		{command: "linfo", format: cliout.FormatHuman},
		{command: "list", format: cliout.FormatHuman},
		{command: "settings", format: cliout.FormatHuman},
		{command: "config", format: cliout.FormatHuman},
		{command: "lstart", format: cliout.FormatJSON, wantWarning: true, wantStdout: `{"interrupted":true}` + "\n"},
		{command: "exec", format: cliout.FormatJSONL, wantStdout: `{"type":"interrupted"}` + "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.command+"/"+string(tc.format), func(t *testing.T) {
			a := newTestApp(t)
			a.console.Format = tc.format
			a.commandName = tc.command

			if code := a.finish(1, true); code != 0 {
				t.Errorf("exit = %d, want 0", code)
			}
			// In a machine format the warning goes to stderr, in human mode to
			// stdout (where Python's RichHandler puts it).
			logged := a.stdoutString() + a.stderrString()
			if strings.Contains(logged, warning) != tc.wantWarning {
				t.Errorf("warning emitted = %t, want %t (got %q)",
					strings.Contains(logged, warning), tc.wantWarning, logged)
			}
			if a.stdoutString() != tc.wantStdout && tc.wantStdout != "" {
				t.Errorf("stdout = %q, want %q", a.stdoutString(), tc.wantStdout)
			}
		})
	}
}

func TestInterruptWarningGoesToStderrInMachineFormats(t *testing.T) {
	a := newTestApp(t)
	a.console.Format = cliout.FormatJSON
	a.commandName = "lstart"
	a.finish(1, true)

	if strings.Contains(a.stdoutString(), "You interrupted") {
		t.Errorf("the warning is on stdout, which json mode reserves for protocol: %q", a.stdoutString())
	}
	if !strings.Contains(a.stderrString(), "You interrupted") {
		t.Errorf("the warning is missing from stderr: %q", a.stderrString())
	}
}

func TestUsageErrorsWriteNothingToStdout(t *testing.T) {
	for _, format := range []cliout.Format{cliout.FormatHuman, cliout.FormatJSON} {
		t.Run(string(format), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["lclean"]
			code := runCommand(t.Context(), a.app, spec, []string{"--format", string(format), "--nope"})
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if a.stdoutString() != "" {
				t.Errorf("stdout = %q, want empty", a.stdoutString())
			}
			if !strings.Contains(a.stderrString(), "usage: kathara lclean") {
				t.Errorf("stderr has no usage text: %q", a.stderrString())
			}
		})
	}
}

// TestHelpFlagPrintsToStdoutAndExitsZero is argparse's `action='help'`.
func TestHelpFlagPrintsToStdoutAndExitsZero(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["lstart"]
	if code := runCommand(t.Context(), a.app, spec, []string{"-h"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	out := a.stdoutString()
	for _, want := range []string{
		"usage: kathara lstart",
		"Start a Kathara network scenario",
		"--noterminals",
		// The epilog folds at the console width, exactly as
		// `HelpFormatter._fill_text` folds it.
		"For examples and further information visit:\nhttps://github.com/KatharaFramework/Kathara/wiki",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not contain %q:\n%s", want, out)
		}
	}
}

func TestLinfoStaticInformation(t *testing.T) {
	dir := scenarioDir(t, map[string]string{"lab.conf": "LAB_NAME=example\npc1[0]=A\n"})
	a := newTestApp(t)
	a.console.Level = cliout.LevelDebug
	code := dispatch(t.Context(), a.app, []string{"kathara", "linfo", "-c", "-n", "pc1", "-d", dir})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := a.stdoutString(); !strings.Contains(got, "pc1 Information") || !strings.Contains(got, "Interfaces:") {
		t.Errorf("stdout = %q", got)
	}
}

// TestIsLowerMatchesCPython pins `str.islower()`, which decides between the
// general help (exit 1) and a command lookup.
func TestIsLowerMatchesCPython(t *testing.T) {
	for in, want := range map[string]bool{
		"":         false,
		"lstart":   true,
		"LSTART":   false,
		"Lstart":   false,
		"l1":       true,
		"123":      false,
		"_":        false,
		"l_start":  true,
		"überlist": true,
		"Ǆa":       false, // a titlecase character
	} {
		if got := isLower(in); got != want {
			t.Errorf("isLower(%q) = %t, want %t", in, got, want)
		}
	}
}
