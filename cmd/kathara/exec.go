// This file is `cli/command/ExecCommand.py` (CLI_SURFACE.md §10) — the one
// command whose success exit code is not 0, and the only `jsonl` producer in
// 1.0 (JSON_CLI_CONTRACT.md §4).

package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
)

type execFlags struct {
	directory string
	vmachine  bool
	labHash   string
	labName   string
	noStdout  bool
	noStderr  bool
	wait      bool
}

func newExecCmd(a *app) *commandSpec {
	cmd := newParser("exec")
	f := &execFlags{}
	flags := cmd.Flags()
	flags.StringVarP(&f.directory, "directory", "d", "",
		"Specify the folder containing the network scenario.")
	cmd.meta("directory", "DIRECTORY")
	flags.BoolVarP(&f.vmachine, "vmachine", "v", false,
		"The device has been started with vstart command.")
	registerLabRef(cmd, &f.labHash, &f.labName)
	cmd.exclusiveGroup("directory", "vmachine", "lab-hash", "lab-name")
	flags.BoolVar(&f.noStdout, "no-stdout", false, "Disable stdout of the executed command.")
	flags.BoolVar(&f.noStderr, "no-stderr", false, "Disable stderr of the executed command.")
	flags.BoolVar(&f.wait, "wait", false, "Wait until startup commands execution finishes.")
	registerFormat(cmd, true)
	cmd.pos("DEVICE_NAME", nargsOne, "Name of the device to execute the command into.")
	cmd.pos("COMMAND", nargsOneOrMore, "Shell command that will be executed inside the device.")

	return &commandSpec{
		Name:      "exec",
		Cmd:       cmd,
		Streaming: true,
		Run: func(ctx context.Context, a *app, positional, _ []string) (int, error) {
			return runExec(ctx, a, f, positional)
		},
	}
}

// runExec is `ExecCommand.run`.
func runExec(ctx context.Context, a *app, f *execFlags, positional []string) (int, error) {
	machineName, words := positional[0], positional[1:]

	ref, err := a.execLabRef(f.directory, f.vmachine, f.labHash, f.labName)
	if err != nil {
		return 1, err
	}

	// `args['command'] if len(args['command']) > 1 else args['command'].pop()`:
	// one token becomes a bare string, which the backend then shlex-splits, so
	// `kathara exec pc1 "ls -la"` really does run two words (OQ-7b).
	command := kathara.NewCommand(words...)
	if len(words) == 1 {
		command = kathara.NewShellCommand(words[0])
	}

	wait := kathara.NoWait()
	if f.wait {
		wait = kathara.WaitForever()
	}

	mgr, err := a.manager(ctx)
	if err != nil {
		return 1, err
	}
	stream, err := mgr.ExecStream(ctx, machineName, command, ref, wait)
	if err != nil {
		return 1, err
	}
	defer func() { _ = stream.Close() }()

	var stdout, stderr strings.Builder
	outDecoder, errDecoder := &utf8Decoder{}, &utf8Decoder{}

	for {
		outChunk, errChunk, err := stream.Next(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 1, err
		}

		if len(outChunk) > 0 && !f.noStdout {
			text := outDecoder.decode(outChunk)
			switch a.console.Format {
			case cliout.FormatJSON:
				stdout.WriteString(text)
			case cliout.FormatJSONL:
				a.console.EmitStreamChunk("stdout", text)
			default:
				a.console.WriteOut([]byte(text))
			}
		}
		if len(errChunk) > 0 && !f.noStderr {
			text := errDecoder.decode(errChunk)
			switch a.console.Format {
			case cliout.FormatJSON:
				stderr.WriteString(text)
			case cliout.FormatJSONL:
				a.console.EmitStreamChunk("stderr", text)
			default:
				a.console.WriteErr([]byte(text))
			}
		}
	}

	// Whatever the decoder is still holding at the end of the stream is an
	// incomplete sequence that will never be completed; it becomes U+FFFD now.
	if tail := outDecoder.flush(); tail != "" && !f.noStdout {
		switch a.console.Format {
		case cliout.FormatJSON:
			stdout.WriteString(tail)
		case cliout.FormatJSONL:
			a.console.EmitStreamChunk("stdout", tail)
		default:
			a.console.WriteOut([]byte(tail))
		}
	}
	if tail := errDecoder.flush(); tail != "" && !f.noStderr {
		switch a.console.Format {
		case cliout.FormatJSON:
			stderr.WriteString(tail)
		case cliout.FormatJSONL:
			a.console.EmitStreamChunk("stderr", tail)
		default:
			a.console.WriteErr([]byte(tail))
		}
	}

	code, err := stream.ExitCode(ctx)
	if err != nil {
		return 1, err
	}

	switch a.console.Format {
	case cliout.FormatJSON:
		a.console.Emit(cliout.ExecResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code})
	case cliout.FormatJSONL:
		a.console.EmitStreamExit(code)
	}

	// The process exits with the remote command's code, in every mode
	// (JSON_CLI_CONTRACT.md A4).
	return code, nil
}

