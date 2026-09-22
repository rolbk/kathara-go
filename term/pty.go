package term

import (
	"errors"
	"io"
	"os/exec"
)

// Winsize is a terminal geometry in character cells.
type Winsize struct {
	Cols uint16
	Rows uint16
}

// Errors returned by Pty implementations for lifecycle misuse.
var (
	errAlreadyStarted = errors.New("term: pty already started")
	errNotStarted     = errors.New("term: pty not started")
	errClosed         = errors.New("term: pty closed")
)

// Pty is one local pseudo-terminal hosting one child process.
type Pty interface {
	// Start launches cmd with stdin/stdout/stderr (and, on Windows, its
	// console) attached to the pty's child side, honouring cmd.Dir and
	// cmd.Env. On success cmd.Process is set, so callers can Kill and — on
	// Unix, where cmd.Start was used underneath — cmd.Wait. On Windows the
	// process was not started by os/exec, so use cmd.Process.Wait, not
	// cmd.Wait.
	Start(cmd *exec.Cmd) error

	// Resize sets the terminal size. Before Start it adjusts the size the
	// child will be created with; after Start it resizes the live terminal
	// (TIOCSWINSZ on Unix, ResizePseudoConsole on Windows).
	Resize(ws Winsize) error

	// Read returns child output (VT byte stream on Windows — ConPTY always
	// renders to VT sequences). After the child exits and buffered output
	// drains, Read returns io.EOF on all platforms (Linux's EIO-on-hangup is
	// normalized, see pty_unix.go).
	io.Reader

	// Write delivers input bytes to the child as terminal input.
	io.Writer

	// Close releases the pty. It unblocks pending Unix reads; on Windows a
	// pending Read may not unblock until the child exits (spike limitation,
	// recorded as follow-up work).
	io.Closer
}

// New returns an unstarted Pty for the current OS with initial size ws.
// Zero Cols/Rows default to 80×24 — ConPTY rejects zero dimensions
// (E_INVALIDARG) and a 0×0 Unix pty is useless, so the seam normalizes
// centrally rather than per-OS.
func New(ws Winsize) (Pty, error) {
	if ws.Cols == 0 {
		ws.Cols = 80
	}
	if ws.Rows == 0 {
		ws.Rows = 24
	}
	return newPty(ws)
}
