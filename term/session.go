// This file is the seam between the multiplexer and whatever is on the other
// end of a pane.
//
// PACKAGE_GRAPH.md D-5 splits Python's conflated terminal classes into a
// backend-owned transport and a `term`-owned renderer. The transport interface
// is `kathara.TTYSession`; [Session] here is *the same method set*, declared
// again so that `term` needs no import of `kathara` at all. A
// `kathara.TTYSession` therefore satisfies [Session] with no adapter, and
// `term` stays a leaf that a Layer-A-free unit test can drive.
//
// The second implementation, [PtySession], puts a local child process behind a
// pane instead of a backend transport. It is what the real-pty integration test
// drives on Unix, and it is the seam docs/port/SPIKES/windows-terminal.md §1
// sketched for Windows, where a pane would host `kathara connect <device>`
// under a ConPTY.
//
// The shipped multiplexer does not use it: a pane attaches through the backend
// transport on every platform, which is one code path instead of two and lets
// bubbletea own the console modes (it sets the Windows VT modes itself). The
// local-child leg is kept because it is the tested shape a future embedded pane
// would need — see DIVERGENCES.md item 120 for what that leaves unproven on
// Windows.

package term

import (
	"os/exec"
	"sync"
)

// Session is one pane's bidirectional byte stream.
//
// The method set is `kathara.TTYSession`'s, deliberately: Resize takes columns
// first, which is the one axis-swap this port cannot afford
// (`kathara/tty.go`), and Close is idempotent.
//
// Concurrency contract, also inherited: one goroutine may sit in Read while
// another calls Write, Resize and Close. Nothing more is required of an
// implementation, and the multiplexer asks for nothing more.
type Session interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
}

// PtySession is a [Session] backed by a local child process on a [Pty].
//
// Close kills the child. That is not the Unix [Pty] contract — closing a pty
// master leaves the child running, and creack/pty is deliberate about it — but
// it is what a *pane* means: a pane's child is a client this process started
// and owns, and the ConPTY leg terminates the child on close anyway
// (SPIKES/windows-terminal.md §4.4), so killing here is what makes the two
// platforms behave the same.
//
// The containers behind the child are untouched by any of this, which is the
// "clean detach that leaves containers running" requirement of §3.3 item 1:
// what dies is a connect client, not a device.
type PtySession struct {
	pty Pty
	cmd *exec.Cmd

	closeOnce sync.Once
	closeErr  error
}

var _ Session = (*PtySession)(nil)

// StartPtySession opens a pty of the given size and starts cmd on it.
func StartPtySession(cmd *exec.Cmd, ws Winsize) (*PtySession, error) {
	p, err := New(ws)
	if err != nil {
		return nil, err
	}
	if err := p.Start(cmd); err != nil {
		// Closing an unstarted pty is a no-op on Unix and releases the
		// pseudoconsole on Windows; either way the failure must not leak it.
		_ = p.Close()
		return nil, err
	}
	return &PtySession{pty: p, cmd: cmd}, nil
}

func (s *PtySession) Read(p []byte) (int, error)  { return s.pty.Read(p) }
func (s *PtySession) Write(p []byte) (int, error) { return s.pty.Write(p) }

// Resize forwards the geometry to the pty, converting to the cols-first
// [Winsize] the local layer uses.
func (s *PtySession) Resize(cols, rows uint16) error {
	return s.pty.Resize(Winsize{Cols: cols, Rows: rows})
}

// Close kills the child, reaps it and releases the pty. It is idempotent and
// reports the pty's error, not the child's exit status: a killed child always
// "fails", and that is not information a caller can act on.
//
// The child is reaped through cmd.Process.Wait rather than cmd.Wait, which is
// the asymmetry SPIKES/windows-terminal.md §4.3 (work item W6-7) records:
// os/exec never observed a Start on the ConPTY leg, so cmd.Wait would report
// "not started" there. cmd.Process.Wait works on both.
func (s *PtySession) Close() error {
	s.closeOnce.Do(func() {
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			_, _ = s.cmd.Process.Wait()
		}
		s.closeErr = s.pty.Close()
	})
	return s.closeErr
}

// PrefixSession returns a [Session] whose Read yields prefix before anything
// the underlying session produces.
//
// It exists for one thing: `connect_tty`'s startup-log block. The backend
// writes that log to an io.Writer while the session is being opened
// (`kathara.ConnectTTYOptions.LogWriter`), which for a multiplexer pane is too
// early — the pane does not exist yet, and writing to the real stdout would
// land underneath bubbletea's alternate screen. Buffering it and replaying it
// as the first bytes of the stream puts it exactly where Python's `-l` put it:
// at the top of the terminal that opened onto the device.
func PrefixSession(s Session, prefix []byte) Session {
	if len(prefix) == 0 {
		return s
	}
	return &prefixSession{Session: s, prefix: prefix}
}

type prefixSession struct {
	Session
	prefix []byte
}

func (p *prefixSession) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Session.Read(b)
}

// Wait blocks until the child exits and returns its exit error, if any. It is
// how a pane learns that its child ended on its own rather than by Close.
func (s *PtySession) Wait() error {
	if s.cmd.Process == nil {
		return errNotStarted
	}
	state, err := s.cmd.Process.Wait()
	if err != nil {
		return err
	}
	if !state.Success() {
		return &exec.ExitError{ProcessState: state}
	}
	return nil
}
