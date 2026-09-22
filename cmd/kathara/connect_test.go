package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

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

// blockingSession is the shell that never says anything and never exits — the
// session a redirected `connect` would park on forever.
type blockingSession struct {
	reads  atomic.Int64
	closed atomic.Bool
	block  chan struct{}
}

func (s *blockingSession) Read(p []byte) (int, error) {
	s.reads.Add(1)
	<-s.block
	return 0, os.ErrClosed
}

func (s *blockingSession) Write(p []byte) (int, error) { return len(p), nil }
func (s *blockingSession) Resize(_, _ uint16) error    { return nil }
func (s *blockingSession) Close() error                { s.closed.Store(true); return nil }

// TestConnectFailsFastWhenStdinIsNotATerminal is the parity property of
// [errStdinNotATerminal].
func TestConnectFailsFastWhenStdinIsNotATerminal(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })

	session := &blockingSession{block: make(chan struct{})}
	t.Cleanup(func() { close(session.block) })

	a := newTestApp(t)
	a.stdin = r
	withManager(a, &connectManager{session: session})

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		// `-v` addresses `kathara_vlab` by name, which keeps the test off the
		// scenario parser; the attach path under test is the same one.
		code, err := runConnect(t.Context(), a.app, &connectFlags{vmachine: true}, []string{"pc1"})
		done <- result{code, err}
	}()

	var got result
	select {
	case got = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("`connect` with a non-terminal stdin never returned")
	}

	if got.code != 1 {
		t.Errorf("exit = %d, want 1", got.code)
	}
	if !errors.Is(got.err, errStdinNotATerminal) {
		t.Errorf("error = %v, want %v", got.err, errStdinNotATerminal)
	}
	// The errno Python reported, reachable the Go way.
	if !errors.Is(got.err, syscall.ENOTTY) {
		t.Errorf("error = %v, want it to wrap ENOTTY", got.err)
	}

	if code := kerrors.Code(got.err); code != kerrors.CodeInternalError {
		t.Errorf("code = %q, want %q", code, kerrors.CodeInternalError)
	}
	// Fail-fast means the pump never started: not one byte was read off the
	// session, and it was handed back.
	if n := session.reads.Load(); n != 0 {
		t.Errorf("the session was read %d times, want 0", n)
	}
	if !session.closed.Load() {
		t.Error("the session was not closed")
	}
}

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
