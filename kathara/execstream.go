// This file is `foundation/manager/exec_stream/IExecStream.py`, the handle a
// streaming `exec` hands back: pull chunks until the stream ends, then ask for
// the exit code.

package kathara

import "context"

// ExecStream is the output of `exec(..., stream=True)`
// (`foundation/manager/exec_stream/IExecStream.py`), implemented by
// `backend/docker/execstream.go` and `backend/kubernetes/execstream.go`.
//
// It is the source of the `jsonl` envelope of JSON_CLI_CONTRACT.md §4: one
// [ExecStream.Next] call per frame, `stdout`/`stderr` events for the non-empty
// sides, then a single `{"type":"exit","code":N}` from [ExecStream.ExitCode].
type ExecStream interface {
	// Next is `stream_next`: the next frame of output, as a pair of
	// per-stream chunks.
	//
	// Python signals the end by raising StopIteration; Go returns io.EOF,
	// and a caller must test for it with errors.Is rather than assuming any
	// other error means "keep going".
	//
	// Either side may be empty, and a frame with both sides empty is legal.
	// The docker SDK's demux yields `None` for the stream a frame does not
	// belong to, the Python CLI writes "" for a falsy stdout and skips a
	// falsy stderr, so `None` and `b""` were already indistinguishable
	// downstream; JSON_CLI_CONTRACT.md §4.2 pins that as "an event only for a
	// non-empty side" and this interface therefore does not distinguish them
	// either. Chunk boundaries are a transport artefact: concatenate per
	// stream, never treat one Next as one line.
	//
	// The context is per call (PORT_SPEC §0.2 #11): cancelling it aborts the
	// read, which is how SIGINT turns into `{"type":"interrupted"}` without
	// waiting for a device that has stopped writing.
	Next(ctx context.Context) (stdout, stderr []byte, err error)

	// ExitCode is `exit_code`: the remote command's exit status, and the
	// process exit code of `kathara exec` (JSON_CLI_CONTRACT.md §3.6).
	//
	// It is meaningful only after [ExecStream.Next] has returned io.EOF.
	// Python reads it from a live `exec_inspect` and casts with `int(...)`,
	// which is `TypeError` while the command is still running because the
	// field is null until then (analysis/manager-foundation.md §7 gotcha 8).
	// Calling it early is therefore a caller error, and an implementation
	// reports it as one instead of guessing a code.
	//
	// The signature carries an error where PACKAGE_GRAPH.md §2.7 sketches a
	// bare `int`: the value comes from a live API round-trip that can fail on
	// its own, Python lets that failure raise, and a bare int would have to
	// swallow it or panic. DIVERGENCES.md §"From `kathara/`" records it.
	ExitCode(ctx context.Context) (int, error)

	// Close releases the transport — the hijacked HTTP connection on Docker,
	// the SPDY stream on Kubernetes — and is safe to call more than once and
	// before the stream is exhausted.
	//
	// It has no Python counterpart: CPython's refcounting closed the socket
	// when the generator went out of scope. Go has no such moment, so an
	// abandoned stream without a Close leaks a connection for the life of the
	// process.
	Close() error
}
