package docker

import (
	"errors"
	"fmt"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/containerd/errdefs/pkg/errhttp"
)

// daemonError builds the error VALUE the Go SDK produces for a non-2xx
// response, mirroring `client/errors.go`'s unexported `httpError` field for
// field: the message is the daemon's `message` behind one fixed prefix, and the
// status lives in a separate `errdef` field that only `errors.Is` reaches.
type daemonHTTPError struct{ err, errdef error }

func (e *daemonHTTPError) Error() string        { return e.err.Error() }
func (e *daemonHTTPError) Unwrap() error        { return e.err }
func (e *daemonHTTPError) Is(target error) bool { return errors.Is(e.errdef, target) }

func daemonError(status int, message string) error {
	return &daemonHTTPError{
		err:    fmt.Errorf("%s%s", daemonPrefix, message),
		errdef: errhttp.ToNative(status),
	}
}

// TestExplanation is `APIError.explanation`: the daemon's message with the
// SDK's wrapper prefix removed, which is the string every sniff in
// `DockerMachine.py` and `DockerImage.py` is written against.
func TestExplanation(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{
			"a daemon error loses its prefix",
			errors.New("Error response from daemon: network does not exist"),
			"network does not exist",
		},
		{
			"a local error is returned as-is",
			errors.New("dial unix /var/run/docker.sock: connect: no such file or directory"),
			"dial unix /var/run/docker.sock: connect: no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := explanation(tt.err); got != tt.want {
				t.Errorf("explanation = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStatusCodeRoundTrips measures exactly how much of
// `APIError.response.status_code` survives the Go SDK, because [statusCode]'s
// callers are only allowed to lean on the part that does.
func TestStatusCodeRoundTrips(t *testing.T) {
	for _, status := range []int{404, 409, 500} {
		if got := statusCode(daemonError(status, "x")); got != status {
			t.Errorf("statusCode(%d) = %d, want it preserved", status, got)
		}
	}
	for _, status := range []int{502, 510} {
		if got := statusCode(daemonError(status, "x")); got != 500 {
			t.Errorf("statusCode(%d) = %d, want the 500 the SDK collapses it to", status, got)
		}
	}
	if got := statusCode(errors.New("dial unix: no such file")); got != 500 {
		t.Errorf("statusCode(local error) = %d, want 500", got)
	}

	if !isNotFound(daemonError(404, "No such plugin")) {
		t.Error("a 404 is not reported as NotFound")
	}
	if isNotFound(daemonError(500, "boom")) {
		t.Error("a 500 is reported as NotFound")
	}
}

// TestPluginInconsistentStateSniff is the pair of substrings `start` and
// `connect_interface` both test on a 500 and translate to
// `kerrors.ErrInconsistentState`; anything else is re-raised untouched, which
// is `test_connect_interface_plugin_api_error`'s 510.
func TestPluginInconsistentStateSniff(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"500 network does not exist", daemonError(500, "network does not exist"), true},
		{"500 endpoint does not exist", daemonError(500, "endpoint does not exist"), true},
		{"500 with an unrelated message", daemonError(500, "something else went wrong"), false},
		{"404 with a matching message", daemonError(404, "network does not exist"), false},
		{"a local error with a matching message", errors.New("network does not exist"), true},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPluginInconsistentState(tt.err); got != tt.want {
				t.Errorf("isPluginInconsistentState = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPluginSniffCannotSeeAnUnmappedStatus records the residue of
// [TestStatusCodeRoundTrips]: a 510 carrying one of the two plugin phrases is
// indistinguishable from a 500 carrying it, so this implementation translates where Python
// would re-raise.
func TestPluginSniffCannotSeeAnUnmappedStatus(t *testing.T) {
	if !isPluginInconsistentState(daemonError(510, "network does not exist")) {
		t.Skip("the SDK grew a way to recover an unmapped status; tighten isServerError")
	}
}

// TestMountsDeniedIsAPrefixTest: `start` uses `e.explanation.startswith(
// 'Mounts denied')`, not a substring search, so the phrase has to open the
// message.
func TestMountsDeniedIsAPrefixTest(t *testing.T) {
	if !isMountsDenied(daemonError(500, "Mounts denied: the path /x is not shared")) {
		t.Error("a Mounts denied 500 was not recognised")
	}
	if isMountsDenied(daemonError(500, "error: Mounts denied later in the string")) {
		t.Error("a mid-string match was accepted, but Python tests the prefix")
	}
	if isMountsDenied(daemonError(404, "Mounts denied")) {
		t.Error("a non-500 was accepted")
	}
}

// TestDialTCPSniff is `DockerImage._check_and_pull`'s registry-unreachable
// signature: a 500 whose explanation mentions `dial tcp`.
func TestDialTCPSniff(t *testing.T) {
	if !isDialTCP(daemonError(500, "Get https://registry-1.docker.io/v2/: dial tcp: lookup failed")) {
		t.Error("a dial tcp 500 was not recognised")
	}
	if isDialTCP(daemonError(500, "manifest unknown")) {
		t.Error("an unrelated 500 was recognised as offline")
	}
}

// TestVersionComparisonMatchesDockerPy is `docker.utils.version_lt` /
// `version_gte`, whose answers were read off docker-py 7.2.0 directly.
func TestVersionComparisonMatchesDockerPy(t *testing.T) {
	tests := []struct {
		v1, v2  string
		wantLT  bool
		wantGTE bool
	}{
		{"25.0.0", "26.0.0", true, false},
		{"26.0.0", "26.0.0", false, true},
		{"27.0.1", "27.0.0", false, true},
		{"20.10", "26.0.0", true, false},
		{"28.5.2", "27.0.0", false, true},
		// docker-py's zero padding: equal, not less.
		{"26.0", "26.0.0", false, true},
		{"27", "27.0.0", false, true},
		// `int()`'s tolerance, which is the whole reason PyInt is used here.
		{"027.000.000", "27.0.0", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_vs_"+tt.v2, func(t *testing.T) {
			lt, err := versionLT(tt.v1, tt.v2)
			if err != nil {
				t.Fatalf("versionLT: %v", err)
			}
			if lt != tt.wantLT {
				t.Errorf("versionLT(%q, %q) = %v, want %v", tt.v1, tt.v2, lt, tt.wantLT)
			}

			gte, err := versionGTE(tt.v1, tt.v2)
			if err != nil {
				t.Fatalf("versionGTE: %v", err)
			}
			if gte != tt.wantGTE {
				t.Errorf("versionGTE(%q, %q) = %v, want %v", tt.v1, tt.v2, gte, tt.wantGTE)
			}
		})
	}
}

// TestVersionComparisonShortCircuitsOnEquality reproduces docker-py's
// `if v1 == v2: return 0`, which runs BEFORE either side is parsed — so two
// equal unparsable strings compare successfully. The two empty strings are the
// reachable case: `parse_docker_engine_version` returns "" for a version that
// starts with a non-digit.
func TestVersionComparisonShortCircuitsOnEquality(t *testing.T) {
	lt, err := versionLT("", "")
	if err != nil {
		t.Fatalf("versionLT(\"\", \"\") = %v, want no error from the short circuit", err)
	}
	if lt {
		t.Error("two equal strings compared as less-than")
	}
}

// TestVersionComparisonRejectsAnUnparsableComponent covers the exception a
// daemon version like "v27.0.0" produces: the parser answers "" and `int("")` raises,
// at the comparison rather than at the constructor.
func TestVersionComparisonRejectsAnUnparsableComponent(t *testing.T) {
	for _, v := range []string{"", "3.8.3-beta", "v27"} {
		if _, err := versionLT(v, "26.0.0"); err == nil {
			t.Errorf("versionLT(%q, …) accepted an unparsable version", v)
		}
	}
}

// TestErrdefsSentinelsSurviveWrapping guards the assumption the classifiers
// rest on: the boxed sentinel is still reachable through errors.Is after the
// SDK's message wrapper.
func TestErrdefsSentinelsSurviveWrapping(t *testing.T) {
	err := daemonError(404, "No such container")
	if !errors.Is(err, cerrdefs.ErrNotFound) {
		t.Error("the NotFound sentinel did not survive wrapping")
	}
}
