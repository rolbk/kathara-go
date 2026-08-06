// This file holds the CPython primitives the model feeds user text into and
// then dispatches on: `str.strip()`, `str.isnumeric()`, arbitrary-precision
// `int()` and `float()`. `internal/util` owns the two that other packages also
// need ([util.PyInt], [util.StrToBool]); what is here is what only the meta
// accessors reach.
//
// RULINGS.md OQ-14a ("Replicate CPython") is what makes these binding: the
// accessors decide between a stored int and a stored string, between a
// MachineOptionError and a crash, on the exact acceptance sets below.

package model

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// pySpace reports whether r is whitespace for `str.strip()`, i.e. whether
// `str.isspace()` is true for it.
//
// [unicode.IsSpace] is that set minus the four separators 0x1C-0x1F, which
// Python does treat as whitespace (`'\x1c'.isspace()` is True). The difference
// is reachable: `Machine("\x1cpc1")` strips to `pc1` on the oracle, and with
// Go's predicate alone it would fail the device-name regex instead.
//
// It is deliberately NOT [util.PyInt]'s whitespace set, which is the one
// `int()` accepts and which excludes 0x1C-0x1F; the two Python functions
// disagree and both are used here.
func pySpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1C && r <= 0x1F)
}

// pyStrip is `str.strip()` with no argument.
func pyStrip(s string) string { return strings.TrimFunc(s, pySpace) }

// pyIsNumeric is `str.isnumeric()`: true when s is non-empty and every
// character carries a Unicode numeric value.
//
// It gates the sysctl int coercion (`model/Machine.py:182`), where being wrong
// in either direction is observable: `net.a.b=²` is numeric and then fails
// `int()` with an uncaught ValueError, while `net.a.b=+5` is not numeric and is
// stored as the string `"+5"` (both oracle-verified).
//
// RESIDUAL GAP, recorded in DIVERGENCES.md: Python's predicate is
// `Numeric_Type != None`, which also covers the CJK ideographic numerals
// (`'一'.isnumeric()` is True) and a handful of other Lo characters. Go's
// tables carry no Numeric_Type, so this is the categories Nd, Nl and No —
// every character Python calls numeric *except* those ideographs. The only
// input class affected is a sysctl value made entirely of them, which Python
// crashes on and this stores as a string.
func pyIsNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.In(r, unicode.Nd, unicode.Nl, unicode.No) {
			return false
		}
	}
	return true
}

// pyIntSpace is the whitespace `int()` skips around a literal. It mirrors
// util.intSpace, which is unexported; TestPyBigIntMatchesUtilPyInt sweeps the
// two against each other so they cannot drift.
func pyIntSpace(r rune) bool {
	switch {
	case r >= 0x09 && r <= 0x0D, r == 0x20, r == 0x85, r == 0xA0,
		r == 0x1680, r >= 0x2000 && r <= 0x200A,
		r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000:
		return true
	}
	return false
}

// pyBigInt is CPython's `int(s)` at CPython's precision.
//
// [util.PyInt] is the same function bounded to a Go int, and it is what decides
// "is this a number" everywhere the answer only has to be yes or no. This one
// exists for the three sites where the *value* can exceed a Go int and Python
// keeps every digit: `get_mem` renders it back into the memory string,
// `add_meta("ulimit", …)` renders it into the soft/hard error message
// (kerrors.NewOptionUlimitSoftHard takes it pre-rendered for this reason), and
// both compare it against -1.
//
// The accepted set is [util.PyInt]'s, restated: optional surrounding
// whitespace, one optional sign, one or more decimal digits from any Unicode
// script, PEP 515 underscores strictly between digits.
func pyBigInt(s string) (*big.Int, error) {
	trimmed := strings.TrimFunc(s, pyIntSpace)

	rest := trimmed
	negative := false
	if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
		negative = rest[0] == '-'
		rest = rest[1:]
	}
	if rest == "" {
		return nil, util.ErrPyIntSyntax
	}

	value := new(big.Int)
	ten := big.NewInt(10)
	digits := 0
	prevUnderscore := false
	for _, r := range rest {
		if r == '_' {
			if digits == 0 || prevUnderscore {
				return nil, util.ErrPyIntSyntax
			}
			prevUnderscore = true
			continue
		}

		d, ok := pyDecimalValue(r)
		if !ok {
			return nil, util.ErrPyIntSyntax
		}
		prevUnderscore = false
		digits++

		value.Mul(value, ten)
		value.Add(value, big.NewInt(int64(d)))
	}
	if digits == 0 || prevUnderscore {
		return nil, util.ErrPyIntSyntax
	}

	if negative {
		value.Neg(value)
	}
	return value, nil
}

