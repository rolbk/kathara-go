// This file is `exec_stream/DockerExecStream.py` plus the two pieces of
// docker-py it stands on: `frames_iter_no_tty`, which parses the 8-byte
// multiplexing header the daemon puts in front of every chunk, and
// `demux_adaptor`, which turns a frame into the `(stdout|None, stderr|None)`
// pair Kathará hands out.

package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"regexp"
	"strconv"
	"sync"

	"github.com/docker/docker/api/types"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ociRuntimeRE is `OCI_RUNTIME_RE` (`DockerMachine.py:35`):
var ociRuntimeRE = regexp.MustCompile(
	`OCI runtime exec failed(.*?)(stat (.*): no such file or directory|exec: "(.*)": executable file not found)`)

// ociBinaryError is the `except APIError` arm of `_exec_run`
// (`DockerMachine.py:871-876`): a missing binary reported by the daemon.
// It returns nil when the explanation is some other API failure, which Python
// re-raises untouched.
func ociBinaryError(err error, machineName string) error {
	return ociBinaryErrorFromOutput([]byte(explanation(err)), machineName)
}

// ociBinaryErrorFromOutput is the second detection path
// (`DockerMachine.py:879-890`): the same pattern over the collected STDOUT of a
// finished, non-zero exec.
func ociBinaryErrorFromOutput(out []byte, machineName string) error {
	matches := ociRuntimeRE.FindSubmatch(out)
	if matches == nil {
		return nil
	}
	binary := string(matches[3])
	if binary == "" {
		binary = string(matches[4])
	}
	return kerrors.NewMachineBinary(binary, machineName)
}

// execFrames reads the daemon's exec output stream.
type execFrames struct {
	response types.HijackedResponse
	reader   *bufio.Reader
	tty      bool

	closeOnce sync.Once
}

func newExecFrames(response types.HijackedResponse, tty bool) *execFrames {
	return &execFrames{response: response, reader: response.Reader, tty: tty}
}

// Next is one turn of docker-py's `frames_iter` + `demux_adaptor`: the next
// frame, as a pair with the side it did not belong to left nil.
func (f *execFrames) Next(ctx context.Context) (stdout, stderr []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	stop := f.abortOnCancel(ctx)
	defer stop()

	if f.tty {
		chunk := make([]byte, 4096)
		n, err := f.reader.Read(chunk)
		if n > 0 {
			return chunk[:n], nil, nil
		}
		return nil, nil, translateReadError(ctx, err)
	}

	var header [8]byte
	if _, err := io.ReadFull(f.reader, header[:]); err != nil {
		return nil, nil, translateReadError(ctx, err)
	}
	size := binary.BigEndian.Uint32(header[4:])
	payload := make([]byte, size)
	if _, err := io.ReadFull(f.reader, payload); err != nil {
		return nil, nil, translateReadError(ctx, err)
	}

	// docker-py's `demux_adaptor` (`utils/socket.py:177-187`): stream id 1 is
	// stdout, 2 is stderr, and the other side of the pair is None. Its `else`
	// arm is `raise ValueError(f'{stream_id} is not a valid stream')` — there
	// is no silent drop — so an id the daemon should never send (0 is stdin)
	// fails the exec rather than vanishing from its output.
	switch header[0] {
	case 1:
		return payload, nil, nil
	case 2:
		return nil, payload, nil
	default:
		return nil, nil, kerrors.NewValue(strconv.Itoa(int(header[0])) + " is not a valid stream")
	}
}

// ReadAll is the non-stream collection: every frame, concatenated per side.
func (f *execFrames) ReadAll(ctx context.Context) (stdout, stderr []byte, err error) {
	for {
		out, errOut, err := f.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return stdout, stderr, nil
			}
			return nil, nil, err
		}
		stdout = append(stdout, out...)
		stderr = append(stderr, errOut...)
	}
}

// Close releases the hijacked connection. Safe to call more than once, which
// [kathara.ExecStream.Close] requires and which CPython's refcounting gave
// Python for free.
func (f *execFrames) Close() error {
	f.closeOnce.Do(func() { f.response.Close() })
	return nil
}

// abortOnCancel closes the connection when ctx is done, and returns a function
// that stops the watcher. The returned error from the interrupted read is then
// remapped to the context's by [translateReadError], so a cancelled exec
// reports cancellation rather than a torn connection.
func (f *execFrames) abortOnCancel(ctx context.Context) func() {
	if ctx.Done() == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = f.Close()
		case <-done:
		}
	}()
	return func() { close(done) }
}

// translateReadError maps the transport's end-of-stream and its
// cancellation-induced failures onto the two errors the interface names.
func translateReadError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err == nil {
		return io.EOF
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return io.EOF
	}
	return err
}

// execStream is `DockerExecStream` (`exec_stream/DockerExecStream.py:9`), the
// handle `exec(..., stream=True)` returns.
type execStream struct {
	manager *Manager
	frames  *execFrames
	// execID is `_stream_api_object`, the Docker exec id `exit_code` inspects.
	execID string
}

// Next is `stream_next` (`DockerExecStream.py:24`).
func (s *execStream) Next(ctx context.Context) (stdout, stderr []byte, err error) {
	return s.frames.Next(ctx)
}

// ExitCode is `exit_code` (`DockerExecStream.py:32`):
// `int(exec_inspect(id)['ExitCode'])`.
func (s *execStream) ExitCode(ctx context.Context) (int, error) {
	inspect, err := s.manager.api.ContainerExecInspect(ctx, s.execID)
	if err != nil {
		return 0, err
	}
	code := execExitCode(inspect)
	if code == nil {
		return 0, newPyTypeError("int() argument must be a string, a bytes-like object or a real number, not 'NoneType'")
	}
	return *code, nil
}

// Close releases the hijacked connection.
func (s *execStream) Close() error { return s.frames.Close() }
