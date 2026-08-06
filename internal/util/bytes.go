// This file is utils.human_readable_bytes (utils.py:416), the size formatter
// whose output text is asserted by golden tests.

package util

import (
	"math"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// sizeName is the unit table of human_readable_bytes (utils.py:421), in the
// order Python indexes it.
var sizeName = [...]string{"B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}

// HumanReadableBytes is utils.human_readable_bytes (utils.py:416).
//
// Python:
//
//	i = int(math.floor(math.log(size_bytes, 1024)))
//	p = math.pow(1024, i)
//	s = round(size_bytes / p, 2)
//	return "%s %s" % (s, size_name[i])
//
// Three details make this more than a division.
//
// The exponent is chosen with floating-point arithmetic and is occasionally
// wrong, in a way that shows: `float(2**50 - 1) / float(2**50)` divides to
// exactly 5.0, so 1125899906842623 bytes are reported as "1.0 PB" rather than
// "1024.0 TB". The Go expression is spelled the same way, `Log(n)/Log(1024)`,
// so the same artefact appears; it was verified identical to CPython over half
// a million values including every 2**k neighbourhood.
//
// `round(x, 2)` is round-half-to-even on the exact binary value, which
// `strconv.FormatFloat(v, 'f', 2, 64)` also is — Go's fixed-precision
// formatting breaks ties to even too. Re-parsing puts the result back on the
// nearest double, which is what Python's `round` returns.
//
// `"%s" % float` is `str(float)`, the shortest decimal that round-trips, and it
// always keeps a fractional part: Python prints "1.0", Go's %v prints "1". The
// trailing ".0" is restored explicitly. The rounding can also carry across the
// unit boundary, so 1048571 bytes render as "1024.0 KB" and never as "1.0 MB".
//
// Zero short-circuits to "0 B" before the logarithm. A negative size is
// `math.log`'s domain error in Python, an uncaught ValueError; it is returned
// as an error here rather than allowed to become a NaN, and it is not reachable
// from any 3.8.3 call site (both feed byte counts from Docker or from a tar).
func HumanReadableBytes(sizeBytes int64) (string, error) {
	if sizeBytes == 0 {
		return "0 B", nil
	}
	if sizeBytes < 0 {
		return "", kerrors.NewValue("math domain error")
	}

	i := int(math.Floor(math.Log(float64(sizeBytes)) / math.Log(1024)))
	if i < 0 {
		i = 0
	}
	if i >= len(sizeName) {
		// Unreachable for an int64: 1024**9 needs 90 bits. Python would raise
		// IndexError here; clamping keeps a unit name on the value instead of
		// panicking on a slice bound.
		i = len(sizeName) - 1
	}

	p := math.Pow(1024, float64(i))
	s := pyRound2(float64(sizeBytes) / p)

	return pyFloatStr(s) + " " + sizeName[i], nil
}

// pyRound2 is Python's `round(x, 2)`: the correctly rounded two-decimal value,
// ties to even, put back on the nearest float64.
func pyRound2(x float64) float64 {
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(x, 'f', 2, 64), 64)
	if err != nil {
		// FormatFloat emits a finite decimal literal for every finite x, and
		// HumanReadableBytes only reaches this with a finite positive value.
		return x
	}
	return rounded
}

// pyFloatStr is Python's `str(float)` for the values human_readable_bytes
// produces, which are finite and inside [1, 1024]: the shortest round-tripping
// decimal, with a ".0" appended when it came out integral.
func pyFloatStr(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsRune(s, '.') {
		s += ".0"
	}
	return s
}
