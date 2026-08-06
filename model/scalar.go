package model

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// Kind is the Python type of a [Scalar].
type Kind uint8

// The Python types a meta, a general option or a global machine metadata value
// can hold. They are the closed set the model can produce or accept; anything
// else Python could technically store there is unreachable through this API.
const (
	// KindAbsent is "the key is not in the dict". It is not Python's None: a
	// stored None is not representable (DIVERGENCES.md), because every reader
	// in this subsystem tests presence, not nil-ness.
	KindAbsent Kind = iota
	// KindString is `str`, what every lab.conf value and every CLI flag is.
	KindString
	// KindBool is `bool`, what `strtobool` produces and what the API passes for
	// `privileged`, `bridged` and `ipv6`.
	KindBool
	// KindInt is `int`.
	KindInt
	// KindFloat is `float`.
	KindFloat
	// KindStrings is `list[str]`, which only `args` ever holds (argparse's
	// REMAINDER, VstartCommand.py:180).
	KindStrings
)

// Scalar is one Python `Any` the model stores without interpreting: a meta
// value, a general option, a global machine metadata entry.
//
// It exists because the values are genuinely dynamic in 3.8.3 and the dynamism
// is observable. `pc1[ipv6]=false` stores the *string* `"false"` while
// `update_meta({'ipv6': False})` stores the *bool*, and `is_ipv6_enabled`
// branches on `type(x) is bool` (DIVERGENCES.md 3, README SURPRISE 7).
// `bridged_iface` is worse: a string there makes `check()` raise a TypeError,
// an int makes it fill an interface slot (DIVERGENCES.md 1). Collapsing the two
// into a Go string or a Go int would erase a difference the port is required to
// keep.
//
// The zero value is [KindAbsent], i.e. "no such key", which is why [Meta] can
// use plain fields for the optional metas instead of pointers to them.
type Scalar struct {
	kind    Kind
	str     string
	boolean bool
	integer int64
	float   float64
	strings []string
}

// Str is a Python str value.
func Str(v string) Scalar { return Scalar{kind: KindString, str: v} }

// Bool is a Python bool value.
func Bool(v bool) Scalar { return Scalar{kind: KindBool, boolean: v} }

// Int is a Python int value.
func Int(v int64) Scalar { return Scalar{kind: KindInt, integer: v} }

// Float is a Python float value.
func Float(v float64) Scalar { return Scalar{kind: KindFloat, float: v} }

// Strings is a Python list[str] value.
func Strings(v []string) Scalar { return Scalar{kind: KindStrings, strings: v} }

// Kind reports which Python type the value has, or [KindAbsent].
func (s Scalar) Kind() Kind { return s.kind }

// IsSet reports whether the key is present at all.
func (s Scalar) IsSet() bool { return s.kind != KindAbsent }

// Value returns the underlying Go value — string, bool, int64, float64 or
// []string — or nil when absent. It is what a JSON encoder wants: the vector
// corpus records `"num_terms": "2"` as a string and `"privileged": true` as a
// bool, and the difference is the whole point (labfile/testdata/vectors).
func (s Scalar) Value() any {
	switch s.kind {
	case KindString:
		return s.str
	case KindBool:
		return s.boolean
	case KindInt:
		return s.integer
	case KindFloat:
		return s.float
	case KindStrings:
		return s.strings
	case KindAbsent:
		return nil
	}
	return nil
}

// AsString reports the value when it is a str, like a Go type assertion would.
func (s Scalar) AsString() (string, bool) { return s.str, s.kind == KindString }

// AsBool reports the value when it is a bool. It is Python's
// `type(x) is bool`, which `is_ipv6_enabled` tests before falling back to
// `strtobool` (`model/Machine.py:616`).
func (s Scalar) AsBool() (bool, bool) { return s.boolean, s.kind == KindBool }

// AsStrings reports the value when it is a list[str].
func (s Scalar) AsStrings() ([]string, bool) { return s.strings, s.kind == KindStrings }

