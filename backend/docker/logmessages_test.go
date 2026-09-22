package docker

import (
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
)

// TestCommandRepr pins the `%s` operand of `exec`'s debug line
// (`DockerMachine.py:777`), which interpolates the command BEFORE the
// `shlex.split` at `:803`. So a `str` command renders bare and a `List[str]`
// one renders as the list's repr.
func TestCommandRepr(t *testing.T) {
	tests := []struct {
		name    string
		command kathara.Command
		want    string
	}{
		{"str renders bare", kathara.NewShellCommand("ls -la"), "ls -la"},
		{"list renders as a repr", kathara.NewCommand("ls", "-la"), "['ls', '-la']"},
		{"empty list", kathara.NewCommand(), "[]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandRepr(tc.command); got != tc.want {
				t.Errorf("commandRepr = %q, want %q", got, tc.want)
			}
		})
	}
}
