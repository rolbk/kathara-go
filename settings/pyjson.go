// This file is the half of the frozen serialization that `encoding/json`
// cannot produce. Two differences make the standard encoder unusable for
// `kathara.conf`:
//
//   - CPython's `json.dumps` defaults to `ensure_ascii=True`, so every rune
//     above U+007E leaves as a `\uXXXX` escape (a surrogate pair above the
//     BMP), while `<`, `>` and `&` are written literally. Go's encoder does the
//     exact opposite on both counts.
//   - `json.dumps` writes a float through CPython's `repr`, whose fixed/
//     scientific switch and shortest-digit rules are its own. Go's `'g'` format
//     switches at a different exponent, so `last_checked` would come out as
//     `1.7859231240260758e+09` where Python writes `1785923124.0260758`.
//
// Both are observable in a file that existing installs read back, so both are
// reproduced here rather than approximated.

package settings

import (
	"math"
	"strconv"
)

// pyEscapeDict is `json.encoder.ESCAPE_DCT` for the characters that get a
// short escape rather than a `\uXXXX` one. Every other control character, and
// every rune outside ' '..'~', takes the numeric form.
var pyEscapeDict = map[rune]string{
	'\\': `\\`,
	'"':  `\"`,
	'\b': `\b`,
	'\f': `\f`,
	'\n': `\n`,
	'\r': `\r`,
	'\t': `\t`,
}

const hexDigits = "0123456789abcdef"

// appendPyJSONString appends `json.encoder.py_encode_basestring_ascii(s)`.
//
// The predicate is `ESCAPE_ASCII = re.compile(r'([\\"]|[^\ -~])')`: a rune is
// escaped when it is a backslash, a double quote, or outside the printable
// ASCII range U+0020-U+007E. U+007F (DEL) is outside that range and so is
// escaped; `<`, `>`, `&` and `/` are inside it and so are not.
//
// A rune above the BMP is written as the UTF-16 surrogate pair CPython emits,
// because `\uXXXX` cannot address it. Invalid UTF-8 in the Go string ranges as
// U+FFFD and is written as `�`; Python cannot hold such a string in the
// first place, and every value that reaches here has either come out of a JSON
// document or off the platform's own APIs.
func appendPyJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')

	for _, r := range s {
		if short, ok := pyEscapeDict[r]; ok {
			dst = append(dst, short...)
			continue
		}
		if r >= 0x20 && r <= 0x7e {
			dst = append(dst, byte(r))
			continue
		}
		if r > 0xffff {
			r -= 0x10000
			dst = appendUnicodeEscape(dst, 0xd800+(r>>10))
			dst = appendUnicodeEscape(dst, 0xdc00+(r&0x3ff))
			continue
		}
		dst = appendUnicodeEscape(dst, r)
	}

	return append(dst, '"')
}

// appendUnicodeEscape appends one `\uXXXX`, lower-case hex, as
// `'\\u{0:04x}'.format(...)` spells it.
func appendUnicodeEscape(dst []byte, r rune) []byte {
	return append(dst, '\\', 'u',
		hexDigits[(r>>12)&0xf], hexDigits[(r>>8)&0xf], hexDigits[(r>>4)&0xf], hexDigits[r&0xf])
}

// pyFloatRepr is CPython's `repr(float)`, which is what `json.dumps` calls for
// a float (`json.encoder.FLOAT_REPR`).
//
// The digits are the shortest decimal that round-trips, which Go's
// `strconv.FormatFloat(..., -1, 64)` produces from the same algorithm family
// and to the same result. What differs is the presentation, and CPython's rule
// is a single comparison on `decpt`, the position of the decimal point
// relative to the first significant digit:
//
//	scientific  iff  decpt <= -4 or decpt > 16
//
// so 1e16 (decpt 17) is `1e+16` while 9007199254740992.0 (decpt 16) is written
// out in full, and 0.0001 (decpt -3) is written out while 1e-05 (decpt -4) is
// not. Go's `'g'` compares against the digit count instead and disagrees with
// both ends of that range.
//
// A value that lands in fixed notation always carries a `.0` when it has no
// fractional digits (`Py_DTSF_ADD_DOT_0`), which is why a whole-second
// `last_checked` reads `1785923124.0` and not `1785923124`.
//
// The non-finite spellings are `json.dumps`'s own — CPython emits the
// JavaScript literals `NaN`, `Infinity` and `-Infinity` rather than raising,
// unless `allow_nan=False`. Nothing in the port can *load* one back (Go's JSON
// scanner rejects all three), but [Settings.LastChecked] is an exported
// float64 that a caller can set to anything, and writing a malformed document
// would be worse than writing Python's.
func pyFloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}

	// 'e' with precision -1 gives the shortest round-tripping digits already
	// normalised to one digit before the point, which is the form CPython's
	// `_Py_dg_dtoa` hands to its formatter.
	sci := strconv.FormatFloat(f, 'e', -1, 64)

	sign := ""
	if sci[0] == '-' {
		sign, sci = "-", sci[1:]
	}

	mantissa, exponent, ok := cutByte(sci, 'e')
	if !ok {
		// Unreachable: 'e' formatting always emits an exponent. Returning the
		// raw form keeps the output valid JSON if it ever were.
		return sign + sci
	}

	digits := mantissa
	if head, tail, found := cutByte(mantissa, '.'); found {
		digits = head + tail
	}

	exp, err := strconv.Atoi(exponent)
	if err != nil {
		return sign + sci
	}
	decpt := exp + 1

	if decpt <= -4 || decpt > 16 {
		out := digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		return sign + out + "e" + formatExponent(exp)
	}

	switch {
	case decpt <= 0:
		return sign + "0." + zeros(-decpt) + digits
	case decpt >= len(digits):
		return sign + digits + zeros(decpt-len(digits)) + ".0"
	default:
		return sign + digits[:decpt] + "." + digits[decpt:]
	}
}

// formatExponent is the `%+03d`-shaped tail CPython writes: an explicit sign
// and at least two digits, widening past two when the exponent needs it
// (`5e-324`).
func formatExponent(exp int) string {
	sign := "+"
	if exp < 0 {
		sign, exp = "-", -exp
	}
	body := strconv.Itoa(exp)
	if len(body) < 2 {
		body = "0" + body
	}
	return sign + body
}

func zeros(n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = '0'
	}
	return string(out)
}

// cutByte is strings.Cut for a one-byte separator, kept local so pyFloatRepr
// reads as the digit-shuffling it is.
func cutByte(s string, sep byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
