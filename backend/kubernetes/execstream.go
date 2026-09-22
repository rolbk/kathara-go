// This file is `exec_stream/KubernetesExecStream.py`, `_exec_stream` and
// `_exec_all` (`KubernetesMachine.py:848,879`), plus the SDK plumbing they sit
// on: the `stream(connect_get_namespaced_pod_exec, _preload_content=False)`
// call that opens a SPDY/WebSocket exec channel, and the `returncode` property
// that reads the exit status off the error channel once it closes.
// It also holds `OCI_RUNTIME_RE` (`KubernetesMachine.py:40`), which on this
// backend is the bare literal `OCI runtime exec failed` — no capture groups,
// unlike the Docker backend's. That is why `MachineBinaryError.binary` here is
// the whole command, quoted ([ShlexJoin]), rather than the missing executable:
// Megalos never learns which word was missing.

package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"regexp"
	"sync"

	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ociRuntimeRE is `OCI_RUNTIME_RE` (`KubernetesMachine.py:40`).
var ociRuntimeRE = regexp.MustCompile(`OCI runtime exec failed`)

// execRequest is the keyword set of
// `stream(self.core_client.connect_get_namespaced_pod_exec, …)`
// (`KubernetesMachine.py:826-835`). `stdout` is always true there and is not a
// field.
type execRequest struct {
	// Namespace is the scenario hash. Python passes `lab_hash`, not the pod's
	// own namespace — they are the same string for every pod this backend
	// finds, since the listing was filtered by it.
	Namespace string
	// Pod is `pod.metadata.name`.
	Pod string
	// Command is the already-split argv.
	Command []string
	Stdin   bool
	Stderr  bool
	TTY     bool
}

// executorFactory builds the transport for one exec. It is an interface so that
// a test can run every flow in this package without a cluster: the real
// implementation is [restExecutorFactory] and the fake one lives in the tests.
type executorFactory interface {
	NewExecutor(req execRequest) (remotecommand.Executor, error)
}

// execResult is what a completed non-streaming exec produced: the two output
// sides and the exit code, i.e. `_exec_all`'s tuple.
type execResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// runExecAll is `_exec_all` (`KubernetesMachine.py:879`): pump the exec to
// completion and answer everything it wrote plus its exit status.
func runExecAll(ctx context.Context, factory executorFactory, req execRequest, machineName string, stdinBuffer [][]byte) (execResult, error) {
	executor, err := factory.NewExecutor(req)
	if err != nil {
		return execResult{}, err
	}

	var stdout, stderr bytes.Buffer
	options := remotecommand.StreamOptions{Stdout: &stdout, Tty: req.TTY}
	if req.Stderr {
		options.Stderr = &stderr
	}
	if req.Stdin {
		options.Stdin = bytes.NewReader(bytes.Join(stdinBuffer, nil))
	}

	streamErr := executor.StreamWithContext(ctx, options)
	exitCode, err := execExitCode(ctx, streamErr, machineName, req.Command)
	if err != nil {
		return execResult{}, err
	}

	return execResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitCode}, nil
}

// execExitCode is the `try: exit_code = response.returncode / except ValueError`
// block (`KubernetesMachine.py:913-918`), shared by [runExecAll] and
// [execStream.ExitCode].
func execExitCode(ctx context.Context, streamErr error, machineName string, command []string) (int, error) {
	switch {
	case streamErr == nil:
		return 0, nil
	case ctx.Err() != nil:
		return 0, streamErr
	}

	var coded utilexec.CodeExitError
	if errors.As(streamErr, &coded) {
		return coded.Code, nil
	}

	if ociRuntimeRE.MatchString(streamErr.Error()) {
		return 0, kerrors.NewMachineBinary(ShlexJoin(command), machineName)
	}
	return 1, nil
}

// ---------------------------------------------------------------------------
// Streaming
// ---------------------------------------------------------------------------

// execFrame is one turn of `_exec_stream`'s generator: whatever arrived on each
// side since the last one. Either half may be nil, which is Python's `None`.
type execFrame struct {
	stdout []byte
	stderr []byte
}

// execStream is `KubernetesExecStream`
// (`exec_stream/KubernetesExecStream.py:6`), the handle
// `exec(..., is_stream=True)` returns.
type execStream struct {
	frames chan execFrame

	// done closes when the transport goroutine has finished; streamErr is
	// written before it closes and read only after.
	done      chan struct{}
	streamErr error

	machineName string
	command     []string

	cancel    context.CancelFunc
	closeOnce sync.Once
}

// newExecStream opens the exec and starts pumping it.
func newExecStream(ctx context.Context, factory executorFactory, req execRequest, machineName string, stdinBuffer [][]byte) (*execStream, error) {
	executor, err := factory.NewExecutor(req)
	if err != nil {
		return nil, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	s := &execStream{
		frames:      make(chan execFrame),
		done:        make(chan struct{}),
		machineName: machineName,
		command:     req.Command,
		cancel:      cancel,
	}

	options := remotecommand.StreamOptions{
		Stdout: &frameWriter{stream: s, stderr: false, ctx: streamCtx},
		Tty:    req.TTY,
	}
	if req.Stderr {
		options.Stderr = &frameWriter{stream: s, stderr: true, ctx: streamCtx}
	}
	if req.Stdin {
		options.Stdin = bytes.NewReader(bytes.Join(stdinBuffer, nil))
	}

	go func() {
		defer close(s.done)
		defer close(s.frames)
		s.streamErr = executor.StreamWithContext(streamCtx, options)
	}()

	return s, nil
}

// frameWriter is the [io.Writer] the transport writes one side into. It hands
// the bytes to [execStream.Next] and blocks until they are taken, which is what
// gives the caller back-pressure instead of an unbounded buffer.
type frameWriter struct {
	stream *execStream
	stderr bool
	ctx    context.Context
}

func (w *frameWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	// The transport reuses its buffer between writes, so the bytes have to be
	// copied before they cross the channel.
	chunk := make([]byte, len(p))
	copy(chunk, p)

	frame := execFrame{stdout: chunk}
	if w.stderr {
		frame = execFrame{stderr: chunk}
	}

	select {
	case w.stream.frames <- frame:
		return len(p), nil
	case <-w.ctx.Done():
		return 0, w.ctx.Err()
	}
}

// Next is `stream_next` (`KubernetesExecStream.py:14`): the next frame, io.EOF
// once the command has finished writing.
func (s *execStream) Next(ctx context.Context) (stdout, stderr []byte, err error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case frame, open := <-s.frames:
		if !open {
			return nil, nil, io.EOF
		}
		return frame.stdout, frame.stderr, nil
	}
}

// ExitCode is `exit_code` (`KubernetesExecStream.py:22`):
// `int(self._stream_api_object.returncode)`.
func (s *execStream) ExitCode(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-s.done:
	}
	return execExitCode(ctx, s.streamErr, s.machineName, s.command)
}

// Close aborts the transport and releases it. Safe to call more than once and
// before the stream is exhausted.
func (s *execStream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		// Drain so the transport goroutine's pending write, if any, unblocks
		// and the goroutine can exit.
		go func() {
			for range s.frames { //nolint:revive // draining
			}
		}()
	})
	return nil
}
