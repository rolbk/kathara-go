// This file is `cli/ui/utils.open_machine_terminal`'s three per-platform
// closures (`cli/ui/utils.py:137-204`), which PORT_SPEC §3.3 item 3 marks
// **faithful-port territory, not redesign**: "Port the existing adapters for
// users who want separate OS windows."
//
// So the command lines are Python's, character for character, including the
// double space `"%s connect %s -l %s"` leaves where `is_vmachine` is empty. A
// Go `strings.Join` of non-empty parts would have produced a tidier string and
// a different byte sequence, and the tidier string is not what this is for.
//
// The one substitution is the macOS driver: PORT_SPEC §6 maps `appscript` to
// `osascript`, because py-appscript is a Carbon binding with no cgo-free
// equivalent. The AppleScript emitted is the literal translation of the two
// appscript calls — `do_script` and
// `create_window_with_default_profile` + `current_session.write` — so the
// observable effect is the same window with the same command typed into it.
//
// Everything here is a pure function of its arguments; the per-OS files hold
// only the spawn. That is what makes the exec shapes table-testable on a Linux
// CI box with no emulator installed anywhere.

package term

import (
	"errors"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ErrExternalUnsupported is returned by [OpenExternal] on a platform with no
// ported adapter — Python's `exec_by_platform` falls off the end there and
// returns None, i.e. does nothing at all, which is not a behaviour worth
// reproducing for something the user explicitly asked for.
var ErrExternalUnsupported = errors.New("term: external terminal emulators are not supported on this platform")

// Request is everything the adapters need about one device.
type Request struct {
	// Terminal is the `terminal` setting value: a program path on Linux, an
	// application name on macOS, ignored on Windows (Python's
	// `windows_connect` never reads it).
	Terminal string

	// Executable is `utils.get_executable_path(sys.argv[0])`, which is
	// **already quoted** — `internal/util.GetExecutablePath` returns
	// `"…/kathara"` with the quotes, as Python does.
	Executable string

	// Machine is the device name.
	Machine string

	// VMachine is `not machine.lab.has_host_path()`, i.e. the scenario has no
	// directory on disk and the spawned `kathara connect` needs `-v`.
	VMachine bool

	// LabPath is `machine.lab.fs_path()`, or "" when the scenario has no host
	// path. It becomes the spawned process's working directory, and on macOS
	// also the `cd` that prefixes the command.
	LabPath string
}

// ConnectCommand is Python's `connect_command`:
//
//	"%s connect %s -l %s" % (executable_path, is_vmachine, machine.name)
//
// `is_vmachine` is `"-v"` or `""`, and the `%s` for it is not conditional, so
// the non-vmachine form carries two spaces after `connect`. Every consumer
// either hands the string to a shell or `shlex.split`s it, both of which
// collapse the run — but the string that reaches `Popen` has it, and that is
// what "byte-matched exec shapes" means.
func ConnectCommand(r Request) string {
	isVmachine := ""
	if r.VMachine {
		isVmachine = "-v"
	}
	return r.Executable + " connect " + isVmachine + " -l " + r.Machine
}

// Spec is one emulator launch: a program, its arguments and a working
// directory. Args excludes the program itself, matching `exec.Command`.
type Spec struct {
	Path string
	Args []string
	Dir  string
}

// Argv renders the spec as the argv `subprocess.Popen` was given, which is the
// form the tests compare against Python.
func (s Spec) Argv() []string {
	return append([]string{s.Path}, s.Args...)
}

// LinuxSpec is `unix_connect`'s non-TMUX branch (`cli/ui/utils.py:143-158`).
//
//	command = [terminal]
//	if 'gnome-terminal' in terminal:
//	    command.append("--"); command.extend(shlex.split(connect_command))
//	else:
//	    command.append("-e"); command.append(connect_command)
//
// The `gnome-terminal` test is a substring test on the whole setting value, so
// `/usr/local/bin/gnome-terminal-wrapper` takes the `--` branch too. That is
// Python's behaviour and it is reproduced rather than tightened.
func LinuxSpec(terminal, connectCommand, dir string) (Spec, error) {
	spec := Spec{Path: terminal, Dir: dir}
	if strings.Contains(terminal, "gnome-terminal") {
		words, err := shlexSplit(connectCommand)
		if err != nil {
			return Spec{}, err
		}
		spec.Args = append([]string{"--"}, words...)
		return spec, nil
	}
	spec.Args = []string{"-e", connectCommand}
	return spec, nil
}

// WindowsSpec is `windows_connect` (`cli/ui/utils.py:160-169`):
//
//	subprocess.Popen(["powershell.exe", "-Command", "& " + connect_command],
//	                 creationflags=CREATE_NEW_CONSOLE, cwd=lab.fs_path())
//
// The `& ` prefix is PowerShell's call operator, needed because the executable
// path is quoted and a quoted string is otherwise just a string.
//
// PACKAGE_GRAPH.md's term row sketches this as a `cmd /c start` adapter
// (work item W6-13). PowerShell is what the Python actually runs, and §3.3
// item 3 says port the existing adapter, so the Python wins over the sketch.
func WindowsSpec(connectCommand, dir string) Spec {
	return Spec{
		Path: "powershell.exe",
		Args: []string{"-Command", "& " + connectCommand},
		Dir:  dir,
	}
}

// DarwinCommand is `osx_connect`'s `complete_osx_command`
// (`cli/ui/utils.py:171-174`):
//
//	cd_to_lab_path = 'cd "%s" &&' % lab.fs_path() if lab.has_host_path() else ""
//	"%s clear && %s && exit" % (cd_to_lab_path, connect_command)
//
// Note the leading space when there is no lab path: the format string joins
// the empty prefix and `clear` with one, so the command starts with " clear".
// Harmless to a shell, and preserved.
func DarwinCommand(connectCommand, labPath string) string {
	cdPrefix := ""
	if labPath != "" {
		cdPrefix = `cd "` + labPath + `" &&`
	}
	return cdPrefix + " clear && " + connectCommand + " && exit"
}

// DarwinSpec is the osascript replacement for the two appscript drivers.
//
// Python dispatches on the exact application name and silently does nothing
// for any other value (`cli/ui/utils.py:196-201` has no else). ok is false for
// that case, so the caller reproduces the no-op instead of inventing a third
// driver — recorded in DIVERGENCES.md rather than fixed, per §10.
//
// There is no working directory: appscript talks to an already-running
// application over Apple Events, so `osx_connect` never had a `cwd` to pass —
// the `cd` inside [DarwinCommand] is how the lab path reaches the shell.
func DarwinSpec(terminal, osxCommand string) (spec Spec, ok bool) {
	var script string
	switch terminal {
	case "Terminal":
		// appscript: app("Terminal").do_script(cmd)
		script = `tell application "Terminal" to do script "` + escapeAppleScript(osxCommand) + `"`
	case "iTerm":
		// appscript: w = app("iTerm").create_window_with_default_profile()
		//            w.current_session.write(text=cmd)
		script = `tell application "iTerm"
	set w to (create window with default profile)
	tell w's current session to write text "` + escapeAppleScript(osxCommand) + `"
end tell`
	default:
		return Spec{}, false
	}
	return Spec{Path: "/usr/bin/osascript", Args: []string{"-e", script}}, true
}

// escapeAppleScript quotes a value for an AppleScript string literal, where
// only the backslash and the double quote are special. It is the same
// escaping `settings/terminal_darwin.go` applies to the app name it probes.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `"`, `\"`)
}

