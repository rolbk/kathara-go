//go:build linux || darwin

package term

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// unixPty implements Pty over creack/pty (PACKAGE_GRAPH: term → creack/pty
// v1.1.24). The per-OS constructors in pty_linux.go / pty_darwin.go both land
// here; they exist as the seam where genuinely divergent per-OS behaviour
// (e.g. external-emulator spawning) attaches in Phase 6.
type unixPty struct {
	mu      sync.Mutex
	ws      Winsize
	master  *os.File // pty master; nil until Start
	started bool
	closed  bool
}

var _ Pty = (*unixPty)(nil)

func newUnixPty(ws Winsize) (Pty, error) {
	return &unixPty{ws: ws}, nil
}

func (p *unixPty) Start(cmd *exec.Cmd) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errClosed
	}
	if p.started {
		return errAlreadyStarted
	}
	// StartWithSize opens the pty pair, applies the size to the slave before
	// the child runs (so the initial size is never racy — the Windows leg
	// mirrors this by passing the size to CreatePseudoConsole), wires the
	// slave to cmd's stdio, and calls cmd.Start.
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: p.ws.Rows, Cols: p.ws.Cols})
	if err != nil {
		return err
	}
	p.master = f
	p.started = true
	return nil
}

func (p *unixPty) Resize(ws Winsize) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errClosed
	}
	p.ws = ws
	if !p.started {
		// Pre-start: becomes the size passed to StartWithSize.
		return nil
	}
	return pty.Setsize(p.master, &pty.Winsize{Rows: ws.Rows, Cols: ws.Cols})
}

func (p *unixPty) Read(b []byte) (int, error) {
	f, err := p.file()
	if err != nil {
		return 0, err
	}
	n, err := f.Read(b)
	// Linux returns EIO from the master once the slave side is gone (child
	// exited and no other slave fd open); macOS returns plain EOF. Normalize
	// to io.EOF so callers get one portable end-of-stream signal.
	if err != nil && errors.Is(err, syscall.EIO) {
		return n, io.EOF
	}
	return n, err
}

func (p *unixPty) Write(b []byte) (int, error) {
	f, err := p.file()
	if err != nil {
		return 0, err
	}
	return f.Write(b)
}

// Close releases the master. It does not signal the child: the caller owns
// cmd.Process (same contract as creack/pty). Idempotent.
func (p *unixPty) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.master == nil {
		return nil
	}
	return p.master.Close()
}

func (p *unixPty) file() (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errClosed
	}
	if !p.started {
		return nil, errNotStarted
	}
	return p.master, nil
}
