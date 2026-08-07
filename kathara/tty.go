// This file is `foundation/manager/terminal/core/ITerminalSession.py`: the
// bidirectional byte stream a backend opens onto a running device, and the
// only thing `term` needs from a backend in order to draw a terminal on it.

package kathara

// TTYSession is an interactive session on a running device
// (`foundation/manager/terminal/core/ITerminalSession.py`) — a Docker attach
// hijack on Unix, a named pipe on Windows, an exec websocket on Kubernetes.
//
// PACKAGE_GRAPH.md D-5 is why this interface exists at all. The Python
// "terminal" classes conflate the transport with the rendering; the §0.2 #2
// rebuild moves rendering into `term`, but a transport is backend-SDK code and
// has to stay behind the same import boundary as the SDK. So backends
// implement this, `term` consumes it, and neither imports the other.
//
// The Python base class also declares `fileno() -> Optional[int]`, which its
// `TerminalRunner` used to choose between an fd-readiness loop and a threaded
// pump (NILABILITY.tsv:178). Go needs neither: a goroutine blocked on Read is
// the whole of the threaded pump and costs nothing, so the fd escape hatch —
// and the "fd 0 is valid, test with `is None`" trap that came with it — does
// not survive.
//
// An implementation is not required to be safe for concurrent use beyond the
// one pattern `term` actually runs: one goroutine in [TTYSession.Read] while
// another calls [TTYSession.Write] and [TTYSession.Resize]. That pattern must
// work.
type TTYSession interface {
	// Read is `read(n)`: fill p with output from the device, io.Reader
	// semantics.
	//
	// Python returns `b""` at EOF and its two pump implementations disagree
	// about what that means — the fd path closes, the threaded path sleeps
	// 30 ms and polls again (analysis/manager-foundation.md §7 gotcha 14).
	// The disagreement does not survive: end of session is io.EOF, a zero-byte
	// read with a nil error means nothing has arrived yet, and `term` treats
	// only the former as "close".
	Read(p []byte) (int, error)

	// Write is `write(data)`: send input to the device, io.Writer semantics.
	Write(p []byte) (int, error)

	// Resize is `resize(cols, rows)`: tell the device its new geometry.
	//
	// Columns first. The order is Python's and it is the one axis-swap bug
	// this port cannot afford, which is why `term.Winsize` is spelled the
	// same way round.
	Resize(cols, rows uint16) error

	// Close ends the session and releases the transport. It is safe to call
	// more than once — Python's `_closed` flag exists for exactly that, and
	// `TerminalRunner` closes from several arms of its cleanup.
	Close() error
}
