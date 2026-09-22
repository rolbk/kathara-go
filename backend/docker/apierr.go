// This file has no Python original. It is the adapter between two error
// vocabularies: `docker.errors.APIError`, which Python sniffs by
// `.response.status_code` and `.explanation`, and the Go SDK's errors, which
// carry the status inside `containerd/errdefs` and the daemon's message inside
// the error string.
// It also holds `docker.utils.version_lt` / `version_gte`, which are NOT
// `version.less_than` from `internal/util` — the two disagree, and the
// disagreement is reachable (see [versionLT]).

package docker

import (
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errhttp"
	"github.com/docker/docker/client"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// daemonPrefix is what `errors.Wrap(daemonErr, "Error response from daemon")`
// prepends in `client/request.go:273`. Python's `APIError.explanation` is the
// unwrapped message, and every sniff in `DockerMachine.py` and
// `DockerImage.py` is written against that, so the prefix comes off before any
// of them run.
const daemonPrefix = "Error response from daemon: "

// explanation is `APIError.explanation`: the `message` field of the daemon's
// JSON error body, with nothing around it.
func explanation(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimPrefix(err.Error(), daemonPrefix)
}

// statusCode is `APIError.response.status_code`, as far as the Go SDK
// preserves it.
func statusCode(err error) int {
	if err == nil {
		return 0
	}
	return errhttp.ToHTTP(err)
}

// isServerError is Python's `e.response.status_code == 500`.
func isServerError(err error) bool { return statusCode(err) == 500 }

// isNotFound is `except docker.errors.NotFound`, which docker-py raises for a
// 404 and the Go SDK reports through errdefs.
func isNotFound(err error) bool { return cerrdefs.IsNotFound(err) }

// isPluginInconsistentState is the pair of substrings `DockerMachine.start`
// (`:516-518`) and `DockerMachine.connect_interface` (`:422-425`) both test on
// a 500, and which they both translate to
// [kerrors.ErrInconsistentState].
func isPluginInconsistentState(err error) bool {
	if !isServerError(err) {
		return false
	}
	e := explanation(err)
	return strings.Contains(e, "network does not exist") || strings.Contains(e, "endpoint does not exist")
}

// isMountsDenied is `DockerMachine.start`'s `e.explanation.startswith('Mounts denied')`
// (`:514`) — a prefix test, not a substring one.
func isMountsDenied(err error) bool {
	return isServerError(err) && strings.HasPrefix(explanation(err), "Mounts denied")
}

// isDialTCP is `DockerImage._check_and_pull`'s `'dial tcp' in e.explanation`
// on a 500 (`:162`), the registry-unreachable signature.
func isDialTCP(err error) bool {
	return isServerError(err) && strings.Contains(explanation(err), "dial tcp")
}

// isConnectionFailed reports a transport-level failure to reach the daemon —
// the Go analogue of the `requests.exceptions.ConnectionError` that
// `check_docker_status` turns into `DockerDaemonConnectionError`
// (`DockerManager.py:49-52`).
func isConnectionFailed(err error) bool {
	return err != nil && client.IsErrConnectionFailed(err)
}

// ---------------------------------------------------------------------------
// docker.utils version comparison
// ---------------------------------------------------------------------------

// versionLT is `docker.utils.version_lt` (`docker/utils/utils.py`), used at
// `DockerMachine.py:279` for the `< 26.0.0` engine fork.
func versionLT(v1, v2 string) (bool, error) {
	cmp, err := compareVersion(v1, v2)
	if err != nil {
		return false, err
	}
	return cmp > 0, nil
}

// versionGTE is `docker.utils.version_gte`: `not version_lt(v1, v2)`. Used at
// `DockerMachine.py:297,460` for the `>= 27.0.0` fork.
func versionGTE(v1, v2 string) (bool, error) {
	lt, err := versionLT(v1, v2)
	if err != nil {
		return false, err
	}
	return !lt, nil
}

// compareVersion is docker-py's `compare_version`, sign convention included:
// it returns 1 when v1 is OLDER than v2, -1 when it is newer, 0 when equal.
// The inversion is docker-py's and is why [versionLT] tests `> 0`.
func compareVersion(v1, v2 string) (int, error) {
	if v1 == v2 {
		return 0, nil
	}

	left, err := versionTuple(v1)
	if err != nil {
		return 0, err
	}
	right, err := versionTuple(v2)
	if err != nil {
		return 0, err
	}

	n := max(len(left), len(right))
	for i := 0; i < n; i++ {
		c1, c2 := 0, 0
		if i < len(left) {
			c1 = left[i]
		}
		if i < len(right) {
			c2 = right[i]
		}
		switch {
		case c1 == c2:
			continue
		case c1 > c2:
			return -1, nil
		default:
			return 1, nil
		}
	}
	return 0, nil
}

// versionTuple is `tuple(int(p) for p in v.split('.'))`, with `int()` spelled
// as [util.PyInt] so that the accepted syntax is CPython's: " 3 " parses,
// "007" parses to 7, "" raises.
func versionTuple(v string) ([]int, error) {
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		n, err := util.PyInt(part)
		if err != nil {
			// Python's bare `ValueError` from `int()`.
			failure := util.PyIntFailure(err, part)
			return nil, kerrors.WrapValue(failure, failure.Error())
		}
		out = append(out, n)
	}
	return out, nil
}
