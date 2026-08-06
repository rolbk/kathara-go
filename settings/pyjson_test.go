package settings

import (
	"math"
	"testing"
)

// TestPyFloatReprAgainstOracle replays 2,700+ doubles through CPython's
// `repr`, recorded from `json.dumps` (testdata/pyrepr.json). The corpus is a
// hand-picked set of boundary values — the fixed/scientific switch at decpt 16
// and -4, the subnormal floor, the 2^53 plateau — plus a seeded sweep of
// random bit patterns and a dense band around the epoch seconds `last_checked`
// actually holds.
func TestPyFloatReprAgainstOracle(t *testing.T) {
	type row struct {
		Bits uint64 `json:"bits"`
		Repr string `json:"repr"`
	}
	rows := loadJSONFixture[[]row](t, "pyrepr.json")
	if len(rows) < 2000 {
		t.Fatalf("fixture shrank: %d rows", len(rows))
	}

	for _, r := range rows {
		v := math.Float64frombits(r.Bits)
		if got := pyFloatRepr(v); got != r.Repr {
			t.Errorf("pyFloatRepr(%#016x) = %s, want %s", r.Bits, got, r.Repr)
		}
	}
}

// TestPyFloatReprBoundaries spells out the rule the corpus exercises, so that
// a failure names the branch rather than a bit pattern.
func TestPyFloatReprBoundaries(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0, "0.0"},
		{math.Copysign(0, -1), "-0.0"},
		{1, "1.0"},
		{1234, "1234.0"},
		{1e6, "1000000.0"},
		{0.1, "0.1"},
		{1e-4, "0.0001"},                         // decpt -3: fixed
		{1e-5, "1e-05"},                          // decpt -4: scientific
		{9007199254740992, "9007199254740992.0"}, // decpt 16: fixed
		{1e16, "1e+16"},                          // decpt 17: scientific
		{5e-324, "5e-324"},                       // exponent wider than two digits
		{1785923124.0260758, "1785923124.0260758"},
		{-1785923124.0260758, "-1785923124.0260758"},
		{math.MaxFloat64, "1.7976931348623157e+308"},
	} {
		if got := pyFloatRepr(tc.in); got != tc.want {
			t.Errorf("pyFloatRepr(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestPyFloatReprNonFinite pins the JavaScript literals `json.dumps` writes
// with its default allow_nan. Nothing can load one back — Go's scanner rejects
// all three — but LastChecked is an exported float64 and writing a malformed
// document would be worse.
func TestPyFloatReprNonFinite(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{math.NaN(), "NaN"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
	} {
		if got := pyFloatRepr(tc.in); got != tc.want {
			t.Errorf("pyFloatRepr(%v) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

// TestPyJSONStringAgainstOracle replays CPython's
// `json.dumps(s)` with its default ensure_ascii (testdata/pyescape.json): the
// short escapes, the `\uXXXX` form for everything outside ' '..'~', surrogate
// pairs above the BMP, and the characters Go's encoder escapes but CPython
// does not (`<`, `>`, `&`).
func TestPyJSONStringAgainstOracle(t *testing.T) {
	type row struct {
		In  string `json:"in"`
		Out string `json:"out"`
	}
	rows := loadJSONFixture[[]row](t, "pyescape.json")
	if len(rows) < 200 {
		t.Fatalf("fixture shrank: %d rows", len(rows))
	}

	for _, r := range rows {
		if got := string(appendPyJSONString(nil, r.In)); got != r.Out {
			t.Errorf("appendPyJSONString(%q) = %s, want %s", r.In, got, r.Out)
		}
	}
}

// TestPyJSONStringHTMLCharactersAreLiteral is the difference that would
// silently rewrite a user's file: Go's `encoding/json` escapes these three by
// default and CPython never does.
func TestPyJSONStringHTMLCharactersAreLiteral(t *testing.T) {
	got := string(appendPyJSONString(nil, `a<b>c&d`))
	if want := `"a<b>c&d"`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
