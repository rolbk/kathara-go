// This file implements Kathara/version.py, plus utils.py's
// `parse_docker_engine_version` — the sanitiser that feeds it.

package util

import (
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// CurrentVersion is version.CURRENT_VERSION (version.py:3).
const CurrentVersion = "3.8.3"

// ParseVersion is version.parse (version.py:6): `tuple(int(x) for x in
// version.split('.'))`.
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
