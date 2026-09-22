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
