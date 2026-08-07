package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/docker/docker/api/types"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// frame builds one multiplexed chunk the way the daemon writes it: an 8-byte
// header carrying the stream id and a big-endian length, then the payload.
func frame(stream byte, payload string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	return append(header, payload...)
}

// hijacked wraps a byte stream in the `types.HijackedResponse` the SDK's
// `ContainerExecAttach` returns.
//
// The connection half is a real (closed) pipe end rather than nil, so that
// Close does what the production path does.
func hijacked(t *testing.T, wire []byte) types.HijackedResponse {
	t.Helper()

	local, remote := net.Pipe()
	t.Cleanup(func() { _ = local.Close(); _ = remote.Close() })

	return types.HijackedResponse{
		Conn:   local,
		Reader: bufio.NewReader(bytes.NewReader(wire)),
	}
}

// TestExecFramesDemux is docker-py's `frames_iter_no_tty` + `demux_adaptor`:
// stream id 1 is stdout, 2 is stderr, and the OTHER side of the pair is nil.
//
// Nil is Python's `None`, and JSON_CLI_CONTRACT.md §4.2 already makes it
// indistinguishable from an empty chunk downstream — an event is emitted only
// for a non-empty side.
func TestExecFramesDemux(t *testing.T) {
	wire := bytes.Join([][]byte{
		frame(1, "out1"),
		frame(2, "err1"),
		frame(1, "out2"),
	}, nil)

	frames := newExecFrames(hijacked(t, wire), false)
	defer func() { _ = frames.Close() }()

	ctx := context.Background()

	stdout, stderr, err := frames.Next(ctx)
	if err != nil || string(stdout) != "out1" || stderr != nil {
		t.Fatalf("frame 1 = (%q, %q, %v)", stdout, stderr, err)
	}

	stdout, stderr, err = frames.Next(ctx)
	if err != nil || stdout != nil || string(stderr) != "err1" {
		t.Fatalf("frame 2 = (%q, %q, %v)", stdout, stderr, err)
	}

	stdout, _, err = frames.Next(ctx)
	if err != nil || string(stdout) != "out2" {
		t.Fatalf("frame 3 = (%q, %v)", stdout, err)
	}

	if _, _, err = frames.Next(ctx); !errors.Is(err, io.EOF) {
		t.Errorf("end of stream = %v, want io.EOF", err)
	}
}

// TestExecFramesUnknownStreamRaises is docker-py's `demux_adaptor` else arm
// (`utils/socket.py:186-187`), which is `raise ValueError(f'{stream_id} is not
// a valid stream')` — NOT a silent drop. Unreachable with a well-behaved
// daemon, since only ids 1 and 2 appear on exec output, but the message and the
// bucket are pinned so the claim cannot rot again.
func TestExecFramesUnknownStreamRaises(t *testing.T) {
	wire := append(frame(7, "junk"), frame(1, "real")...)

	frames := newExecFrames(hijacked(t, wire), false)
	defer func() { _ = frames.Close() }()

	_, _, err := frames.Next(context.Background())
	if err == nil {
		t.Fatal("unknown stream id was accepted; docker-py raises ValueError")
	}
	if err.Error() != "7 is not a valid stream" {
		t.Errorf("error = %q, want docker-py's own message", err)
	}
	if !errors.Is(err, kerrors.ErrValue) {
		t.Errorf("error %v is not in the Value bucket", err)
	}
}

// TestExecFramesEmptyFrameIsLegal: a zero-length payload is a real frame, not
// the end. [kathara.ExecStream.Next] says so explicitly.
func TestExecFramesEmptyFrameIsLegal(t *testing.T) {
	frames := newExecFrames(hijacked(t, frame(1, "")), false)
	defer func() { _ = frames.Close() }()

	stdout, stderr, err := frames.Next(context.Background())
	if err != nil {
		t.Fatalf("empty frame = %v, want no error", err)
	}
	if len(stdout) != 0 || stderr != nil {
		t.Errorf("empty frame = (%q, %q)", stdout, stderr)
	}
}

// TestExecFramesTruncatedStreamIsEOF: the daemon closes the connection when the
// exec finishes, so a half-read header or payload is the end of the stream —
// docker-py's `next_frame_header` reports a short read as StopIteration too.
func TestExecFramesTruncatedStreamIsEOF(t *testing.T) {
	// Four bytes of an eight-byte header.
	frames := newExecFrames(hijacked(t, []byte{1, 0, 0, 0}), false)
	defer func() { _ = frames.Close() }()

	if _, _, err := frames.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("truncated header = %v, want io.EOF", err)
	}
}

// TestExecFramesReadAll is the non-stream collection: every frame concatenated
// per side, which is what `exec_start(stream=False, demux=True)` returns.
func TestExecFramesReadAll(t *testing.T) {
	wire := bytes.Join([][]byte{
		frame(1, "a"), frame(2, "X"), frame(1, "b"), frame(2, "Y"),
	}, nil)

	frames := newExecFrames(hijacked(t, wire), false)
	defer func() { _ = frames.Close() }()

	stdout, stderr, err := frames.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(stdout) != "ab" || string(stderr) != "XY" {
		t.Errorf("ReadAll = (%q, %q), want (\"ab\", \"XY\")", stdout, stderr)
	}
}

