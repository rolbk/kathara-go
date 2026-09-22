// This file holds the CPython `int(str)` primitive that `version.parse` and
// `parse_docker_engine_version` are built on. It is separate from version.go
// because the semantics belong to the interpreter, not to Kathará: the RULINGS
// CPython-compatible parsing is required wherever Python dispatches on these
// `int()` or `str.isdigit()` is on a user-reachable path.

package util

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"
)

// ErrPyIntSyntax is the "not a number" signal of [PyInt]: the exact condition
// under which CPython's `int(s)` raises ValueError. Callers that must render
// the Python message use [PyIntError], which quotes the offending literal.
var ErrPyIntSyntax = errors.New("invalid literal for int() with base 10")

// ErrPyIntRange is returned when the literal parses but does not fit a Go int.
var ErrPyIntRange = errors.New("integer literal out of range for int")

// PyIntFailure renders the ValueError CPython raises for the literal s, message
// included, so a caller can surface Python's own text. cause must be the error
// [PyInt] returned; it stays reachable through errors.Is.
func PyIntFailure(cause error, s string) error {
	return fmt.Errorf("%w: %s", cause, PythonRepr(s))
}

// intSpace reports whether r is one of the code points CPython's `int()`
// tolerates around a literal.
func intSpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0D, r == 0x20, r == 0x85, r == 0xA0,
		r == 0x1680, r >= 0x2000 && r <= 0x200A,
		r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000:
		return true
	}
	return false
}

// ndZeros lists the code point of every Unicode decimal-digit-zero, i.e. the
// first element of each `Nd` run of ten. CPython's `int()` accepts any `Nd`
// code point and reads its decimal value, so `int("٣")` is 3.
var ndZeros = [...]rune{
	0x0030, 0x0660, 0x06F0, 0x07C0, 0x0966, 0x09E6, 0x0A66, 0x0AE6,
	0x0B66, 0x0BE6, 0x0C66, 0x0CE6, 0x0D66, 0x0DE6, 0x0E50, 0x0ED0,
	0x0F20, 0x1040, 0x1090, 0x17E0, 0x1810, 0x1946, 0x19D0, 0x1A80,
	0x1A90, 0x1B50, 0x1BB0, 0x1C40, 0x1C50, 0xA620, 0xA8D0, 0xA900,
	0xA9D0, 0xA9F0, 0xAA50, 0xABF0, 0xFF10, 0x104A0, 0x10D30, 0x11066,
	0x110F0, 0x11136, 0x111D0, 0x112F0, 0x11450, 0x114D0, 0x11650, 0x116C0,
	0x11730, 0x118E0, 0x11950, 0x11C50, 0x11D50, 0x11DA0, 0x11F50, 0x16A60,
	0x16AC0, 0x16B50, 0x1D7CE, 0x1D7D8, 0x1D7E2, 0x1D7EC, 0x1D7F6, 0x1E140,
	0x1E2F0, 0x1E4F0, 0x1E950, 0x1FBF0,
}

// decimalValue is CPython's `Py_UNICODE_TODECIMAL`: the value 0-9 of a Unicode
// decimal digit, and false for everything else.
func decimalValue(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	for _, zero := range ndZeros {
		if r >= zero && r < zero+10 {
			return int(r - zero), true
		}
	}
	return 0, false
}

// nonDecimalDigits are the code points that Python's `str.isdigit()` accepts
// and `int()` does not: `Numeric_Type=Digit` characters outside `Nd`, i.e. the
// superscripts, the circled and parenthesised digits and their kin. The
// distinction is load-bearing exactly once, in
// [ParseDockerEngineVersion], which keeps a character because `isdigit()` says
// yes and then hands the result to a parse that says no.
var nonDecimalDigits = [...][2]rune{
	{0x00B2, 0x00B3}, {0x00B9, 0x00B9}, {0x1369, 0x1371}, {0x19DA, 0x19DA},
	{0x2070, 0x2070}, {0x2074, 0x2079}, {0x2080, 0x2089}, {0x2460, 0x2468},
	{0x2474, 0x247C}, {0x2488, 0x2490}, {0x24EA, 0x24EA}, {0x24F5, 0x24FD},
	{0x24FF, 0x24FF}, {0x2776, 0x277E}, {0x2780, 0x2788}, {0x278A, 0x2792},
	{0x10A40, 0x10A43}, {0x10E60, 0x10E68}, {0x11052, 0x1105A}, {0x1F100, 0x1F10A},
}

// PyIsDigit is Python's `str.isdigit()` for a single rune: every decimal digit
// plus the `Numeric_Type=Digit` characters that are not decimal.
func PyIsDigit(r rune) bool {
	if _, ok := decimalValue(r); ok {
		return true
	}
	for _, rg := range nonDecimalDigits {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// PyInt is CPython's `int(s)` with base 10.
func PyInt(s string) (int, error) {
	trimmed := strings.TrimFunc(s, intSpace)

	rest := trimmed
	negative := false
	if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
		negative = rest[0] == '-'
		rest = rest[1:]
	}
	if rest == "" {
		return 0, ErrPyIntSyntax
	}

	value := 0
	digits := 0
	prevUnderscore := false
	for _, r := range rest {
		if r == '_' {
			// An underscore needs a digit on its left and, checked after the
			// loop, a digit on its right.
			if digits == 0 || prevUnderscore {
				return 0, ErrPyIntSyntax
			}
			prevUnderscore = true
			continue
		}

		d, ok := decimalValue(r)
		if !ok {
			return 0, ErrPyIntSyntax
		}
		prevUnderscore = false
		digits++

		if value > (math.MaxInt-d)/10 {
			return 0, ErrPyIntRange
		}
		value = value*10 + d
	}
	if digits == 0 || prevUnderscore {
		return 0, ErrPyIntSyntax
	}

	if negative {
		return -value, nil
	}
	return value, nil
}

// PythonRepr renders s the way CPython's `repr()` would, which is how a string
// appears inside the ValueError text of a failed `int()` and therefore inside
// a message the CLI prints.
func PythonRepr(s string) string {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}

	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == rune(quote) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString("\\t")
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r < 0x20 || r == 0x7F:
			writeHexEscape(&b, `\x`, r, 2)
		case r < 0x7F, unicode.IsPrint(r):
			b.WriteRune(r)
		case r < 0x100:
			writeHexEscape(&b, `\x`, r, 2)
		case r < 0x10000:
			writeHexEscape(&b, `\u`, r, 4)
		default:
			writeHexEscape(&b, `\U`, r, 8)
		}
	}
	b.WriteByte(quote)

	return b.String()
}

// PythonStrListRepr is `str(['a', 'b'])` for a list of strings: the elements'
// [PythonRepr], comma-space separated, in square brackets. An empty slice —
// and a nil one — renders `[]`, as `str([])` does.
func PythonStrListRepr(items []string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, PythonRepr(item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// writeHexEscape emits prefix followed by width lower-case hex digits of r.
func writeHexEscape(b *strings.Builder, prefix string, r rune, width int) {
	b.WriteString(prefix)
	for shift := (width - 1) * 4; shift >= 0; shift -= 4 {
		b.WriteByte(hexDigits[(r>>shift)&0xF])
	}
}

const hexDigits = "0123456789abcdef"
