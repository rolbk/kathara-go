package main

import (
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/spf13/pflag"
)

// TestFlagSurfaceMatchesCLISurface walks every row of docs/port/CLI_SURFACE.md
// §1-§12 and asserts that the flag exists with the spelling, shorthand and
// value-or-not the table records.
//
// It is a table of the *contract*, not of the implementation: the rows below
// were transcribed from the document, so a flag that silently loses its
// shorthand — or gains a value where argparse had `store_const` — fails here
// rather than in a user's script.
func TestFlagSurfaceMatchesCLISurface(t *testing.T) {
	type row struct {
		long      string
		short     string
		takesArgs bool
	}
	tests := []struct {
		command string
		flags   []row
		// count is CLI_SURFACE.md §17's "Options" column, plus the port's own
		// additions (`--format`, `--lab-hash`, `--lab-name`, `--from-archive`,
		// `--name`), which the per-command comment enumerates.
		extra []string
	}{
		{
			command: "lstart",
			flags: []row{
				{"help", "h", false},
				{"noterminals", "", false},
				{"terminals", "", false},
				{"privileged", "", false},
				{"directory", "d", true},
				{"force-lab", "F", false},
				{"list", "l", false},
				{"pass", "o", true},
				{"terminal-emu", "", true},
				{"print", "", false},
				{"dry-mode", "", false},
				{"no-hosthome", "H", false},
				{"hosthome", "", false},
				{"no-shared", "S", false},
				{"shared", "", false},
				{"exclude", "", true},
			},
			extra: []string{"format", "from-archive", "name"},
		},
		{
			command: "lclean",
			flags: []row{
				{"help", "h", false},
				{"directory", "d", true},
				{"exclude", "", true},
			},
			extra: []string{"format", "lab-hash", "lab-name"},
		},
		{
			command: "lrestart",
			flags: []row{
				{"help", "h", false},
				{"noterminals", "", false},
				{"terminals", "", false},
				{"privileged", "", false},
				{"directory", "d", true},
				{"force-lab", "F", false},
				{"list", "l", false},
				{"pass", "o", true},
				{"xterm", "", true},
				{"no-hosthome", "H", false},
				{"hosthome", "", false},
				{"no-shared", "S", false},
				{"shared", "", false},
				{"exclude", "", true},
			},
			extra: []string{"format"},
		},
		{
			command: "linfo",
			flags: []row{
				{"help", "h", false},
				{"directory", "d", true},
				{"watch", "w", false},
				{"live", "l", false},
				{"conf", "c", false},
				{"name", "n", true},
				{"topology", "t", false},
			},
			extra: []string{"format"},
		},
		{
			command: "lconfig",
			flags: []row{
				{"help", "h", false},
				{"directory", "d", true},
				{"name", "n", true},
				{"add", "", true},
				{"rm", "", true},
			},
			extra: []string{"format", "lab-hash", "lab-name"},
		},
		{
			command: "vstart",
			flags: []row{
				{"help", "h", false},
				{"noterminals", "", false},
				{"terminals", "", false},
				{"num_terms", "", true},
				{"privileged", "", false},
				{"name", "n", true},
				{"eth", "", true},
				{"exec", "e", true},
				{"mem", "", true},
				{"cpus", "", true},
				{"image", "i", true},
				{"no-hosthome", "H", false},
				{"hosthome", "", false},
				{"terminal-emu", "", true},
				{"print", "", false},
				{"dry-run", "", false},
				{"bridged", "", false},
				{"port", "", true},
				{"sysctl", "", true},
				{"env", "", true},
				{"ulimit", "", true},
				{"volume", "", true},
				{"shell", "", true},
				{"entrypoint", "", true},
			},
			extra: []string{"format"},
		},
		{
			command: "vclean",
			flags:   []row{{"help", "h", false}, {"name", "n", true}},
			extra:   []string{"format"},
		},
		{
			command: "vconfig",
			flags: []row{
				{"help", "h", false},
				{"name", "n", true},
				{"add", "", true},
				{"rm", "", true},
			},
			extra: []string{"format"},
		},
		{
			command: "connect",
			flags: []row{
				{"help", "h", false},
				{"directory", "d", true},
				{"vmachine", "v", false},
				{"shell", "", true},
				{"logs", "l", false},
			},
		},
		{
			command: "exec",
			flags: []row{
				{"help", "h", false},
				{"directory", "d", true},
				{"vmachine", "v", false},
				{"no-stdout", "", false},
				{"no-stderr", "", false},
				{"wait", "", false},
			},
			extra: []string{"format", "lab-hash", "lab-name"},
		},
		{
			command: "list",
			flags: []row{
				{"help", "h", false},
				{"all", "a", false},
				{"watch", "w", false},
				{"live", "l", false},
				{"name", "n", true},
			},
			extra: []string{"format"},
		},
		{
			command: "wipe",
			flags: []row{
				{"help", "h", false},
				{"force", "f", false},
				{"settings", "s", false},
				{"all", "a", false},
			},
			extra: []string{"format"},
		},
		{
			command: "check",
			flags:   []row{{"help", "h", false}},
			extra:   []string{"format"},
		},
		{
			// `SettingsCommand` defines no parser at all (CLI_SURFACE.md M-5),
			// so the only flag is the one the dispatcher needs.
			command: "settings",
			flags:   []row{{"help", "h", false}},
		},
	}

	a := newTestApp(t)
	table := commandTable(a.app)

	for _, tc := range tests {
		t.Run(tc.command, func(t *testing.T) {
			spec, ok := table[tc.command]
			if !ok {
				t.Fatalf("command %q is not registered", tc.command)
			}

			want := map[string]bool{}
			for _, r := range tc.flags {
				want[r.long] = true
				flag := spec.Cmd.Flags().Lookup(r.long)
				if flag == nil {
					t.Errorf("--%s missing", r.long)
					continue
				}
				if flag.Shorthand != r.short {
					t.Errorf("--%s shorthand = %q, want %q", r.long, flag.Shorthand, r.short)
				}
				// "Takes a value" is the flag's *type*, not its NoOptDefVal:
				// an argparse `nargs='*'` option takes values AND may appear
				// bare, which is exactly what a non-empty NoOptDefVal on a
				// list-valued flag means (`bindList`).
				if takes := flag.Value.Type() != "bool"; takes != r.takesArgs {
					t.Errorf("--%s takes a value = %t, want %t", r.long, takes, r.takesArgs)
				}
			}
			for _, name := range tc.extra {
				want[name] = true
				if spec.Cmd.Flags().Lookup(name) == nil {
					t.Errorf("--%s missing (port addition)", name)
				}
			}

			spec.Cmd.Flags().VisitAll(func(f *pflag.Flag) {
				if !want[f.Name] {
					t.Errorf("--%s is declared but is not in CLI_SURFACE.md", f.Name)
				}
			})
		})
	}
}

