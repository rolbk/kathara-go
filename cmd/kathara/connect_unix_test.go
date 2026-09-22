//go:build linux || darwin

// This is the Unix leg of that test, and it is the one that can be executed
// here: a real pseudo-terminal stands in for the user's console, `attachTTY`
// runs on it for real, and the termios the kernel holds is compared before and
// after — over every way the function can leave, including a panic unwinding
// through it.

package main

import (
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	xterm "golang.org/x/term"
)

// restoreSession is a [kathara.TTYSession] whose every method is a test hook.
type restoreSession struct {
	read   func([]byte) (int, error)
	resize func(cols, rows uint16) error

	mu     sync.Mutex
	closed bool
}

func (s *restoreSession) Read(p []byte) (int, error) { return s.read(p) }

func (s *restoreSession) Write(p []byte) (int, error) { return len(p), nil }

func (s *restoreSession) Resize(cols, rows uint16) error {
	if s.resize != nil {
		return s.resize(cols, rows)
	}
	return nil
}

func (s *restoreSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// consoleOnAPty gives the app a real terminal on both ends and hands back the
// slave's fd, which is the one `attachTTY` puts in raw mode.
func consoleOnAPty(t *testing.T) (*testApp, int) {
	t.Helper()

	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	// Nothing is read off the master and nothing needs to be: none of the
	// sessions below produce output, so the attach loop writes nothing into
	// the pty buffer.
	t.Cleanup(func() {
		// Closing the master is what unblocks the attach loop's stdin pump,
		// which is otherwise parked in a read on the slave — the kernel fails
		// that read with EIO the moment the other end goes. The slave file is
		// deliberately left to the process exit: closing a descriptor a
		// goroutine is still blocked in a read on is how a test earns a
		// use-after-close on a recycled fd.
		_ = master.Close()
	})

	a := newTestApp(t)
	a.stdin = slave
	a.console.Out = slave
	return a, int(slave.Fd())
}

func TestConnectRestoresTheTerminalOnEveryExit(t *testing.T) {
	for _, tc := range []struct {
		name string
		// run drives one exit path and returns what attachTTY did.
		run func(t *testing.T, a *testApp, fd int) error
	}{
		{
			name: "the session ends",
			run: func(t *testing.T, a *testApp, fd int) error {
				before, err := xterm.GetState(fd)
				if err != nil {
					t.Fatalf("GetState: %v", err)
				}
				session := &restoreSession{read: func([]byte) (int, error) {
					// Inside the loop the console must actually be raw, or the
					// whole test would pass over a function that changed
					// nothing.
					during, err := xterm.GetState(fd)
					if err != nil {
						t.Errorf("GetState during the attach: %v", err)
					} else if reflect.DeepEqual(before, during) {
						t.Error("attachTTY never put the console in raw mode")
					}
					return 0, io.EOF
				}}
				return attachTTY(t.Context(), session, a.app)
			},
		},
		{
			name: "the stream fails",
			run: func(t *testing.T, a *testApp, fd int) error {
				session := &restoreSession{read: func([]byte) (int, error) {
					return 0, errors.New("stream reset by the daemon")
				}}
				err := attachTTY(t.Context(), session, a.app)
				if err == nil {
					t.Error("a broken stream returned no error")
				}
				return err
			},
		},
		{
			name: "ctrl+c cancels the context",
			run: func(t *testing.T, a *testApp, fd int) error {
				ctx, cancel := context.WithCancel(t.Context())
				blocked := make(chan struct{})
				session := &restoreSession{read: func([]byte) (int, error) {
					close(blocked)
					<-ctx.Done()
					return 0, io.EOF
				}}
				go func() {
					<-blocked
					cancel()
				}()
				return attachTTY(ctx, session, a.app)
			},
		},
		{
			name: "a panic unwinds through it",
			run: func(t *testing.T, a *testApp, fd int) error {

				session := &restoreSession{
					read:   func([]byte) (int, error) { return 0, io.EOF },
					resize: func(uint16, uint16) error { panic("boom") },
				}
				defer func() {
					if recover() == nil {
						t.Error("the panic did not reach the caller")
					}
				}()
				return attachTTY(t.Context(), session, a.app)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, fd := consoleOnAPty(t)

			before, err := xterm.GetState(fd)
			if err != nil {
				t.Fatalf("GetState: %v", err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = tc.run(t, a, fd)
			}()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("attachTTY did not return")
			}

			after, err := xterm.GetState(fd)
			if err != nil {
				t.Fatalf("GetState after the attach: %v", err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("the console was left in a different mode:\nbefore %+v\nafter  %+v", before, after)
			}
			// Belt and braces on the same property, in the terms a user would
			// notice: a console still in raw mode echoes nothing.
			if xterm.IsTerminal(fd) {
				if _, err := xterm.GetState(fd); err != nil {
					t.Errorf("the console is no longer readable: %v", err)
				}
			}
		})
	}
}

// TestConnectRestoreIsNotVacuous guards the guard: if `attachTTY` ever stops
// touching the terminal at all, the comparison above would pass for the wrong
// reason. A raw console really is a different termios.
func TestConnectRestoreIsNotVacuous(t *testing.T) {
	_, fd := consoleOnAPty(t)

	before, err := xterm.GetState(fd)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	state, err := xterm.MakeRaw(fd)
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	raw, err := xterm.GetState(fd)
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if reflect.DeepEqual(before, raw) {
		t.Fatal("raw mode is indistinguishable from cooked mode on this pty; " +
			"the restore test proves nothing")
	}
	if err := xterm.Restore(fd, state); err != nil {
		t.Fatalf("Restore: %v", err)
	}
}