// TestExecFramesReadAllNilSides: a side that produced nothing comes back nil,
// which is Python's `None` half of the demux tuple. `(None, None, 0)` is a
// legal result.
func TestExecFramesReadAllNilSides(t *testing.T) {
	frames := newExecFrames(hijacked(t, nil), false)
	defer func() { _ = frames.Close() }()

	stdout, stderr, err := frames.ReadAll(context.Background())
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if stdout != nil || stderr != nil {
		t.Errorf("ReadAll = (%q, %q), want two nils", stdout, stderr)
	}
}

// TestExecFramesTTYIsUnmultiplexed: with a tty the daemon writes the payload
// raw, so everything is stdout — docker-py's `frames_iter_tty` assumes the
// same. `DockerManager.exec` forces tty=False, so this is the `connect` path's
// shape rather than `exec`'s.
func TestExecFramesTTYIsUnmultiplexed(t *testing.T) {
	frames := newExecFrames(hijacked(t, []byte("raw output")), true)
	defer func() { _ = frames.Close() }()

	stdout, stderr, err := frames.Next(context.Background())
	if err != nil || string(stdout) != "raw output" || stderr != nil {
		t.Fatalf("tty frame = (%q, %q, %v)", stdout, stderr, err)
	}
}

// TestExecFramesHonoursCancellation is the SIGINT path of
// JSON_CLI_CONTRACT.md §6.2: a cancelled context aborts the read rather than
// waiting for a device that has stopped writing.
func TestExecFramesHonoursCancellation(t *testing.T) {
	frames := newExecFrames(hijacked(t, frame(1, "out")), false)
	defer func() { _ = frames.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := frames.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled read = %v, want context.Canceled", err)
	}
}

// TestExecFramesCloseIsIdempotent is what [kathara.ExecStream.Close] requires
// and what CPython's refcounting gave Python for free.
func TestExecFramesCloseIsIdempotent(t *testing.T) {
	frames := newExecFrames(hijacked(t, nil), false)
	for range 3 {
		if err := frames.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

// TestOCIRuntimeDetection is EXPECTATIONS-docker.md §1.7: runc's two English
// spellings of "no such binary", mapped to `MachineBinaryError` carrying the
// binary name — a class kathara-lab-checker catches by name (PORT_SPEC §4.3).
func TestOCIRuntimeDetection(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantBinary string
	}{
		{
			name:       "executable file not found",
			text:       `OCI runtime exec failed: exec failed: unable to start container process: exec: "nosuch": executable file not found in $PATH: unknown`,
			wantBinary: "nosuch",
		},
		{
			name:       "stat: no such file or directory",
			text:       `OCI runtime exec failed: exec failed: container_linux.go:380: starting container process caused: stat /bin/nosuch: no such file or directory: unknown`,
			wantBinary: "/bin/nosuch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ociBinaryErrorFromOutput([]byte(tt.text), "pc1")
			if err == nil {
				t.Fatal("the OCI failure was not recognised")
			}

			var typed *kerrors.BinaryError
			if !errors.As(err, &typed) {
				t.Fatalf("err = %v, want a BinaryError", err)
			}
			if typed.Binary != tt.wantBinary {
				t.Errorf("Binary = %q, want %q", typed.Binary, tt.wantBinary)
			}
			if typed.Machine != "pc1" {
				t.Errorf("Machine = %q", typed.Machine)
			}
		})
	}
}

// TestOCIRuntimeDetectionIgnoresOtherOutput: the scan runs over ordinary
// command output too, and must not turn a program's own words into a
// MachineBinaryError.
func TestOCIRuntimeDetectionIgnoresOtherOutput(t *testing.T) {
	for _, text := range []string{
		"",
		"bash: nosuch: command not found\n",
		"ls: cannot access '/x': No such file or directory\n",
	} {
		if err := ociBinaryErrorFromOutput([]byte(text), "pc1"); err != nil {
			t.Errorf("ociBinaryErrorFromOutput(%q) = %v, want nil", text, err)
		}
	}
}

// TestOCIRuntimeDetectionFromAPIError is the OTHER detection path
// (`DockerMachine.py:871-876`): the same pattern over the daemon's explanation
// rather than over collected output. It is the one that fires in STREAMING
// mode, where no output is ever collected.
func TestOCIRuntimeDetectionFromAPIError(t *testing.T) {
	err := daemonError(500, `OCI runtime exec failed: exec: "nosuch": executable file not found in $PATH: unknown`)

	binaryErr := ociBinaryError(err, "pc1")
	if binaryErr == nil {
		t.Fatal("the OCI failure was not recognised in the explanation")
	}
	var typed *kerrors.BinaryError
	if !errors.As(binaryErr, &typed) || typed.Binary != "nosuch" {
		t.Errorf("binaryErr = %v", binaryErr)
	}

	if ociBinaryError(daemonError(500, "something else"), "pc1") != nil {
		t.Error("an unrelated API error was translated")
	}
}
