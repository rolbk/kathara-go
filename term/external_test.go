package term

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// The expectations below were produced by running the Python side of
// `cli/ui/utils.open_machine_terminal` (CPython 3, `shlex` and the `%`
// interpolation, same as 3.8.3) and printing the argv each `subprocess.Popen`
// would receive. They are quoted verbatim, double spaces included.
//
//	'"/usr/local/bin/kathara" connect  -l pc1'
//	['gnome-terminal', '--', '/usr/local/bin/kathara', 'connect', '-l', 'pc1']
//	['/usr/bin/xterm', '-e', '"/usr/local/bin/kathara" connect  -l pc1']
//	['powershell.exe', '-Command', '& "/usr/local/bin/kathara" connect  -l pc1']
//	' clear && "/usr/local/bin/kathara" connect  -l pc1 && exit'

// quotedExe is what `internal/util.GetExecutablePath` hands the adapters: the
// resolved path, already wrapped in double quotes by Python and by the port.
const quotedExe = `"/usr/local/bin/kathara"`

func TestConnectCommandMatchesPython(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  Request
		want string
	}{
		{
			name: "scenario with a host path",
			req:  Request{Executable: quotedExe, Machine: "pc1"},
			// Two spaces after `connect`: `is_vmachine` is the empty string
			// and the format string interpolates it unconditionally.
			want: `"/usr/local/bin/kathara" connect  -l pc1`,
		},
		{
			name: "vstart device",
			req:  Request{Executable: quotedExe, Machine: "pc1", VMachine: true},
			want: `"/usr/local/bin/kathara" connect -v -l pc1`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ConnectCommand(tc.req); got != tc.want {
				t.Errorf("ConnectCommand()\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestLinuxSpecMatchesPython(t *testing.T) {
	cmdNoV := ConnectCommand(Request{Executable: quotedExe, Machine: "pc1"})
	cmdV := ConnectCommand(Request{Executable: quotedExe, Machine: "pc1", VMachine: true})

	for _, tc := range []struct {
		name     string
		terminal string
		command  string
		dir      string
		want     []string
	}{
		{
			name:     "xterm takes the -e branch with the command as one word",
			terminal: "/usr/bin/xterm",
			command:  cmdNoV,
			dir:      "/labs/demo",
			want:     []string{"/usr/bin/xterm", "-e", `"/usr/local/bin/kathara" connect  -l pc1`},
		},
		{
			name:     "gnome-terminal takes the -- branch with a shlex split",
			terminal: "gnome-terminal",
			command:  cmdNoV,
			want:     []string{"gnome-terminal", "--", "/usr/local/bin/kathara", "connect", "-l", "pc1"},
		},
		{
			name:     "gnome-terminal, vstart device",
			terminal: "gnome-terminal",
			command:  cmdV,
			want:     []string{"gnome-terminal", "--", "/usr/local/bin/kathara", "connect", "-v", "-l", "pc1"},
		},
		{
			// `'gnome-terminal' in terminal` is a substring test on the whole
			// setting value, so an absolute path matches too.
			name:     "an absolute gnome-terminal path still takes the -- branch",
			terminal: "/usr/bin/gnome-terminal",
			command:  cmdNoV,
			want:     []string{"/usr/bin/gnome-terminal", "--", "/usr/local/bin/kathara", "connect", "-l", "pc1"},
		},
		{
			name:     "any other emulator takes the -e branch",
			terminal: "/usr/bin/konsole",
			command:  cmdNoV,
			want:     []string{"/usr/bin/konsole", "-e", `"/usr/local/bin/kathara" connect  -l pc1`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := LinuxSpec(tc.terminal, tc.command, tc.dir)
			if err != nil {
				t.Fatalf("LinuxSpec: %v", err)
			}
			if got := spec.Argv(); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("argv\n got %q\nwant %q", got, tc.want)
			}
			if spec.Dir != tc.dir {
				t.Errorf("Dir = %q, want %q (Popen's cwd=machine.lab.fs_path())", spec.Dir, tc.dir)
			}
		})
	}
}

func TestWindowsSpecMatchesPython(t *testing.T) {
	cmd := ConnectCommand(Request{Executable: quotedExe, Machine: "pc1"})
	spec := WindowsSpec(cmd, `C:\labs\demo`)
	want := []string{"powershell.exe", "-Command", `& "/usr/local/bin/kathara" connect  -l pc1`}
	if got := spec.Argv(); !reflect.DeepEqual(got, want) {
		t.Errorf("argv\n got %q\nwant %q", got, want)
	}
	if spec.Dir != `C:\labs\demo` {
		t.Errorf("Dir = %q, want the lab path", spec.Dir)
	}
}

func TestDarwinCommandMatchesPython(t *testing.T) {
	cmd := ConnectCommand(Request{Executable: quotedExe, Machine: "pc1"})

	// With a host path the `cd` prefix is present; without one the format
	// string still contributes the space that separated it from `clear`.
	if got, want := DarwinCommand(cmd, "/labs/demo"),
		`cd "/labs/demo" && clear && "/usr/local/bin/kathara" connect  -l pc1 && exit`; got != want {
		t.Errorf("DarwinCommand with a lab path\n got %q\nwant %q", got, want)
	}
	if got, want := DarwinCommand(cmd, ""),
		` clear && "/usr/local/bin/kathara" connect  -l pc1 && exit`; got != want {
		t.Errorf("DarwinCommand without a lab path\n got %q\nwant %q", got, want)
	}
}

func TestDarwinSpecDrivers(t *testing.T) {
	cmd := DarwinCommand(ConnectCommand(Request{Executable: quotedExe, Machine: "pc1"}), "/labs/demo")

	t.Run("Terminal is do script", func(t *testing.T) {
		spec, ok := DarwinSpec("Terminal", cmd)
		if !ok {
			t.Fatal("DarwinSpec(Terminal) reported no driver")
		}
		want := []string{
			"/usr/bin/osascript", "-e",
			`tell application "Terminal" to do script "cd \"/labs/demo\" && clear && \"/usr/local/bin/kathara\" connect  -l pc1 && exit"`,
		}
		if got := spec.Argv(); !reflect.DeepEqual(got, want) {
			t.Errorf("argv\n got %q\nwant %q", got, want)
		}
	})

	t.Run("iTerm creates a window and writes into its session", func(t *testing.T) {
		spec, ok := DarwinSpec("iTerm", cmd)
		if !ok {
			t.Fatal("DarwinSpec(iTerm) reported no driver")
		}
		script := spec.Args[1]
		for _, want := range []string{
			`tell application "iTerm"`,
			"create window with default profile",
			"tell w's current session to write text",
			`\"/usr/local/bin/kathara\" connect  -l pc1`,
		} {
			if !strings.Contains(script, want) {
				t.Errorf("script missing %q:\n%s", want, script)
			}
		}
	})

	t.Run("any other application is a silent no-op, as in Python", func(t *testing.T) {
		// `osx_connect` has `if terminal == 'iTerm' … elif terminal ==
		// 'Terminal' …` and no else, so a third name opens nothing at all.
		if _, ok := DarwinSpec("Alacritty", cmd); ok {
			t.Error("DarwinSpec invented a driver for an unsupported application")
		}
	})
}

func TestShlexSplit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{`"/usr/local/bin/kathara" connect  -l pc1`, []string{"/usr/local/bin/kathara", "connect", "-l", "pc1"}},
		{`'a b' c`, []string{"a b", "c"}},
		{`a\ b`, []string{"a b"}},
		{`"a\"b"`, []string{`a"b`}},
		{`'a\b'`, []string{`a\b`}},
		{`""`, []string{""}},
		{"  ", nil},
	} {
		got, err := shlexSplit(tc.in)
		if err != nil {
			t.Errorf("shlexSplit(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("shlexSplit(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestShlexSplitErrorsCarryCPythonMessages(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`"unterminated`, "No closing quotation"},
		{`trailing\`, "No escaped character"},
	} {
		_, err := shlexSplit(tc.in)
		if err == nil {
			t.Fatalf("shlexSplit(%q) accepted a malformed command", tc.in)
		}
		if !errors.Is(err, kerrors.ErrValue) {
			t.Errorf("shlexSplit(%q) error is not ErrValue: %v", tc.in, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("shlexSplit(%q) = %q, want it to contain %q", tc.in, err, tc.want)
		}
	}
}

func TestModeFor(t *testing.T) {
	for _, tc := range []struct {
		terminal string
		want     Mode
	}{
		{TerminalMultiplexer, ModeMultiplexer},
		{"", ModeMultiplexer},
		{TerminalTMUX, ModeTmux},
		{"/usr/bin/xterm", ModeExternal},
		{"Terminal", ModeExternal},
		{"gnome-terminal", ModeExternal},
		// The tokens are exact, not case-insensitive: Python's own `== "TMUX"`
		// is too.
		{"tmux", ModeExternal},
		{"multiplexer", ModeExternal},
	} {
		if got := ModeFor(tc.terminal); got != tc.want {
			t.Errorf("ModeFor(%q) = %v, want %v", tc.terminal, got, tc.want)
		}
	}
}
