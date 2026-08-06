// This file covers the two argument validators of utils.py
// (`check_single_not_none_var`, `check_required_single_not_none_var`), plus
// `re_search_fail` and `parse_cd_mac_address` — the three small helpers whose
// failure text the API surface exposes.

package util

import (
	"errors"
	"regexp"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// Param is one keyword argument of a Python `check_*_not_none_var` call: the
// parameter's name and whether the caller supplied it.
//
// Python receives `**kwargs` and counts the values that `is not None`, so
// Present must be spelled the same way at the call site: it is "the caller
// passed something", not "the value is useful". A zero-length lab name and an
// empty Lab both count as present, and both then fail the truthiness dispatch
// further down — NILABILITY.tsv records that asymmetry, and mapping Present
// onto emptiness instead of onto nil-ness would quietly repair it.
type Param struct {
	// Name is the Python keyword, e.g. "lab_hash".
	Name string
	// Present is Python's `value is not None`.
	Present bool
}

// paramNames is `', '.join(kwargs.keys())`: every declared name, whether or not
// it was supplied, in call order.
//
// The order is the argument order at the call site, which PEP 468 makes
// deterministic in Python and which ORDERING.tsv therefore pins for the Go
// message (utils.py:114). It is why this takes a slice and not a map.
func paramNames(params []Param) []string {
	names := make([]string, 0, len(params))
	for _, p := range params {
		names = append(names, p.Name)
	}
	return names
}

func countPresent(params []Param) int {
	n := 0
	for _, p := range params {
		if p.Present {
			n++
		}
	}
	return n
}

// CheckSingleNotNoneVar is utils.check_single_not_none_var (utils.py:110): at
// most one of the parameters may be supplied. None supplied is fine — the
// callers that require one use [CheckRequiredSingleNotNoneVar].
func CheckSingleNotNoneVar(params ...Param) error {
	if countPresent(params) > 1 {
		return kerrors.NewOnlyOneParameter(paramNames(params))
	}
	return nil
}

// CheckRequiredSingleNotNoneVar is utils.check_required_single_not_none_var
// (utils.py:117): exactly one of the parameters must be supplied.
//
// The two failures carry different text — "You must specify a parameter among
// …" for none, "You must specify only a parameter among …" for more than one —
// and Python tests the "none" case first.
func CheckRequiredSingleNotNoneVar(params ...Param) error {
	switch n := countPresent(params); {
	case n == 0:
		return kerrors.NewOneParameter(paramNames(params))
	case n > 1:
		return kerrors.NewOnlyOneParameter(paramNames(params))
	}
	return nil
}

// ErrNoMatch is what utils.re_search_fail raises: a bare, message-less
// `ValueError` (utils.py:89) that every call site catches and replaces with its
// own text. ERROR_CODES.md records it as internal-only, with no code of its
// own.
var ErrNoMatch = errors.New("util: no match")

// ReSearchFail is utils.re_search_fail (utils.py:85): `re.search`, raising when
// nothing matched instead of returning a null match. It returns the submatches
// of the first match, group 0 first.
//
// It takes a compiled expression rather than a pattern string because the
// caller, not this function, owns the translation from Python's regex dialect
// to RE2. One difference bites at the only two call sites (Setting.py:216 and
// :221): Python's `$` also matches immediately before a trailing newline, so
// `re.search(r"^[a-z]+$", "kathara\n")` succeeds. Go's `$` without `(?m)` is
// end-of-text only, so the equivalent Go pattern has to be spelled
// `^[a-z]+\n?$`.
func ReSearchFail(expression *regexp.Regexp, line string) ([]string, error) {
	matches := expression.FindStringSubmatch(line)
	if matches == nil {
		return nil, ErrNoMatch
	}
	return matches, nil
}

// ParseCDMACAddress is utils.parse_cd_mac_address (utils.py:459), the parser
// for a `--eth` value of the form "cd" or "cd/mac".
//
// With no "/" the whole value is the collision domain and there is no MAC (the
// returned string is empty, which Python spells None; an empty MAC cannot come
// out of the other branch, so the two are not confusable).
//
// With a "/", the value is split and the *empty segments are dropped* before
// the count is checked, which makes the accepted set irregular: "A//B" parses
// to ("A", "B"), while "A/" and "/A" and "A/B/C" are all syntax errors. The
// filter also means no segment is ever trimmed — "  A  /  B  " yields a
// collision domain with its spaces intact.
func ParseCDMACAddress(value string) (cdName string, macAddress string, err error) {
	if !strings.Contains(value, "/") {
		return value, "", nil
	}

	var parts []string
	for _, x := range strings.Split(value, "/") {
		if x != "" {
			parts = append(parts, x)
		}
	}

	if len(parts) != 2 {
		return "", "", kerrors.NewSyntaxInterfaceDefinition(value)
	}

	return parts[0], parts[1], nil
}