// TestConnectRejectsFormat is JSON_CLI_CONTRACT.md §1.1: `connect` is
// human-only and declares no `--format`, so any use of it is an unknown-flag
// usage error with exit 2 and an empty stdout.
func TestConnectRejectsFormat(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["connect"]
	code := runCommand(t.Context(), a.app, spec, []string{"--format", "json", "pc1"})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if out := a.stdoutString(); out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(a.stderrString(), "unknown flag: --format") {
		t.Errorf("stderr = %q, want an unknown-flag message", a.stderrString())
	}
}

// TestSettingsNeverParsesArgv is CLI_SURFACE.md §12 and M-5: `SettingsCommand`
// builds no parser, so `-h` is ignored, `--format json` is ignored, and there
// is no argparse exit-2 path at all — every invocation opens the screen. Here
// there is no terminal, so the screen answers InvocationError and exits 1;
// what matters is that argv never produced a usage error and never printed
// help.
func TestSettingsNeverParsesArgv(t *testing.T) {
	for _, argv := range [][]string{{"-h"}, {"--help"}, {"--format", "json"}, {"--bogus"}, {"extra"}} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["settings"]
			code := runCommand(t.Context(), a.app, spec, argv)
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (the no-terminal answer)", code)
			}
			if out := a.stdoutString(); strings.Contains(out, "usage:") {
				t.Errorf("stdout = %q, want no help text", out)
			}
			if errOut := a.stderrString(); errOut != "" {
				t.Errorf("stderr = %q, want empty", errOut)
			}
		})
	}
}

