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
type Param struct {
	// Name is the Python keyword, e.g. "lab_hash".
	Name string
	// Present is Python's `value is not None`.
	Present bool
}

// paramNames is `', '.join(kwargs.keys())`: every declared name, whether or not
// it was supplied, in call order.
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
func CheckRequiredSingleNotNoneVar(params ...Param) error {
	switch n := countPresent(params); {
	case n == 0:
		return kerrors.NewOneParameter(paramNames(params))
	case n > 1:
		return kerrors.NewOnlyOneParameter(paramNames(params))
	}
	return nil
}

var ErrNoMatch = errors.New("util: no match")

// ReSearchFail is utils.re_search_fail (utils.py:85): `re.search`, raising when
// nothing matched instead of returning a null match. It returns the submatches
// of the first match, group 0 first.
func ReSearchFail(expression *regexp.Regexp, line string) ([]string, error) {
	matches := expression.FindStringSubmatch(line)
	if matches == nil {
		return nil, ErrNoMatch
	}
	return matches, nil
}

// ParseCDMACAddress is utils.parse_cd_mac_address (utils.py:459), the parser
// for a `--eth` value of the form "cd" or "cd/mac".
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
