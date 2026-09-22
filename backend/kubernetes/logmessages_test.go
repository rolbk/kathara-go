package kubernetes

import "testing"

// TestExecCommandLogLine pins `exec`'s debug line
// (`KubernetesMachine.py:817`). Unlike the Docker backend's, it is logged
// AFTER the `shlex.split` at `:816`, so the command is always a list and
// always renders as the list's repr.
func TestExecCommandLogLine(t *testing.T) {
	got := execCommandLogLine([]string{"ls", "-la"}, "pc1")
	want := "Executing command `['ls', '-la']` to device with name: pc1"
	if got != want {
		t.Errorf("execCommandLogLine = %q, want %q", got, want)
	}
}