// TestFormatRejectsUnsupportedValue covers the two halves of §1.1's
// "Passing `--format json`/`jsonl` to a command that does not support that
// value is a usage error".
func TestFormatRejectsUnsupportedValue(t *testing.T) {
	tests := []struct {
		command string
		value   string
		wantErr bool
	}{
		{"lstart", "human", false},
		{"lstart", "json", false},
		{"lstart", "jsonl", true},
		{"exec", "jsonl", false},
		{"list", "xml", true},
	}
	for _, tc := range tests {
		t.Run(tc.command+"/"+tc.value, func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)[tc.command]
			if err := spec.Cmd.ParseFlags([]string{"--format", tc.value}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := a.applyFormat(spec)
			if tc.wantErr != (err != nil) {
				t.Fatalf("applyFormat error = %v, wantErr %t", err, tc.wantErr)
			}
			if err == nil && a.console.Format != cliout.Format(tc.value) {
				t.Errorf("console format = %q, want %q", a.console.Format, tc.value)
			}
		})
	}
}

// TestMutuallyExclusiveGroups covers every argparse MEG of CLI_SURFACE.md, in
// both directions: the pair together is exit 2, each alone is accepted.
func TestMutuallyExclusiveGroups(t *testing.T) {
	tests := []struct {
		command string
		args    []string
		wantErr bool
	}{
		{"lstart", []string{"--noterminals", "--terminals"}, true},
		{"lstart", []string{"--noterminals"}, false},
		{"lstart", []string{"--hosthome", "--no-hosthome"}, true},
		{"lstart", []string{"--shared", "--no-shared"}, true},
		{"lstart", []string{"-H", "-S"}, false},
		{"vstart", []string{"-n", "pc1", "--noterminals", "--num_terms", "2"}, true},
		{"vstart", []string{"-n", "pc1", "--terminals", "--num_terms", "2"}, true},
		{"vstart", []string{"-n", "pc1", "--num_terms", "2"}, false},
		{"linfo", []string{"--watch", "--conf"}, true},
		{"linfo", []string{"--name", "pc1", "--topology"}, true},
		{"linfo", []string{"--name", "pc1"}, false},
		{"wipe", []string{"--settings", "--all"}, true},
		{"connect", []string{"-v", "-d", "/tmp", "pc1"}, true},
		{"exec", []string{"-v", "-d", "/tmp", "pc1", "ls"}, true},
		// `list` has NO groups: -w and -n combine freely (CLI_SURFACE.md §11).
		{"list", []string{"-w", "-n", "pc1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.command+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)[tc.command]
			head := expandGreedy(tc.args, spec.Greedy)
			if err := spec.Cmd.ParseFlags(head); err != nil {
				if !tc.wantErr {
					t.Fatalf("ParseFlags: %v", err)
				}
				return
			}
			err := spec.Cmd.validate(head)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validate = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

// TestRequiredFlags covers the `required=True` rows: `-n/--name` on vstart,
// vclean, vconfig and lconfig, and the required `--add | --rm` group.
func TestRequiredFlags(t *testing.T) {
	tests := []struct {
		command string
		args    []string
		wantErr bool
	}{
		{"vstart", nil, true},
		{"vstart", []string{"-n", "pc1"}, false},
		{"vclean", nil, true},
		{"vclean", []string{"-n", "pc1"}, false},
		{"vconfig", []string{"-n", "pc1"}, true},
		{"vconfig", []string{"-n", "pc1", "--add", "A"}, false},
		{"vconfig", []string{"--add", "A"}, true},
		{"lconfig", []string{"-n", "pc1"}, true},
		{"lconfig", []string{"-n", "pc1", "--rm", "A"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.command+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)[tc.command]
			head := expandGreedy(tc.args, spec.Greedy)
			if err := spec.Cmd.ParseFlags(head); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			err := spec.Cmd.validate(head)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validation = %v, wantErr %t", err, tc.wantErr)
			}
		})
	}
}

// TestArgparseTypeValidators is CLI_SURFACE.md §0.6, including the two shapes
// of `interface_cd_mac` that look like oversights.
func TestArgparseTypeValidators(t *testing.T) {
	t.Run("alphanumeric", func(t *testing.T) {
		for _, ok := range []string{"A", "cd_1", "_", "ÜBER", "٣"} {
			if err := alphanumeric(ok); err != nil {
				t.Errorf("alphanumeric(%q) = %v, want nil", ok, err)
			}
		}
		for _, bad := range []string{"", "a-b", "a b", "a.b"} {
			if err := alphanumeric(bad); err == nil {
				t.Errorf("alphanumeric(%q) = nil, want an error", bad)
			}
		}
	})

	t.Run("interface_cd_mac", func(t *testing.T) {
		tests := []struct {
			in   string
			want ethSpec
			err  string
		}{
			{in: "0:A", want: ethSpec{Number: "0", CD: "A"}},
			{in: "1:B/00:11:22:33:44:55", want: ethSpec{Number: "1", CD: "B", MAC: "00:11:22:33:44:55"}},
			// A third `/` part takes neither branch, so the MAC is dropped and
			// nothing errors.
			{in: "0:A/aa/bb", want: ethSpec{Number: "0", CD: "A"}},
			// A trailing empty MAC IS an error, because the `else: raise` fires
			// only for the two-part case.
			{in: "0:A/", err: "invalid interface definition: 0:A/"},
			{in: "A", err: "invalid interface definition: A"},
			{in: "0:A:B", err: "invalid interface definition: 0:A:B"},
			{in: "0:a-b", err: "invalid interface definition, collision domain `a-b` contains non-alphanumeric characters"},
			// A non-numeric interface number is NOT rejected here: it becomes a
			// SyntaxError later, with exit 1 instead of exit 2.
			{in: "x:A", want: ethSpec{Number: "x", CD: "A"}},
		}
		for _, tc := range tests {
			got, err := interfaceCDMAC(tc.in)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Errorf("interfaceCDMAC(%q) error = %v, want %q", tc.in, err, tc.err)
				}
				continue
			}
			if err != nil {
				t.Errorf("interfaceCDMAC(%q) = %v", tc.in, err)
				continue
			}
			if got != tc.want {
				t.Errorf("interfaceCDMAC(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
		}
	})

	t.Run("volume", func(t *testing.T) {
		for _, ok := range []string{"/h|/g", "/h|/g|ro", "|/h|/g|"} {
			if err := volumeSpec(ok); err != nil {
				t.Errorf("volume(%q) = %v, want nil", ok, err)
			}
		}
		for _, bad := range []string{"/h", "/h|/g|ro|x", ""} {
			if err := volumeSpec(bad); err == nil {
				t.Errorf("volume(%q) = nil, want an error", bad)
			}
		}
	})

	t.Run("cd_mac", func(t *testing.T) {
		cd, mac, err := cdMAC("A")
		if err != nil || cd != "A" || mac != "" {
			t.Errorf("cdMAC(A) = %q,%q,%v", cd, mac, err)
		}
		cd, mac, err = cdMAC("A/00:11:22:33:44:55")
		if err != nil || cd != "A" || mac != "00:11:22:33:44:55" {
			t.Errorf("cdMAC(A/mac) = %q,%q,%v", cd, mac, err)
		}
		if _, _, err := cdMAC("A/b/c"); err == nil {
			t.Error("cdMAC(A/b/c) = nil, want a SyntaxError")
		}
	})
}

// TestRequiredFlagsAreEnforcedByTheDispatcher is the production path, not the
// validator in isolation: `MarkFlagRequired` is answered by cobra's
// `ValidateRequiredFlags` and NOT by `ValidateFlagGroups`, so a dispatcher that
// calls only the latter runs `kathara vclean` with an empty device name and
// exits 0 where argparse exits 2.
func TestRequiredFlagsAreEnforcedByTheDispatcher(t *testing.T) {
	tests := []struct {
		command string
		args    []string
		want    string
	}{
		{"vclean", nil, "the following arguments are required: -n/--name"},
		{"vstart", nil, "the following arguments are required: -n/--name"},
		{"lconfig", []string{"--add", "A"}, "the following arguments are required: -n/--name"},
		{"vconfig", []string{"--rm", "A"}, "the following arguments are required: -n/--name"},
		// The required group is checked AFTER the required options, which is
		// argparse's order (oracle: `lconfig` alone names `-n`, not the group).
		{"lconfig", []string{"-n", "pc1"}, "one of the arguments --add --rm is required"},
		// A conflict is raised during the parse, so it precedes both.
		{"lconfig", []string{"--add", "A", "--rm", "B"}, "argument --rm: not allowed with argument --add"},
		{"exec", nil, "the following arguments are required: DEVICE_NAME, COMMAND"},
		{"exec", []string{"pc1"}, "the following arguments are required: COMMAND"},
		{"connect", nil, "the following arguments are required: DEVICE_NAME"},
	}
	for _, tc := range tests {
		t.Run(tc.command+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)[tc.command]
			if code := runCommand(t.Context(), a.app, spec, tc.args); code != 2 {
				t.Fatalf("exit = %d, want 2 (stdout %q)", code, a.stdoutString())
			}
			if out := a.stdoutString(); out != "" {
				t.Errorf("stdout = %q, want empty", out)
			}
			want := "kathara " + tc.command + ": error: " + tc.want
			if !strings.Contains(a.stderrString(), want) {
				t.Errorf("stderr = %q, want it to contain %q", a.stderrString(), want)
			}
		})
	}
}

