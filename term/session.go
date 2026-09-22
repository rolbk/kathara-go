// This file is the seam between the multiplexer and whatever is on the other
// end of a pane.

package term

import (
	"os/exec"
	"sync"
)

// Session is one pane's bidirectional byte stream.
type Session interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
}

// PtySession is a [Session] backed by a local child process on a [Pty].
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
