// So the command lines are Python's, character for character, including the
// double space `"%s connect %s -l %s"` leaves where `is_vmachine` is empty. A
// Go `strings.Join` of non-empty parts would have produced a tidier string and
// a different byte sequence, and the tidier string is not what this is for.

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
// adapter — Python's `exec_by_platform` falls off the end there and
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
func WindowsSpec(connectCommand, dir string) Spec {
	return Spec{
		Path: "powershell.exe",
		Args: []string{"-Command", "& " + connectCommand},
		Dir:  dir,
	}
}

// DarwinCommand is `osx_connect`'s `complete_osx_command`
// (`cli/ui/utils.py:171-174`):
func DarwinCommand(connectCommand, labPath string) string {
	cdPrefix := ""
	if labPath != "" {
		cdPrefix = `cd "` + labPath + `" &&`
	}
	return cdPrefix + " clear && " + connectCommand + " && exit"
}

// DarwinSpec is the osascript replacement for the two appscript drivers.
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
