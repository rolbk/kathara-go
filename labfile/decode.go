package labfile

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

// UnicodeDecodeError is Python's `UnicodeDecodeError`, which both file parsers
// let escape: every line is `mmap.readline().decode('utf-8')`
// (`LabParser.py:42,90`, `DepParser.py:50,70`) and nothing catches it, so a
// single stray byte anywhere in a lab.conf aborts the whole parse with no file
// name and no line number (README SURPRISE 13).
//
// Go strings are bytes and would have carried the invalid sequence happily, so
// the check is explicit — and so is the message, which CPython builds from the
// position and the reason its decoder stopped at.
//
// The class carries no ERROR_CODES.md row, so it reaches the CLI boundary as
// `InternalError` (§1.4). [UnicodeDecodeError.PythonClass] is what the Layer B
// vector runner compares against Python's `type(e).__name__`.
type UnicodeDecodeError struct {
	// Encoding is the codec name, always "utf-8" here.
	Encoding string
	// Data is the byte string that failed to decode — one line of the file.
	Data []byte
	// Start is the byte offset of the first byte of the bad sequence.
	Start int
	// End is the offset one past its last byte.
	End int
	// Reason is CPython's explanation: "invalid start byte", "invalid
	// continuation byte" or "unexpected end of data".
	Reason string
}

// Error renders `str(e)` exactly. CPython prints a single byte in full and a
// longer run as an inclusive position range.
func (e *UnicodeDecodeError) Error() string {
	if e.Start+1 == e.End {
		return "'" + e.Encoding + "' codec can't decode byte 0x" +
			hexByte(e.Data[e.Start]) + " in position " + strconv.Itoa(e.Start) +
			": " + e.Reason
	}
	return "'" + e.Encoding + "' codec can't decode bytes in position " +
		strconv.Itoa(e.Start) + "-" + strconv.Itoa(e.End-1) + ": " + e.Reason
}

// PythonClass names the Python exception class this reproduces.
func (e *UnicodeDecodeError) PythonClass() string { return "UnicodeDecodeError" }

// hexByte renders b as CPython's `%02x`.
func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0x0F]})
}

// decodeUTF8 is `bytes.decode('utf-8')`: the string, or the
// [UnicodeDecodeError] CPython would have raised.
//
// [utf8.Valid] and CPython's decoder accept exactly the same byte strings —
// both reject overlong forms, surrogate halves and anything above U+10FFFF — so
// the walk that locates the failure only runs once there is one.
func decodeUTF8(b []byte) (string, error) {
	if utf8.Valid(b) {
		return string(b), nil
	}
	start, end, reason := utf8Failure(b)
	return "", &UnicodeDecodeError{
		Encoding: "utf-8",
		Data:     bytes.Clone(b),
		Start:    start,
		End:      end,
		Reason:   reason,
	}
}

// isContinuation is CPython's `IS_CONTINUATION_BYTE`.
func isContinuation(b byte) bool { return b&0xC0 == 0x80 }

// utf8Failure locates the first decode failure the way CPython's
// `STRINGLIB(utf8_decode)` does, and classifies it the way
// `unicode_decode_utf8` does.
//
// The structure is CPython's, branch for branch, because the *span* it reports
// is not derivable from "the first invalid byte": a bad third byte of a 3-byte
// sequence is reported as a two-byte failure, a truncated sequence at the end of
// the input is reported as running to the end of the input, and the two
// surrogate/overlong guards (`\xE0\x80`, `\xED\xA0`, `\xF0\x80`, `\xF4\x90`)
// report a *continuation* failure rather than a bad start byte.
//
// It is only ever called on input [utf8.Valid] has already rejected, so the
// loop always returns from inside.
func utf8Failure(b []byte) (start, end int, reason string) {
	const (
		invalidStart        = "invalid start byte"
		invalidContinuation = "invalid continuation byte"
		unexpectedEnd       = "unexpected end of data"
	)

	n := len(b)
	for s := 0; s < n; {
		c := b[s]

		switch {
		case c < 0x80:
			s++

		case c < 0xE0:
			// \xC2\x80-\xDF\xBF -- U+0080-U+07FF. \x80-\xBF is a stray
			// continuation byte and \xC0-\xC1 is an overlong ASCII.
			if c < 0xC2 {
				return s, s + 1, invalidStart
			}
			if n-s < 2 {
				return s, n, unexpectedEnd
			}
			if !isContinuation(b[s+1]) {
				return s, s + 1, invalidContinuation
			}
			s += 2

		case c < 0xF0:
			// \xE0\xA0\x80-\xEF\xBF\xBF -- U+0800-U+FFFF.
			if n-s < 3 {
				if n-s < 2 {
					return s, n, unexpectedEnd
				}
				c2 := b[s+1]
				bad := !isContinuation(c2)
				if !bad {
					if c2 < 0xA0 {
						bad = c == 0xE0
					} else {
						bad = c == 0xED
					}
				}
				if bad {
					return s, s + 1, invalidContinuation
				}
				return s, n, unexpectedEnd
			}
			c2, c3 := b[s+1], b[s+2]
			switch {
			case !isContinuation(c2),
				c == 0xE0 && c2 < 0xA0,  // overlong
				c == 0xED && c2 >= 0xA0: // surrogate half
				return s, s + 1, invalidContinuation
			}
			if !isContinuation(c3) {
				return s, s + 2, invalidContinuation
			}
			s += 3

		case c < 0xF5:
			// \xF0\x90\x80\x80-\xF4\x8F\xBF\xBF -- U+10000-U+10FFFF.
			if n-s < 4 {
				if n-s < 2 {
					return s, n, unexpectedEnd
				}
				c2 := b[s+1]
				bad := !isContinuation(c2)
				if !bad {
					if c2 < 0x90 {
						bad = c == 0xF0
					} else {
						bad = c == 0xF4
					}
				}
				if bad {
					return s, s + 1, invalidContinuation
				}
				if n-s < 3 {
					return s, n, unexpectedEnd
				}
				if !isContinuation(b[s+2]) {
					return s, s + 2, invalidContinuation
				}
				return s, n, unexpectedEnd
			}
			c2, c3, c4 := b[s+1], b[s+2], b[s+3]
			switch {
			case !isContinuation(c2),
				c == 0xF0 && c2 < 0x90,  // overlong
				c == 0xF4 && c2 >= 0x90: // beyond U+10FFFF
				return s, s + 1, invalidContinuation
			}
			if !isContinuation(c3) {
				return s, s + 2, invalidContinuation
			}
			if !isContinuation(c4) {
				return s, s + 3, invalidContinuation
			}
			s += 4

		default:
			// \xF5-\xFF can start nothing.
			return s, s + 1, invalidStart
		}
	}

	// Unreachable: the caller only asks about input utf8.Valid rejected. Report
	// the whole string rather than panicking (PORT_SPEC §10).
	return 0, n, unexpectedEnd
}

// readLines splits a file the way repeated `mmap.readline()` does: on `\n`,
// keeping the terminator, with a final unterminated chunk kept as it is.
//
// No newline translation happens — Python maps the file rather than reading it
// through the text layer, so a CRLF file keeps its CR and a syntax-error
// message built from a raw line carries it (README SURPRISE 10).
func readLines(data []byte) [][]byte {
	var out [][]byte
	for len(data) > 0 {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			out = append(out, data)
			break
		}
		out = append(out, data[:i+1])
		data = data[i+1:]
	}
	return out
}
