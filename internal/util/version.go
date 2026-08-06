// This file is the port of Kathara/version.py, plus utils.py's
// `parse_docker_engine_version` — the sanitiser that feeds it.

package util

import (
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// CurrentVersion is version.CURRENT_VERSION (version.py:3).
//
// `cmd/kathara` overrides the value it prints through an ldflags-injected
// variable (PACKAGE_GRAPH.md §2.1); this constant is the source-of-truth
// fallback and the value the tests compare the oracle against.
const CurrentVersion = "3.8.3"

// ParseVersion is version.parse (version.py:6): `tuple(int(x) for x in
// version.split('.'))`.
//
// Every component goes through [PyInt], so the tolerated syntax is CPython's
// and not a version grammar: " 3 . 8 " parses to (3, 8), "007.008" to (7, 8),
// "3_0.8" to (30, 8) and "-3.8" to (-3, 8). Anything CPython's `int()` rejects
// — "", "3.8.3-beta", "v3.8.3" — is a ValueError there and an error carrying
// the `Value` code here.
//
// The empty string is worth naming because [ParseDockerEngineVersion] returns
// it for a version that starts with a non-digit, and Python then crashes
// handing that empty string to `int`.
func ParseVersion(version string) ([]int, error) {
	parts := strings.Split(version, ".")

	parsed := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := PyInt(part)
		if err != nil {
			failure := PyIntFailure(err, part)
			return nil, kerrors.WrapValue(failure, failure.Error())
		}
		parsed = append(parsed, n)
	}

	return parsed, nil
}

// LessThan is version.less_than (version.py:10): parse both, then compare as
// Python compares tuples.
//
// The tuple rule is the point of not reaching for a semver library. Comparison
// is element-wise and the first difference decides; when one side runs out
// first and everything before matched, the *shorter* tuple is the smaller one.
// So "3.8" < "3.8.1" is true and "0" < "0.0" is true, which no semver
// implementation would agree with.
//
// Python parses `version` before `other_version`, so a pair where both are
// malformed reports the first one's failure.
func LessThan(version, otherVersion string) (bool, error) {
	left, err := ParseVersion(version)
	if err != nil {
		return false, err
	}
	right, err := ParseVersion(otherVersion)
	if err != nil {
		return false, err
	}

	for i := 0; i < len(left) && i < len(right); i++ {
		if left[i] != right[i] {
			return left[i] < right[i], nil
		}
	}

	return len(left) < len(right), nil
}

// ParseDockerEngineVersion is utils.parse_docker_engine_version (utils.py:479),
// which turns what the Docker daemon reports ("20.10.14+azure-1") into
// something a version comparison can read ("20.10.14").
//
// The character loop is copied rather than replaced by a regexp because two of
// its behaviours are surprising and both are observable:
//
//   - A part contributes its *leading* digit run and the loop keeps going, so
//     "20.10-beta.3" is not truncated at the beta — part 2 yields "10", part 3
//     yields "3", and the result is "20.10.3", a version that was never
//     released.
//   - A part with no leading digit yields nothing and stops the loop entirely,
//     so "v20.10.14" and " 1.2" both return "", which is then a hard ValueError
//     inside [ParseVersion].
//
// "Digit" is Python's `str.isdigit()` ([PyIsDigit]), which is wider than
// `int()` accepts: the superscript in "1².2" is kept here and rejected there.
func ParseDockerEngineVersion(v string) string {
	var parts []string

	for _, part := range strings.Split(v, ".") {
		var numericPart strings.Builder
		for _, char := range part {
			if !PyIsDigit(char) {
				break
			}
			numericPart.WriteRune(char)
		}

		if numericPart.Len() == 0 {
			break
		}
		parts = append(parts, numericPart.String())
	}

	return strings.Join(parts, ".")
}
