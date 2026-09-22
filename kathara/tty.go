// This file is `foundation/manager/terminal/core/ITerminalSession.py`: the
// bidirectional byte stream a backend opens onto a running device, and the
// only thing `term` needs from a backend in order to draw a terminal on it.

package kathara

// TTYSession is an interactive session on a running device
// (`foundation/manager/terminal/core/ITerminalSession.py`) — a Docker attach
// hijack on Unix, a named pipe on Windows, an exec websocket on Kubernetes.
type TTYSession interface {
	// Read is `read(n)`: fill p with output from the device, io.Reader
	// semantics.
	Read(p []byte) (int, error)

	// Write is `write(data)`: send input to the device, io.Writer semantics.
	Write(p []byte) (int, error)

	// Resize is `resize(cols, rows)`: tell the device its new geometry.
	// Columns come first. Preserve this order to maintain the expected
	// column/row mapping; `term.Winsize` uses the same order.
	Resize(cols, rows uint16) error

	// Close ends the session and releases the transport. It is safe to call
	// more than once — Python's `_closed` flag exists for exactly that, and
	// `TerminalRunner` closes from several arms of its cleanup.
	Close() error
}