// execLabRef is the scenario addressing shared by `exec` and `connect`:
// `-v/--vmachine` selects `kathara_vlab`, `--lab-hash`/`--lab-name` name a
// deployment directly, and otherwise the directory is parsed with the
// swallow-everything fallback.
func (a *app) execLabRef(directory string, vmachine bool, labHash, labName string) (kathara.LabRef, error) {
	if vmachine {
		return kathara.LabRef{Name: vlabName}, nil
	}
	if labHash != "" {
		return kathara.LabRef{Hash: labHash}, nil
	}
	if labName != "" {
		return kathara.LabRef{Name: labName}, nil
	}
	lab, err := a.resolveRunningLab(directory, "", "")
	if err != nil {
		return kathara.LabRef{}, err
	}
	return kathara.LabRef{Hash: lab.Hash}, nil
}

// utf8Decoder decodes a byte stream that arrives in arbitrary chunks as UTF-8,
// replacing invalid sequences with U+FFFD and carrying an incomplete trailing
// sequence over to the next chunk.
//
// It replaces Python's per-chunk `chardet.detect` + `bytes.decode`, a pinned
// divergence: `chardet` returns `{'encoding': None}` for output it cannot
// classify — short binary output, most obviously — and `bytes.decode(None)`
// then raises `TypeError`, so `kathara exec pc1 cat /bin/true` crashes
// (`ExecCommand.py:103-110`, `cli.md` gotcha 5). JSON_CLI_CONTRACT.md §3.6
// and §4.2 replace it with UTF-8-plus-replacement, and the carry-over is what
// keeps a multi-byte character that straddles a chunk boundary from becoming
// two replacement characters.
type utf8Decoder struct{ pending []byte }

// The three answers [utf8Step] gives.
const (
	// seqValid is a complete, well-formed sequence.
	seqValid = iota
	// seqInvalid is a maximal subpart that can never be completed: one
	// U+FFFD, and decoding resumes at the byte after it.
	seqInvalid
	// seqIncomplete is a well-formed *prefix* that ran out of buffer.
	seqIncomplete
)

// utf8Step classifies the sequence at the head of b, returning how many bytes
// it covers.
//
// This is CPython's `unicode_decode_utf8` error handling, i.e. the Unicode
// "maximal subpart" recommendation that `bytes.decode('utf-8', 'replace')`
// implements: an ill-formed sequence costs **one** U+FFFD for the longest
// prefix that could still have been the start of a valid one, not one per byte.
// Oracle-measured on CPython 3.13: `b'\xe2\x82'` → `'�'` (one),
// `b'\xe2\x82A'` → `'�A'`, `b'\xf0\x9f\x98'` → `'�'`, while
// `b'\xff\xff'` → two, because neither byte can begin anything.
func utf8Step(b []byte) (size, status int) {
	cont := func(c byte) bool { return c&0xC0 == 0x80 }
	c := b[0]
	switch {
	case c < 0x80:
		return 1, seqValid
	case c < 0xC2:
		// A continuation byte with no lead, or one of the two overlong
		// two-byte leads: never the start of anything.
		return 1, seqInvalid
	case c < 0xE0:
		if len(b) < 2 {
			return 1, seqIncomplete
		}
		if !cont(b[1]) {
			return 1, seqInvalid
		}
		return 2, seqValid
	case c < 0xF0:
		lo, hi := byte(0x80), byte(0xBF)
		if c == 0xE0 {
			lo = 0xA0 // no overlong three-byte forms
		}
		if c == 0xED {
			hi = 0x9F // no surrogates
		}
		if len(b) < 2 {
			return 1, seqIncomplete
		}
		if b[1] < lo || b[1] > hi {
			return 1, seqInvalid
		}
		if len(b) < 3 {
			return 2, seqIncomplete
		}
		if !cont(b[2]) {
			return 2, seqInvalid
		}
		return 3, seqValid
	case c < 0xF5:
		lo, hi := byte(0x80), byte(0xBF)
		if c == 0xF0 {
			lo = 0x90 // no overlong four-byte forms
		}
		if c == 0xF4 {
			hi = 0x8F // nothing above U+10FFFF
		}
		if len(b) < 2 {
			return 1, seqIncomplete
		}
		if b[1] < lo || b[1] > hi {
			return 1, seqInvalid
		}
		if len(b) < 3 {
			return 2, seqIncomplete
		}
		if !cont(b[2]) {
			return 2, seqInvalid
		}
		if len(b) < 4 {
			return 3, seqIncomplete
		}
		if !cont(b[3]) {
			return 3, seqInvalid
		}
		return 4, seqValid
	}
	return 1, seqInvalid
}

// decode turns one chunk into text, holding back a trailing well-formed prefix
// so that a rune split across two chunks is not mistaken for an error.
func (d *utf8Decoder) decode(chunk []byte) string {
	buf := chunk
	if len(d.pending) > 0 {
		buf = append(d.pending, chunk...)
		d.pending = nil
	}

	var b strings.Builder
	b.Grow(len(buf))
	for i := 0; i < len(buf); {
		size, status := utf8Step(buf[i:])
		switch status {
		case seqValid:
			b.Write(buf[i : i+size])
		case seqInvalid:
			b.WriteRune(utf8.RuneError)
		default:
			// The rest of this sequence is in the next chunk. What is held
			// back is by construction exactly one incomplete sequence.
			d.pending = append(d.pending, buf[i:]...)
			return b.String()
		}
		i += size
	}
	return b.String()
}

// flush turns whatever was held back into its replacement character. An
// incomplete sequence at the end of the stream is one maximal subpart, so it is
// one U+FFFD however many bytes it holds.
func (d *utf8Decoder) flush() string {
	if len(d.pending) == 0 {
		return ""
	}
	d.pending = nil
	return string(utf8.RuneError)
}
