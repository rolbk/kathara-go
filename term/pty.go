// Package term hosts the rebuilt terminal integration (PORT_SPEC §0.2 #2):
// the built-in multiplexer (default), the tmux backend (term/tmuxdrv), the
// opt-in external-emulator adapters, the single-device connect runner, and the
// raw-mode console + local PTY layer.
//
// This file defines the minimal local pseudo-terminal seam shared by the Unix
// (creack/pty) and Windows (ConPTY) implementations. It is a Phase 2 spike
// surface: deliberately tiny, no bubbletea, no console-mode handling — see
// docs/port/SPIKES/windows-terminal.md for the full design and the Phase 6
// work items that grow around it.
//
// A local Pty hosts a *local child process* (a multiplexer pane running the
// connect runner, or an external-emulator helper). It is NOT the transport to
// a device: device attach is a backend concern (docker hijacked conn on Unix,
// npipe on Windows, K8s websocket) living in backend/*/tty*.go per
// PACKAGE_GRAPH D-5.
package term

import (
	"errors"
	"io"
	"os/exec"
)

// Winsize is a terminal geometry in character cells.
//
// Cols-first, matching the Python ITerminalSession.resize(cols, rows)
// argument order (analysis/manager-foundation.md §1.7) so the eventual
// runner/session plumbing cannot silently swap axes.
type Winsize struct {
	Cols uint16
	Rows uint16
}

// Errors returned by Pty implementations for lifecycle misuse. Kept
// package-internal in spirit (callers should not branch on them yet); they
// exist so tests can assert the exact failure and so the Phase 6 runner can
// map them onto ERROR_CODES.md entries in one place.
var (
	errAlreadyStarted = errors.New("term: pty already started")
	errNotStarted     = errors.New("term: pty not started")
	errClosed         = errors.New("term: pty closed")
)

// Pty is one local pseudo-terminal hosting one child process.
//
// Lifecycle: New → (optional Resize) → Start → concurrent Read/Write/Resize →
// Close. Start may be called at most once. Close is idempotent and does NOT
// kill the child on Unix (parity with creack/pty: the caller owns cmd.Process);
// on Windows closing the pseudoconsole disconnects the child's console, which
// normally terminates console-attached clients — a recorded platform
// difference, see docs/port/SPIKES/windows-terminal.md §4.4.
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
	// recorded as Phase 6 work item W6-3).
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
