// This file is `terminal/KubernetesWSTerminal.py` and
// `terminal/session/KubernetesWSTerminalSession.py`, reduced to the transport.

package kubernetes

import (
	"context"
	"io"
	"sync"

	"k8s.io/client-go/tools/remotecommand"

	"github.com/KatharaFramework/kathara-go/kathara"
)

// ttySession is `KubernetesWSTerminalSession`
// (`terminal/session/KubernetesWSTerminalSession.py:9`): an interactive exec on
// a running pod.
type ttySession struct {
	// stdinWriter is what [ttySession.Write] fills and the transport drains.
	stdinWriter *io.PipeWriter
	// stdoutReader is what the transport fills and [ttySession.Read] drains.
	stdoutReader *io.PipeReader

	// sizes is the `write_channel(RESIZE_CHANNEL, json.dumps({"Height": rows,
	// "Width": cols}))` of `KubernetesWSTerminalSession.resize`, in the shape
	// client-go wants. It is buffered by one and coalescing: a resize that
	// arrives while an earlier one is still queued REPLACES it, because the
	// terminal only ever cares about its current size and a blocking send here
	// would stall the caller's SIGWINCH handler.
	sizes chan remotecommand.TerminalSize

	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

var _ kathara.TTYSession = (*ttySession)(nil)

// newTTYSession opens the exec and starts pumping it.
func newTTYSession(ctx context.Context, factory executorFactory, req execRequest) (*ttySession, error) {
	executor, err := factory.NewExecutor(req)
	if err != nil {
		return nil, err
	}

	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	streamCtx, cancel := context.WithCancel(ctx)
	s := &ttySession{
		stdinWriter:  stdinWriter,
		stdoutReader: stdoutReader,
		sizes:        make(chan remotecommand.TerminalSize, 1),
		cancel:       cancel,
		done:         make(chan struct{}),
	}

	go func() {
		defer close(s.done)
		err := executor.StreamWithContext(streamCtx, remotecommand.StreamOptions{
			Stdin:             stdinReader,
			Stdout:            stdoutWriter,
			Tty:               true,
			TerminalSizeQueue: s,
		})
		// Closing the read side with the transport's error is what turns the end
		// of the remote shell into an io.EOF from [ttySession.Read]: a nil error
		// closes the pipe with io.EOF, anything else propagates.
		_ = stdoutWriter.CloseWithError(err)
		_ = stdinReader.CloseWithError(err)
	}()

	return s, nil
}

// Read is `read(n)` (`KubernetesWSTerminalSession.py:21`).
func (s *ttySession) Read(p []byte) (int, error) {
	return s.stdoutReader.Read(p)
}

// Write is `write(data)` (`KubernetesWSTerminalSession.py:53`).
func (s *ttySession) Write(p []byte) (int, error) {
	return s.stdinWriter.Write(p)
}

// Resize is `resize(cols, rows)` (`KubernetesWSTerminalSession.py:70`), whose
// payload is `{"Height": rows, "Width": cols}` — client-go builds the same
// frame from [remotecommand.TerminalSize].
func (s *ttySession) Resize(cols, rows uint16) error {
	size := remotecommand.TerminalSize{Width: cols, Height: rows}
	for {
		select {
		case s.sizes <- size:
			return nil
		case <-s.done:
			return nil
		default:
		}
		select {
		case <-s.sizes:
		default:
		}
	}
}

// Next is [remotecommand.TerminalSizeQueue]: the transport calls it in a loop
// and sends a resize frame for every value it gets. A nil return ends the
// resize stream, which is what the closed session produces.
func (s *ttySession) Next() *remotecommand.TerminalSize {
	select {
	case size := <-s.sizes:
		return &size
	case <-s.done:
		return nil
	}
}

// Close is `close()` (`KubernetesWSTerminalSession.py:89`), whose `_closed`
// flag exists precisely so that it can be called more than once —
// `TerminalRunner` closes from several arms of its cleanup.
func (s *ttySession) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		_ = s.stdinWriter.Close()
		_ = s.stdoutReader.Close()
	})
	return nil
}
