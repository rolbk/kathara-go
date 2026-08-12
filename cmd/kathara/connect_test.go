package main

import (
	"context"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// connectManager is a backend that answers exactly one `ConnectTTY`.
type connectManager struct {
	kathara.Manager

	session kathara.TTYSession
	err     error
	calls   int
}

func (m *connectManager) ConnectTTY(
	context.Context, string, kathara.LabRef, kathara.ConnectTTYOptions,
) (kathara.TTYSession, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return m.session, nil
}

// TestConnectMuxAttachesBeforeItDrawsAnything is PORT_SPEC §3.3 item 4's
// "unchanged behaviour" where it is easiest to lose: the failure modes.
//
// A device that is not running has to fail the way it failed before the
// multiplexer existed — exit 1, the error on the console, no window — rather
// than open an alternate screen, render the error into a pane the user then has
// to detach from, and exit 0.
func TestConnectMuxAttachesBeforeItDrawsAnything(t *testing.T) {
	t.Run("a failed attach exits 1 with the error", func(t *testing.T) {
		mgr := &connectManager{err: kerrors.NewMachineNotRunning("pc1")}

		code, err := runConnectMux(t.Context(), mgr, "pc1",
			kathara.LabRef{}, kathara.DefaultConnectTTYOptions())

		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if err == nil || !strings.Contains(err.Error(), "pc1") {
			t.Errorf("error = %v, want the backend's own", err)
		}
		if kerrors.Code(err) != kerrors.Code(kerrors.NewMachineNotRunning("pc1")) {
			t.Errorf("code = %q, want the backend's own", kerrors.Code(err))
		}
	})

	t.Run("a device that vanished mid-startup exits 0 silently", func(t *testing.T) {
		// `startup_waited == 2`: `ConnectTTY` returns (nil, nil) and Python
		// returns None without raising (`DockerMachine.py:698-699`). There is
		// nothing to attach to and nothing to report — and no window either.
		mgr := &connectManager{}

		code, err := runConnectMux(t.Context(), mgr, "pc1",
			kathara.LabRef{}, kathara.DefaultConnectTTYOptions())

		if code != 0 || err != nil {
			t.Errorf("runConnectMux = (%d, %v), want (0, nil)", code, err)
		}
		if mgr.calls != 1 {
			t.Errorf("ConnectTTY called %d times, want exactly 1", mgr.calls)
		}
	})
}