// TestStrayPositionalsAreUnrecognized is argparse's `parse_args` leftover
// check. The commands below declare no positional at all, so a stray word is
// `unrecognized arguments`, exit 2 — not a silently ignored token that lets
// `kathara wipe -f extra` wipe the host.
func TestStrayPositionalsAreUnrecognized(t *testing.T) {
	tests := []struct {
		command string
		args    []string
		want    string
	}{
		{"wipe", []string{"-f", "extra"}, "unrecognized arguments: extra"},
		{"wipe", []string{"-f", "a", "b"}, "unrecognized arguments: a b"},
		{"vclean", []string{"-n", "pc1", "pc2"}, "unrecognized arguments: pc2"},
		{"list", []string{"extra"}, "unrecognized arguments: extra"},
		{"check", []string{"extra"}, "unrecognized arguments: extra"},
		{"linfo", []string{"extra"}, "unrecognized arguments: extra"},
		// A trailing word is swallowed by the `nargs='+'` option, as argparse
		// swallows it (oracle: `to_add == [('A', None), ('extra', None)]`); a
		// LEADING one is the unrecognized argument.
		{"lconfig", []string{"extra", "-n", "pc1", "--add", "A"}, "unrecognized arguments: extra"},
		{"vconfig", []string{"extra", "-n", "pc1", "--add", "A"}, "unrecognized arguments: extra"},
		{"connect", []string{"pc1", "extra"}, "unrecognized arguments: extra"},
	}
	for _, tc := range tests {
		t.Run(tc.command+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)[tc.command]
			if code := runCommand(t.Context(), a.app, spec, tc.args); code != 2 {
				t.Fatalf("exit = %d, want 2 (stdout %q)", code, a.stdoutString())
			}
			want := "kathara " + tc.command + ": error: " + tc.want
			if !strings.Contains(a.stderrString(), want) {
				t.Errorf("stderr = %q, want it to contain %q", a.stderrString(), want)
			}
		})
	}
}