// shlexSplit is `shlex.split(s)` for the one call site Python has in this file
// (the `gnome-terminal --` branch): POSIX word splitting, comments off.
//
// `backend/docker/shlex.go` has the same function for `exec`'s command
// strings. It is not shared because `term` must not import a backend
// (PACKAGE_GRAPH.md §2), and hoisting it into `internal/util` would mean
// editing a backend file this change does not own. Recorded in
// PROPOSED-DIVERGENCES.md as a post-merge cleanup.
//
// The subset is exact for every string this file can produce and for anything
// a user could put in a device name or an install path: whitespace splits,
// `'…'` is literal, `"…"` honours a backslash before `"` or `\`, and a bare
// backslash escapes the next character. Its two failures carry CPython's own
// ValueError messages under [kerrors.ErrValue], as the backend copy does.
func shlexSplit(s string) ([]string, error) {
	var (
		out   []string
		word  strings.Builder
		have  bool
		quote byte
	)
	flush := func() {
		if have {
			out = append(out, word.String())
			word.Reset()
			have = false
		}
	}

	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
				continue
			}
			word.WriteByte(c)
		case quote == '"':
			if c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				i++
				word.WriteByte(s[i])
				continue
			}
			if c == '"' {
				quote = 0
				continue
			}
			word.WriteByte(c)
		case c == '\'' || c == '"':
			quote = c
			have = true
		case c == '\\':
			if i+1 >= len(s) {
				return nil, kerrors.NewValue("No escaped character")
			}
			i++
			word.WriteByte(s[i])
			have = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			word.WriteByte(c)
			have = true
		}
	}
	if quote != 0 {
		return nil, kerrors.NewValue("No closing quotation")
	}
	flush()
	return out, nil
}
