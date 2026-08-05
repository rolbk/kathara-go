package tmuxdrv

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestSessionName(t *testing.T) {
	t.Parallel()

	// The OQ-9 ruling, pinned: named scenarios by name, unnamed by hash,
	// everything prefixed, everything sanitized the way tmux would sanitize it.
	tests := []struct {
		name    string
		labName string
		labHash string
		want    string
	}{
		{"named lab", "megalos", "d41d8cd98f00b204", "kathara_megalos"},
		{"unnamed lab falls back to hash", "", "d41d8cd98f00b204", "kathara_d41d8cd98f00b204"},
		{"vlab", "kathara_vlab", "9bd0a0e1", "kathara_kathara_vlab"},
		{"dot rewritten like tmux does", "lab.v2", "h", "kathara_lab_v2"},
		{"colon rewritten like tmux does", "lab:v2", "h", "kathara_lab_v2"},
		{"space rewritten", "my lab", "h", "kathara_my_lab"},
		{"newline rewritten", "my\nlab", "h", "kathara_my_lab"},
		{"leading dash neutralized", "-rf", "h", "kathara__rf"},
		{"no identity at all", "", "", "kathara_default"},
		{"unicode preserved", "réseau", "h", "kathara_réseau"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SessionName(tc.labName, tc.labHash); got != tc.want {
				t.Errorf("SessionName(%q, %q) = %q, want %q", tc.labName, tc.labHash, got, tc.want)
			}
		})
	}
}

func TestSessionNameIsAlwaysAcceptable(t *testing.T) {
	t.Parallel()

	// Whatever SessionName emits must survive checkSessionName, otherwise
	// EnsureSession would reject names the port itself produced.
	inputs := []string{"", "lab", "a.b:c", "  ", "-x", "\t\n", "ünïcode"}
	for _, in := range inputs {
		name := SessionName(in, "fallbackhash")
		if err := checkSessionName(name); err != nil {
			t.Errorf("SessionName(%q, ...) = %q, rejected by checkSessionName: %v", in, name, err)
		}
	}
}

func TestCheckNames(t *testing.T) {
	t.Parallel()

	if err := checkSessionName(""); !errors.Is(err, ErrInvalidName) {
		t.Errorf("empty session name: got %v, want ErrInvalidName", err)
	}
	if err := checkSessionName("lab.1"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("unsanitized session name: got %v, want ErrInvalidName", err)
	}
	if err := checkSessionName("kathara_lab1"); err != nil {
		t.Errorf("valid session name rejected: %v", err)
	}

	for _, bad := range []string{"", "pc\t1", "pc\n1", "-pc1"} {
		if err := checkWindowName(bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("checkWindowName(%q) = %v, want ErrInvalidName", bad, err)
		}
	}
	if err := checkWindowName("pc1"); err != nil {
		t.Errorf("valid window name rejected: %v", err)
	}
}

func TestTargetsAreExactMatch(t *testing.T) {
	t.Parallel()

	// tmux resolves a bare target by exact name, then prefix, then fnmatch:
	// "-t lab" would select "lab1". The '=' prefix is what makes our probes
	// mean what they say; TestExactMatchTargeting proves it against real tmux.
	if got, want := sessionTarget("lab"), "=lab"; got != want {
		t.Errorf("sessionTarget = %q, want %q", got, want)
	}
	if got, want := windowTarget("lab", "pc1"), "=lab:=pc1"; got != want {
		t.Errorf("windowTarget = %q, want %q", got, want)
	}
}

func TestArgvComposition(t *testing.T) {
	t.Parallel()

	d := &Driver{Socket: "sock", Config: os.DevNull}
	got := d.Argv("has-session", "-t", "=kathara_x")
	want := []string{"tmux", "-f", os.DevNull, "-L", "sock", "has-session", "-t", "=kathara_x"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("Argv = %v, want %v", got, want)
	}

	bare := (&Driver{}).Argv("kill-server")
	if strings.Join(bare, " ") != "tmux kill-server" {
		t.Errorf("zero-value Argv = %v, want [tmux kill-server]", bare)
	}

	attach := (&Driver{Bin: "/usr/bin/tmux", Socket: "s"}).AttachArgs("kathara_lab")
	wantAttach := []string{"/usr/bin/tmux", "-L", "s", "attach-session", "-t", "=kathara_lab"}
	if strings.Join(attach, " ") != strings.Join(wantAttach, " ") {
		t.Errorf("AttachArgs = %v, want %v", attach, wantAttach)
	}
}

func TestDriverValidateRejectsTwoSockets(t *testing.T) {
	t.Parallel()

	d := &Driver{Socket: "name", SocketPath: "/tmp/sock"}
	if err := d.validate(); err == nil {
		t.Fatal("Socket + SocketPath accepted, want error")
	}
}

func TestWindowCommandArgsAreGuarded(t *testing.T) {
	t.Parallel()

	// A command must never be readable as a flag.
	w := Window{Name: "pc1", Command: "-not-a-flag", Dir: "/tmp", Env: []string{"A=B"}}
	if got, want := strings.Join(w.commandArgs(), " "), "-- -not-a-flag"; got != want {
		t.Errorf("commandArgs = %q, want %q", got, want)
	}
	if got, want := strings.Join(w.creationFlags(), " "), "-c /tmp -e A=B"; got != want {
		t.Errorf("creationFlags = %q, want %q", got, want)
	}
	if empty := (Window{Name: "pc1"}).commandArgs(); empty != nil {
		t.Errorf("commandArgs with no command = %v, want nil", empty)
	}
}

func TestOuterSocketPath(t *testing.T) {
	// $TMUX is "<socket-path>,<server-pid>,<session-id>".
	t.Setenv("TMUX", "/tmp/tmux-0/default,12371,0")
	if got, want := outerSocketPath(), "/tmp/tmux-0/default"; got != want {
		t.Errorf("outerSocketPath = %q, want %q", got, want)
	}
	if !InsideTmux() {
		t.Error("InsideTmux = false with $TMUX set")
	}

	t.Setenv("TMUX", "")
	if got := outerSocketPath(); got != "" {
		t.Errorf("outerSocketPath with empty $TMUX = %q, want %q", got, "")
	}
	if InsideTmux() {
		t.Error("InsideTmux = true with $TMUX empty")
	}
}

func TestEnvironWithoutTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-0/default,1,0")
	t.Setenv("TMUX_PANE", "%0")
	for _, kv := range environWithoutTmux() {
		if strings.HasPrefix(kv, "TMUX=") {
			t.Fatalf("environWithoutTmux kept %q", kv)
		}
	}
	// TMUX_PANE is inert for attach and must not be stripped by a sloppy
	// prefix match.
	var sawPane bool
	for _, kv := range environWithoutTmux() {
		if kv == "TMUX_PANE=%0" {
			sawPane = true
		}
	}
	if !sawPane {
		t.Error("environWithoutTmux stripped TMUX_PANE; only TMUX must go")
	}
}