// TestExclusiveConflictNamesTheSecondOption is argparse's `take_action`: the
// option that *arrived* second is the subject, the earliest-declared sibling
// already seen is the object. Oracle-verified on both orderings.
func TestExclusiveConflictNamesTheSecondOption(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"-s", "-a"}, "argument -a/--all: not allowed with argument -s/--settings"},
		{[]string{"-a", "-s"}, "argument -s/--settings: not allowed with argument -a/--all"},
	}
	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["wipe"]
			if code := runCommand(t.Context(), a.app, spec, tc.args); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(a.stderrString(), tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", a.stderrString(), tc.want)
			}
		})
	}
}

// TestLinfoLiveSharesTheWatchGroup is the alias trap: argparse declares
// `-w, -l, --watch, --live` as ONE action inside the MEG with `-c/--conf`, so
// `linfo -l -c` is a usage error. pflag needs two flags to spell it, and a
// group that named only `watch` would let `--live` through.
func TestLinfoLiveSharesTheWatchGroup(t *testing.T) {
	for _, args := range [][]string{{"-l", "-c"}, {"--live", "--conf"}, {"-w", "-c"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["linfo"]
			if code := runCommand(t.Context(), a.app, spec, args); code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if !strings.Contains(a.stderrString(), "not allowed with argument") {
				t.Errorf("stderr = %q", a.stderrString())
			}
		})
	}
	// The two spellings of the same action do NOT conflict with each other.
	a := newTestApp(t)
	spec := commandTable(a.app)["linfo"]
	if code := runCommand(t.Context(), a.app, spec, []string{"-w", "-l"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (the FeatureNotAvailable stub)", code)
	}
}

