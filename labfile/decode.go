package labfile

import (
	"bytes"
	"strconv"
	"unicode/utf8"
)

// UnicodeDecodeError is Python's `UnicodeDecodeError`, which both file parsers
// return directly: every line is `mmap.readline().decode('utf-8')`
// (`LabParser.py:42,90`, `DepParser.py:50,70`). A decoding error therefore has
// no file name or line number (compatibility note 13 in the vector README).
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

	// Unreachable: the caller only asks about input utf8.Valid rejected.
	return 0, n, unexpectedEnd
}

// readLines splits a file the way repeated `mmap.readline()` does: on `\n`,
// keeping the terminator, with a final unterminated chunk kept as it is.
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
