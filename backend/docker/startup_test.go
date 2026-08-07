package docker

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestStartupCommandsMatchPython compares the rendered one-liners against
// testdata/startup.json, which was produced by importing `STARTUP_COMMANDS` and
// `SHUTDOWN_COMMANDS` from 3.8.3 and running its own
// `"; ".join(...).format(...)` on them.
//
// It is a byte comparison of the whole line, which is the only useful
// granularity: the fragments are quote-heavy shell whose `sed` expression
// carries a literal backslash-n, and any transcription slip anywhere in the
// list changes what runs inside every container.
func TestStartupCommandsMatchPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/startup.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var golden map[string]string
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}

	// `machine_commands` defaults to ":" — the shell no-op — so the joined line
	// never has an empty segment.
	if got := renderStartupCommands("pc1", nil); got != golden["startup_noop"] {
		t.Errorf("startup with no exec commands:\n got %q\nwant %q", got, golden["startup_noop"])
	}

	// The device's exec commands, already echo-interleaved by `start`.
	withCommands := renderStartupCommands("r_2", interleaveExecCommands([]string{"ip a"}))
	if withCommands != golden["startup_cmds"] {
		t.Errorf("startup with exec commands:\n got %q\nwant %q", withCommands, golden["startup_cmds"])
	}

	if got := renderShutdownCommands("pc1"); got != golden["shutdown"] {
		t.Errorf("shutdown:\n got %q\nwant %q", got, golden["shutdown"])
	}
}

// TestStartupCommandOrder is SYNTHESIS §1.10 and ORDERING.tsv row 42: the
// sequence inside the container is contract, and `touch /tmp/EOS` must be LAST
// because it is the sentinel `_wait_startup_execution` polls for.
func TestStartupCommandOrder(t *testing.T) {
	line := renderStartupCommands("pc1", nil)

	stages := []string{
		"umount /etc/resolv.conf",
		"umount /etc/hosts",
		"/hostlab/pc1\"",
		"/etc/hosts\"",
		"/var/www",
		"/etc/quagga",
		"/etc/frr",
		"/hostlab/shared.startup",
		"/hostlab/pc1.startup",
		"touch /tmp/EOS",
	}

	previous := -1
	for _, stage := range stages {
		at := strings.Index(line, stage)
		if at < 0 {
			t.Fatalf("stage %q missing from the startup line", stage)
		}
		if at <= previous {
			t.Errorf("stage %q is out of order", stage)
		}
		previous = at
	}

	if !strings.HasSuffix(line, "touch /tmp/EOS") {
		t.Error("the /tmp/EOS sentinel is not the last command")
	}
}

// TestInterleaveExecCommands is `DockerMachine.start:538-543`: each command is
// preceded by an echo of itself into the startup log, so `kathara exec`'s log
// replay shows what ran.
func TestInterleaveExecCommands(t *testing.T) {
	got := interleaveExecCommands([]string{"ip a", "echo hi"})
	want := []string{
		`echo "++ ip a" &>> /var/log/startup.log`,
		"ip a",
		`echo "++ echo hi" &>> /var/log/startup.log`,
		"echo hi",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("interleaveExecCommands = %q, want %q", got, want)
	}

	if interleaveExecCommands(nil) != nil {
		t.Error("an empty command list should stay empty")
	}
}

// TestInterleaveExecCommandsIsNotIdempotent records the destructive rewrite of
// docker-backend.md gotcha 14: Python assigns the doubled list back onto the
// device's meta, so starting the same object twice wraps the echoes again. The
// port reproduces the mutation, so the second pass must double again rather
// than notice.
func TestInterleaveExecCommandsIsNotIdempotent(t *testing.T) {
	once := interleaveExecCommands([]string{"ip a"})
	twice := interleaveExecCommands(once)
	if len(twice) != 2*len(once) {
		t.Fatalf("second pass produced %d commands, want %d", len(twice), 2*len(once))
	}
	if !strings.Contains(twice[0], `++ echo "++ ip a"`) {
		t.Errorf("second pass did not re-wrap: %q", twice[0])
	}
}

// TestStartupTemplateHasNoStrayBraces is what makes the literal `ReplaceAll`
// substitution safe where Python uses `str.format`: `format` raises on a stray
// brace, and a literal replace would silently keep one.
func TestStartupTemplateHasNoStrayBraces(t *testing.T) {
	for _, line := range append(append([]string{}, startupCommands...), shutdownCommands...) {
		stripped := strings.ReplaceAll(line, "{machine_name}", "")
		stripped = strings.ReplaceAll(stripped, "{machine_commands}", "")
		if strings.ContainsAny(stripped, "{}") {
			t.Errorf("stray brace in %q", line)
		}
	}
}