// TestLinfoErrorsInEveryMode is JSON_CLI_CONTRACT.md §1.1's `linfo` row read
// with §5.6: the stub errors in every format, and the `linfo` feature token is
// registered for the JSON *error envelope* — which only exists in the machine
// formats, so the command has to accept `--format` for that registration to be
// reachable at all.
func TestLinfoErrorsInEveryMode(t *testing.T) {
	t.Run("human", func(t *testing.T) {
		a := newTestApp(t)
		spec := commandTable(a.app)["linfo"]
		if code := runCommand(t.Context(), a.app, spec, nil); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		if !strings.Contains(a.stdoutString(), "not supported in this release") {
			t.Errorf("stdout = %q", a.stdoutString())
		}
	})
	t.Run("json", func(t *testing.T) {
		a := newTestApp(t)
		spec := commandTable(a.app)["linfo"]
		if code := runCommand(t.Context(), a.app, spec, []string{"--format", "json"}); code != 1 {
			t.Fatalf("exit = %d, want 1", code)
		}
		want := `{"error":{"code":"FeatureNotAvailable",`
		if !strings.HasPrefix(a.stdoutString(), want) {
			t.Errorf("stdout = %q, want it to start with %q", a.stdoutString(), want)
		}
		if !strings.Contains(a.stdoutString(), `"feature":"linfo"`) {
			t.Errorf("stdout = %q, want the feature token", a.stdoutString())
		}
	})
	t.Run("jsonl is not a linfo value", func(t *testing.T) {
		a := newTestApp(t)
		spec := commandTable(a.app)["linfo"]
		if code := runCommand(t.Context(), a.app, spec, []string{"--format", "jsonl"}); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})
}