// pyDecimalValue is the 0-9 value of a Unicode decimal digit.
//
// It asks [util.PyInt] rather than carrying a second copy of the Nd-zero table:
// a one-rune literal is exactly a digit test, and reusing the frozen table is
// what keeps `int("٣")` == 3 true in both packages at once.
func pyDecimalValue(r rune) (int, bool) {
	if r >= '0' && r <= '9' {
		return int(r - '0'), true
	}
	v, err := util.PyInt(string(r))
	if err != nil || v < 0 || v > 9 {
		return 0, false
	}
	return v, true
}

// pyFloat is CPython's `float(s)`.
//
// [strconv.ParseFloat] is not it, in both directions: Go accepts the hex form
// `0x1p2` that Python rejects, and Python accepts Unicode decimal digits
// (`float("٣.٥")` is 3.5) and PEP 515 underscores that Go's parser does not
// take in a decimal literal. Both differences reach `get_cpu`, whose failure
// mode is a MachineOptionError the user sees.
//
// Accepted: surrounding whitespace ([pyIntSpace]'s set), one optional sign,
// then either an infinity/NaN word (`inf`, `infinity`, `nan`, any case) or a
// decimal literal — digits with at most one dot and an optional `e` exponent,
// with underscores allowed strictly between digits.
func pyFloat(s string) (float64, error) {
	trimmed := strings.TrimFunc(s, pyIntSpace)

	rest := trimmed
	sign := ""
	if len(rest) > 0 && (rest[0] == '+' || rest[0] == '-') {
		if rest[0] == '-' {
			sign = "-"
		}
		rest = rest[1:]
	}
	if rest == "" {
		return 0, errPyFloatSyntax
	}

	switch strings.ToLower(rest) {
	case "inf", "infinity":
		if sign == "-" {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case "nan":
		return math.NaN(), nil
	}

	// Rewrite the literal into the ASCII form ParseFloat understands: Unicode
	// digits become their ASCII twins and the PEP 515 underscores go away,
	// after the "digit on both sides" rule they must satisfy has been checked.
	var b strings.Builder
	b.WriteString(sign)
	prevDigit := false
	prevUnderscore := false
	for _, r := range rest {
		switch {
		case r == '_':
			if !prevDigit || prevUnderscore {
				return 0, errPyFloatSyntax
			}
			prevUnderscore = true
			continue
		case r == '.' || r == 'e' || r == 'E' || r == '+' || r == '-':
			if prevUnderscore {
				return 0, errPyFloatSyntax
			}
			prevDigit = false
			b.WriteRune(r)
		default:
			d, ok := pyDecimalValue(r)
			if !ok {
				return 0, errPyFloatSyntax
			}
			prevDigit = true
			prevUnderscore = false
			b.WriteByte(byte('0' + d))
		}
	}
	if prevUnderscore {
		return 0, errPyFloatSyntax
	}

	f, err := strconv.ParseFloat(b.String(), 64)
	if err != nil {
		// ParseFloat's range error yields ±Inf, which is what Python's float()
		// does for an overflowing decimal literal too; only a syntax failure is
		// a Python ValueError.
		if errors.Is(err, strconv.ErrRange) {
			return f, nil
		}
		return 0, errPyFloatSyntax
	}
	return f, nil
}

// errPyFloatSyntax is the ValueError `float()` raises. Every call site replaces
// it with its own MachineOptionError, so the text is never user-visible.
var errPyFloatSyntax = errors.New("model: could not convert string to float")

// saturateInt64 is the one place Python's arbitrary-precision integers are
// narrowed.
//
// Ports, ulimits and sysctl values are stored as fixed-width integers because
// that is what a container runtime accepts; a literal wider than an int64 keeps
// Python's "no error here" behaviour and clamps. DIVERGENCES.md records it. The
// messages that quote such a number (the ulimit soft/hard variant) render the
// big value, not the clamped one.
func saturateInt64(v *big.Int) int64 {
	if v.IsInt64() {
		return v.Int64()
	}
	if v.Sign() < 0 {
		return math.MinInt64
	}
	return math.MaxInt64
}

// saturateInt is [saturateInt64] for the port numbers, which spec §4.1 keeps as
// a plain int.
func saturateInt(v *big.Int) int {
	n := saturateInt64(v)
	if n > math.MaxInt {
		return math.MaxInt
	}
	if n < math.MinInt {
		return math.MinInt
	}
	return int(n)
}