// String is Python's `str(x)`, which `add_meta` applies before handing
// `privileged` and `bridged` to `strtobool` (`model/Machine.py:159,168`) —
// `str(True)` is `"True"`, which `strtobool` then folds and accepts.
//
// An absent value renders as the empty string; Python has no `str()` of a
// missing key, and no caller reaches this without checking [Scalar.IsSet].
func (s Scalar) String() string {
	switch s.kind {
	case KindString:
		return s.str
	case KindBool:
		if s.boolean {
			return "True"
		}
		return "False"
	case KindInt:
		return strconv.FormatInt(s.integer, 10)
	case KindFloat:
		return pyFloatRepr(s.float)
	case KindStrings:
		// `str(['a', 'b'])`, which is the list's repr.
		parts := make([]string, 0, len(s.strings))
		for _, v := range s.strings {
			parts = append(parts, util.PythonRepr(v))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case KindAbsent:
		return ""
	}
	return ""
}

// Truthy is Python's truth value.
//
// It is what `if memory:` in `get_mem` tests (so `""` means "no limit"), what
// `is_privileged`/`is_bridged` hand back to callers that use the result as a
// bool, and what `update_meta`'s `privileged`/`bridged` gates test.
func (s Scalar) Truthy() bool {
	switch s.kind {
	case KindString:
		return s.str != ""
	case KindBool:
		return s.boolean
	case KindInt:
		return s.integer != 0
	case KindFloat:
		return s.float != 0
	case KindStrings:
		return len(s.strings) > 0
	case KindAbsent:
		return false
	}
	return false
}

// pyTypeName is `type(x).__name__`, which appears verbatim in the
// AttributeError text Python produces when a non-string reaches `strtobool`
// (`'int' object has no attribute 'lower'`, oracle-verified).
func (s Scalar) pyTypeName() string {
	switch s.kind {
	case KindString:
		return "str"
	case KindBool:
		return "bool"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindStrings:
		return "list"
	case KindAbsent:
		return "NoneType"
	}
	return "NoneType"
}

// pyInt is Python's `int(x)` over a stored value, at Python's precision.
//
// The conversions differ per type and all of them are reachable: a str parses
// (`int(" 3 ")` is 3, `int("٣")` is 3, `int("2.5")` raises), a bool is 1 or 0,
// a float truncates toward zero (`int(2.9)` is 2) and raises for an infinity or
// a NaN, and a list is a TypeError.
//
// The error is one of: [util.ErrPyIntSyntax] (the ValueError every caller turns
// into its own MachineOptionError), errPyIntNaN, errPyIntInf or a
// [PyRuntimeError] for the TypeError.
func (s Scalar) pyInt() (*big.Int, error) {
	switch s.kind {
	case KindString:
		return pyBigInt(s.str)
	case KindBool:
		if s.boolean {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case KindInt:
		return big.NewInt(s.integer), nil
	case KindFloat:
		return floatToBigInt(s.float)
	case KindStrings:
		return nil, newTypeError("int() argument must be a string, a bytes-like object or a real number, not 'list'")
	case KindAbsent:
		return nil, newTypeError("int() argument must be a string, a bytes-like object or a real number, not 'NoneType'")
	}
	return nil, util.ErrPyIntSyntax
}

// pyFloat is Python's `float(x)` over a stored value.
func (s Scalar) pyFloat() (float64, error) {
	switch s.kind {
	case KindString:
		return pyFloat(s.str)
	case KindBool:
		if s.boolean {
			return 1, nil
		}
		return 0, nil
	case KindInt:
		return float64(s.integer), nil
	case KindFloat:
		return s.float, nil
	case KindStrings:
		return 0, newTypeError("float() argument must be a string or a real number, not 'list'")
	case KindAbsent:
		return 0, newTypeError("float() argument must be a string or a real number, not 'NoneType'")
	}
	return 0, errPyFloatSyntax
}

// floatToBigInt is `int(f)`: truncation toward zero, with Python's two
// failures. `int(float('nan'))` is a ValueError, which every caller here
// catches and reports as its own MachineOptionError; `int(float('inf'))` is an
// OverflowError, which nothing catches — oracle-verified on `get_cpu`.
func floatToBigInt(f float64) (*big.Int, error) {
	if math.IsNaN(f) {
		return nil, errPyIntNaN
	}
	if math.IsInf(f, 0) {
		return nil, errPyIntInf
	}
	truncated := math.Trunc(f)
	result, _ := big.NewFloat(truncated).Int(nil)
	return result, nil
}

// pyFloatRepr is `str(f)` for a float: Python's repr, i.e. the shortest decimal
// that round-trips, with a trailing ".0" on an integral value.
//
// The digits are Go's — both languages emit the shortest round-tripping decimal
// — but the CHOICE between fixed and exponent notation is not. CPython's
// `format_float_short` switches to the exponent form when the decimal point
// falls at position `<= -4` or `> 16`, i.e. for `|x| < 1e-4` and `|x| >= 1e16`;
// Go's `'g'` switches on the exponent reaching the precision, which for the
// shortest form means as early as `1e6`. So `str(1e6)` is `1000000.0` and
// `str(123456789012345.0)` keeps all its digits on the oracle, where `'g'`
// would have produced `1e+06` and `1.23456789012345e+14`.
func pyFloatRepr(f float64) string {
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	if math.IsNaN(f) {
		return "nan"
	}

	// The 'e' form carries the decimal exponent, which is `decpt - 1` in
	// CPython's terms: `1e16` is one digit at decpt 17.
	exponential := strconv.FormatFloat(f, 'e', -1, 64)
	decpt := 1
	if i := strings.IndexByte(exponential, 'e'); i >= 0 {
		exponent, err := strconv.Atoi(exponential[i+1:])
		if err != nil {
			return exponential
		}
		decpt = exponent + 1
	}
	if decpt <= -4 || decpt > 16 {
		return exponential
	}

	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
