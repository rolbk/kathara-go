// This file is `foundation/manager/exec_stream/IExecStream.py`, the handle a
// streaming `exec` hands back: pull chunks until the stream ends, then ask for
// the exit code.

package kathara

import "context"

// ExecStream is the output of `exec(..., stream=True)`
// (`foundation/manager/exec_stream/IExecStream.py`), implemented by
// `backend/docker/execstream.go` and `backend/kubernetes/execstream.go`.
type ExecStream interface {
	// Next is `stream_next`: the next frame of output, as a pair of
	// per-stream chunks.
	// Python signals the end by raising StopIteration; Go returns io.EOF,
	// and a caller must test for it with errors.Is rather than assuming any
	// other error means "keep going".
	// Either side may be empty, and a frame with both sides empty is legal.

	Next(ctx context.Context) (stdout, stderr []byte, err error)

	ExitCode(ctx context.Context) (int, error)

	// Close releases the transport — the hijacked HTTP connection on Docker,
	// the SPDY stream on Kubernetes — and is safe to call more than once and
	// before the stream is exhausted.
	// It has no Python counterpart: CPython's refcounting closed the socket
	// when the generator went out of scope. Go has no such moment, so an
	// abandoned stream without a Close leaks a connection for the life of the
	// process.
	Close() error
}
