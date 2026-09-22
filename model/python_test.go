package model

import (
	"errors"
	"math"
	"math/big"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// TestPyBigInt is CPython's `int(s)`, pinned against the oracle. Every row was
// produced by running `int(s)` on /root/kathara/pyvenv/bin/python.
func TestPyBigInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "0", want: "0"},
		{in: "10", want: "10"},
		{in: "-10", want: "-10"},
		{in: "+10", want: "10"},
		{in: " 12 ", want: "12"},
		{in: "\t7\n", want: "7"},
		// A no-break space is int() whitespace.
		{in: " 12 ", want: "12"},
		// PEP 515: underscores strictly between digits.
		{in: "1_0", want: "10"},
		// Unicode decimal digits of any script.
		{in: "٣", want: "3"},
		{in: "١٢", want: "12"},
		// Arbitrary precision, which is why this exists next to util.PyInt.
		{in: "99999999999999999999999999999999", want: "99999999999999999999999999999999"},
		{in: "-99999999999999999999999999999999", want: "-99999999999999999999999999999999"},

		{in: "1__0", wantErr: true},
		{in: "_1", wantErr: true},
		{in: "1_", wantErr: true},
		// isdigit-but-not-decimal characters are rejected, which makes the
		// sysctl `--5` exception path reachable.
		{in: "²", wantErr: true},
		{in: "½", wantErr: true},
		{in: "一", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
		{in: "-", wantErr: true},
		{in: "0x10", wantErr: true},
		{in: "12.5", wantErr: true},
		{in: "1e3", wantErr: true},
		// U+200B is not whitespace, and 0x1C is whitespace for strip() but NOT
		// for int() — the two Python predicates differ.
		{in: "\u200b1", wantErr: true},
		{in: "\x1c5", wantErr: true},
	}

	for _, tt := range tests {
		got, err := pyBigInt(tt.in)
		if tt.wantErr {
			if !errors.Is(err, util.ErrPyIntSyntax) {
				t.Errorf("pyBigInt(%q) = %v, %v; want a syntax error", tt.in, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("pyBigInt(%q): %v", tt.in, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("pyBigInt(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

// TestPyBigIntMatchesUtilPyInt keeps this package's arbitrary-precision scanner
// and [util.PyInt] from drifting: on every literal that fits a Go int the two
// must agree, and they must agree on which literals are syntax errors.
func TestPyBigIntMatchesUtilPyInt(t *testing.T) {
	t.Parallel()

	corpus := []string{
		"0", "10", "-10", "+10", " 12 ", "\t7\n", "1_0", "1__0", "_1", "1_", "٣", "١٢",
		"²", "½", "一", "abc", "", "-", "+", "0x10", "12.5", "1e3", "\u200b1", "007",
		"  -0  ", "1_0_1", "١٢٣٤٥", "　　9　",
	}
	// Every code point util.intSpace accepts, so a divergence in the table is a
	// test failure and not a lurking behaviour change.
	spaces := []rune{
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x20, 0x85, 0xA0, 0x1680,
		0x2000, 0x2005, 0x200A, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000,
	}
	for _, r := range spaces {
		corpus = append(corpus, string(r)+"5"+string(r))
	}
	// And a few that are NOT in it, which both must reject.
	for _, r := range []rune{0x1C, 0x1D, 0x1E, 0x1F, 0x200B, 0x2060} {
		corpus = append(corpus, string(r)+"5")
	}

	for _, s := range corpus {
		want, wantErr := util.PyInt(s)
		got, gotErr := pyBigInt(s)

		switch {
		case errors.Is(wantErr, util.ErrPyIntSyntax):
			if !errors.Is(gotErr, util.ErrPyIntSyntax) {
				t.Errorf("pyBigInt(%q) = %v, %v; util.PyInt says syntax error", s, got, gotErr)
			}
		case errors.Is(wantErr, util.ErrPyIntRange):
			if gotErr != nil {
				t.Errorf("pyBigInt(%q) = %v; want the value util.PyInt could not hold", s, gotErr)
			}
		default:
			if gotErr != nil || got.Cmp(big.NewInt(int64(want))) != 0 {
				t.Errorf("pyBigInt(%q) = %v, %v; util.PyInt says %d", s, got, gotErr, want)
			}
		}
	}
}

// TestPyFloat is CPython's `float(s)`, pinned against the oracle.
func TestPyFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{in: "0", want: 0},
		{in: "10", want: 10},
		{in: "-10", want: -10},
		{in: "+10", want: 10},
		{in: " 12 ", want: 12},
		{in: "\t7\n", want: 7},
		{in: "12.5", want: 12.5},
		{in: "1e3", want: 1000},
		{in: "1.5e2", want: 150},
		{in: ".5", want: 0.5},
		{in: "5.", want: 5},
		{in: "+.5", want: 0.5},
		{in: "1_0", want: 10},
		{in: "1_0.5", want: 10.5},
		{in: "٣", want: 3},
		{in: "٣.٥", want: 3.5},
		{in: "99999999999999999999999999999999", want: 1e32},

		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
		{in: "-", wantErr: true},
		{in: "1e", wantErr: true},
		{in: "0x1p2", wantErr: true},
		{in: "0x10", wantErr: true},
		{in: "1__0", wantErr: true},
		{in: "_1", wantErr: true},
		{in: "1_", wantErr: true},
		{in: "²", wantErr: true},
		{in: "\u200b1", wantErr: true},
	}

	for _, tt := range tests {
		got, err := pyFloat(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("pyFloat(%q) = %v, want an error", tt.in, got)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("pyFloat(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}

	// The infinity and NaN words, in every case Python accepts.
	for _, s := range []string{"inf", "Inf", "INF", "infinity", "Infinity"} {
		if got, err := pyFloat(s); err != nil || !math.IsInf(got, 1) {
			t.Errorf("pyFloat(%q) = %v, %v; want +Inf", s, got, err)
		}
	}
	if got, err := pyFloat("-Infinity"); err != nil || !math.IsInf(got, -1) {
		t.Errorf("pyFloat(-Infinity) = %v, %v; want -Inf", got, err)
	}
	for _, s := range []string{"nan", "NAN", "NaN"} {
		if got, err := pyFloat(s); err != nil || !math.IsNaN(got) {
			t.Errorf("pyFloat(%q) = %v, %v; want NaN", s, got, err)
		}
	}
}

// TestPyIsNumeric is `str.isnumeric()`, the gate of the sysctl int coercion.
// Rows are the oracle's; the CJK one is the documented residual gap.
func TestPyIsNumeric(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{in: "0", want: true},
		{in: "10", want: true},
		{in: "٣", want: true},
		{in: "٣٥", want: true},
		{in: "1٣", want: true},
		{in: "99999999999999999999999999999999", want: true},
		// Numeric but not decimal: isnumeric says yes, int() says no, so
		// `pc1[sysctl]=net.a.b=²` raises during conversion.
		{in: "²", want: true},
		{in: "³", want: true},
		{in: "½", want: true},
		{in: "¼", want: true},
		{in: "Ⅷ", want: true},

		{in: "", want: false},
		{in: "-10", want: false},
		{in: "+10", want: false},
		{in: " 12 ", want: false},
		{in: "1 ", want: false},
		{in: "1_0", want: false},
		{in: "abc", want: false},
		{in: "12.5", want: false},
		{in: "0x10", want: false},
	}

	for _, tt := range tests {
		if got := pyIsNumeric(tt.in); got != tt.want {
			t.Errorf("pyIsNumeric(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}

	if pyIsNumeric("一") {
		t.Error("pyIsNumeric(一) changed; update the compatibility expectation if this gap was closed")
	}
}

// TestPyStrip is `str.strip()`. The four separators 0x1C-0x1F are the reason it
// is not strings.TrimSpace: Python strips them, unicode.IsSpace does not know
// them.
func TestPyStrip(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{in: "  a  ", want: "a"},
		{in: "\x1ca\x1f", want: "a"},
		{in: " a ", want: "a"},
		{in: " a", want: "a"},
		{in: "\n\ta\r\v\f", want: "a"},
		// U+200B is not whitespace in Python either.
		{in: "a\u200b", want: "a\u200b"},
		{in: "", want: ""},
	}

	for _, tt := range tests {
		if got := pyStrip(tt.in); got != tt.want {
			t.Errorf("pyStrip(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestScalarString is Python's `str(x)`, which `add_meta` applies to the
// privileged and bridged values before folding them.
func TestScalarString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   Scalar
		want string
	}{
		{in: Str("x"), want: "x"},
		{in: Bool(true), want: "True"},
		{in: Bool(false), want: "False"},
		{in: Int(10), want: "10"},
		{in: Float(1.5), want: "1.5"},
		{in: Float(2), want: "2.0"},
		{in: Strings([]string{"a", "b"}), want: "['a', 'b']"},
		{in: Scalar{}, want: ""},
	}

	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("Scalar(%v).String() = %q, want %q", tt.in.Value(), got, tt.want)
		}
	}
}

// TestPyFloatRepr pins the fixed-vs-exponent switch of `str(float)`, which is
// CPython's (`decpt <= -4 || decpt > 16`) and NOT Go's `'g'` (which would break
// into exponent form at 1e6). Every row is `repr()` from the 3.13 oracle.
func TestPyFloatRepr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   float64
		want string
	}{
		{in: 0, want: "0.0"},
		{in: 1.5, want: "1.5"},
		{in: 2, want: "2.0"},
		{in: 999999, want: "999999.0"},
		{in: 1e6, want: "1000000.0"},
		{in: -1e6, want: "-1000000.0"},
		{in: 1234567, want: "1234567.0"},
		{in: 123456789012345, want: "123456789012345.0"},
		{in: 1e15, want: "1000000000000000.0"},
		{in: 1e16, want: "1e+16"},
		{in: 1e17, want: "1e+17"},
		{in: 1.5e300, want: "1.5e+300"},
		{in: 0.0001, want: "0.0001"},
		{in: 1e-5, want: "1e-05"},
		{in: 5e-324, want: "5e-324"},
		{in: 0.1, want: "0.1"},
		{in: math.Inf(1), want: "inf"},
		{in: math.Inf(-1), want: "-inf"},
		{in: math.NaN(), want: "nan"},
	}

	for _, tt := range tests {
		if got := pyFloatRepr(tt.in); got != tt.want {
			t.Errorf("pyFloatRepr(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestScalarTruthy is Python's truth value, which drives `if memory:` in
// get_mem and the `_mount_volumes` gate.
func TestScalarTruthy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   Scalar
		want bool
	}{
		{in: Scalar{}, want: false},
		{in: Str(""), want: false},
		{in: Str("0"), want: true},
		{in: Bool(false), want: false},
		{in: Bool(true), want: true},
		{in: Int(0), want: false},
		{in: Int(-1), want: true},
		{in: Float(0), want: false},
		{in: Float(0.5), want: true},
		{in: Strings(nil), want: false},
		{in: Strings([]string{"a"}), want: true},
	}

	for _, tt := range tests {
		if got := tt.in.Truthy(); got != tt.want {
			t.Errorf("Scalar(%#v).Truthy() = %v, want %v", tt.in.Value(), got, tt.want)
		}
	}
}

// TestScalarPyInt covers `int(x)` over the stored types: a bool is 1 or 0, a
// float truncates toward zero, an infinity is an OverflowError and a NaN a
// ValueError (oracle P5: `get_num_terms(True)` is 1 and `get_num_terms(2.9)` is
// 2).
func TestScalarPyInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   Scalar
		want int64
	}{
		{in: Str(" 3 "), want: 3},
		{in: Bool(true), want: 1},
		{in: Bool(false), want: 0},
		{in: Int(7), want: 7},
		{in: Float(2.9), want: 2},
		{in: Float(-2.9), want: -2},
	}

	for _, tt := range tests {
		got, err := tt.in.pyInt()
		if err != nil || got.Int64() != tt.want {
			t.Errorf("Scalar(%#v).pyInt() = %v, %v; want %d", tt.in.Value(), got, err, tt.want)
		}
	}

	if _, err := Float(math.Inf(1)).pyInt(); !errors.Is(err, errPyIntInf) {
		t.Errorf("int(inf) = %v, want the OverflowError", err)
	}
	if _, err := Float(math.NaN()).pyInt(); !errors.Is(err, errPyIntNaN) {
		t.Errorf("int(nan) = %v, want the ValueError", err)
	}
	if _, err := Strings([]string{"a"}).pyInt(); !errors.Is(err, ErrPyTypeError) {
		t.Errorf("int(list) = %v, want a TypeError", err)
	}
}

func TestSaturate(t *testing.T) {
	t.Parallel()

	huge, ok := new(big.Int).SetString("99999999999999999999999999", 10)
	if !ok {
		t.Fatal("SetString")
	}
	if got := saturateInt64(huge); got != math.MaxInt64 {
		t.Errorf("saturateInt64(huge) = %d, want MaxInt64", got)
	}
	if got := saturateInt64(new(big.Int).Neg(huge)); got != math.MinInt64 {
		t.Errorf("saturateInt64(-huge) = %d, want MinInt64", got)
	}
	if got := saturateInt64(big.NewInt(42)); got != 42 {
		t.Errorf("saturateInt64(42) = %d", got)
	}
	if got := saturateInt(big.NewInt(42)); got != 42 {
		t.Errorf("saturateInt(42) = %d", got)
	}
}

// TestOrderedMap pins the Python dict semantics the whole model rests on.
func TestOrderedMap(t *testing.T) {
	t.Parallel()

	m := NewOrderedMap[string, int]()
	if m.Len() != 0 || m.Has("a") {
		t.Error("a fresh map is not empty")
	}

	for _, k := range []string{"a", "b", "c"} {
		if prev, existed := m.Set(k, 1); existed || prev != 0 {
			t.Errorf("Set(%q) reported a previous value", k)
		}
	}
	// Re-assignment keeps the position and reports the old value.
	prev, existed := m.Set("a", 9)
	if !existed || prev != 1 {
		t.Errorf("Set(a) = %d, %v; want 1, true", prev, existed)
	}
	if got := m.Keys(); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("Keys() = %v, want [a b c]", got)
	}
	if got := m.Values(); len(got) != 3 || got[0] != 9 {
		t.Errorf("Values() = %v", got)
	}

	if !m.Delete("b") || m.Delete("b") {
		t.Error("Delete is wrong")
	}
	if got := m.Keys(); len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Errorf("Keys() after delete = %v", got)
	}

	// A nil map answers every read.
	var nilMap *OrderedMap[string, int]
	if nilMap.Len() != 0 || nilMap.Has("a") || nilMap.Keys() != nil || nilMap.Entries() != nil {
		t.Error("the nil map does not read as empty")
	}
}

// TestOrderedMapSortStable pins the stability apply_dependencies relies on.
func TestOrderedMapSortStable(t *testing.T) {
	t.Parallel()

	m := NewOrderedMap[string, int]()
	for _, k := range []string{"a", "b", "c", "d"} {
		m.Set(k, 0)
	}
	m.Set("b", 1)
	m.Set("d", 1)

	m.SortStableFunc(func(x, y Entry[string, int]) int { return x.Value - y.Value })
	if got := m.Keys(); len(got) != 4 || got[0] != "a" || got[1] != "c" || got[2] != "b" || got[3] != "d" {
		t.Errorf("Keys() = %v, want [a c b d]", got)
	}
}
