package docker

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// TestNormalizeImageTag is EXPECTATIONS-docker.md §4.1, bug included.
//
// Python's guard is `if (':' or '@') not in image_name`, and `(':' or '@')`
// evaluates to `':'` before the `in` runs — so only the colon is ever tested
// and the `@` never participates (docker-backend.md gotcha 4). It happens not
// to matter, because a digest reference contains `sha256:`, but the port must
// not "fix" it: the string that reaches the daemon would change.
func TestNormalizeImageTag(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"kathara/test", "kathara/test:latest"},
		{"kathara/test:latest", "kathara/test:latest"},
		{"kathara/test:tag", "kathara/test:tag"},
		// A digest reference is left alone — by the colon in `sha256:`, not by
		// the `@` the author meant to test.
		{"kathara/test@sha256:abc", "kathara/test@sha256:abc"},
		// And a registry host with a port is left alone too, for the same
		// accidental reason.
		{"localhost:5000/img", "localhost:5000/img"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := normalizeImageTag(tt.in); got != tt.want {
				t.Errorf("normalizeImageTag(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNormalizeImageTagIgnoresTheAtSign is the bug stated on its own, so that a
// future "cleanup" trips a named test rather than a golden: a name carrying an
// `@` but NO colon is tagged `:latest`, which is not what the code reads like
// it intends.
func TestNormalizeImageTagIgnoresTheAtSign(t *testing.T) {
	if got := normalizeImageTag("img@digest"); got != "img@digest:latest" {
		t.Errorf("normalizeImageTag = %q; the `@` must not suppress the tag", got)
	}
}

// TestImageArchitectureCompatibility is EXPECTATIONS-docker.md §4.3's twelve-way
// matrix, reduced to the two rules it encodes:
//
//   - a LOCAL image has one architecture and it must be in the compatible set;
//   - a REMOTE manifest list has many and ONE compatible entry is enough.
//
// The host architecture is `utils.get_architecture()` — the KERNEL's, not
// `runtime.GOARCH` (OQ-20) — so the test asks the same function the code does
// rather than hard-coding a value that would only hold on one machine. The
// macOS Rosetta row is covered separately by
// [TestImageArchitectureRosettaIsMacOnly].
func TestImageArchitectureCompatibility(t *testing.T) {
	host, err := util.GetArchitecture()
	if err != nil {
		t.Fatalf("GetArchitecture: %v", err)
	}
	// A value the host can never be, for the incompatible rows.
	foreign := "mips64le-not-a-real-host"

	t.Run("local, matching", func(t *testing.T) {
		if err := checkLocalImageArchitecture("kathara/base", host); err != nil {
			t.Errorf("a matching local image was rejected: %v", err)
		}
	})

	t.Run("local, mismatched", func(t *testing.T) {
		err := checkLocalImageArchitecture("kathara/base", foreign)
		if !errors.Is(err, kerrors.ErrInvalidImageArchitecture) {
			t.Errorf("a foreign local image gave %v, want InvalidImageArchitecture", err)
		}
	})

	t.Run("remote, one compatible platform among several", func(t *testing.T) {
		if err := checkRemoteImageArchitecture("kathara/base", []string{foreign, host}); err != nil {
			t.Errorf("a manifest list holding the host arch was rejected: %v", err)
		}
	})

	t.Run("remote, no compatible platform", func(t *testing.T) {
		err := checkRemoteImageArchitecture("kathara/base", []string{foreign})
		if !errors.Is(err, kerrors.ErrInvalidImageArchitecture) {
			t.Errorf("a foreign manifest list gave %v, want InvalidImageArchitecture", err)
		}
	})

	t.Run("remote, empty platform list", func(t *testing.T) {
		// `len(image_archs) > 0` is the compatibility test, so an empty list
		// is incompatible rather than vacuously fine.
		if err := checkRemoteImageArchitecture("kathara/base", nil); !errors.Is(err, kerrors.ErrInvalidImageArchitecture) {
			t.Errorf("an empty platform list gave %v, want InvalidImageArchitecture", err)
		}
	})
}

// TestImageArchitectureErrorNamesTheHost: the message interpolates `host_arch`,
// NOT the compatible set — so a Rosetta-capable host that finds neither arm64
// nor amd64 still reports arm64.
func TestImageArchitectureErrorNamesTheHost(t *testing.T) {
	host, err := util.GetArchitecture()
	if err != nil {
		t.Fatalf("GetArchitecture: %v", err)
	}

	archErr := checkLocalImageArchitecture("kathara/base", "nope")
	var typed *kerrors.ImageArchError
	if !errors.As(archErr, &typed) {
		t.Fatalf("error = %v, want an ImageArchError", archErr)
	}
	if typed.Arch != host {
		t.Errorf("Arch = %q, want the host architecture %q", typed.Arch, host)
	}
	if typed.Image != "kathara/base" {
		t.Errorf("Image = %q", typed.Image)
	}
}

// TestImageArchitectureRosettaIsMacOnly is the one place the three platforms
// disagree (`DockerImage.py:190-192`): macOS additionally accepts `amd64`,
// because Rosetta runs those images on an arm64 host. Linux and Windows do not.
func TestImageArchitectureRosettaIsMacOnly(t *testing.T) {
	host, compatible, err := compatibleArchitectures()
	if err != nil {
		t.Fatalf("compatibleArchitectures: %v", err)
	}

	if compatible[0] != host {
		t.Errorf("the host architecture %q is not first in %q", host, compatible)
	}

	switch runtime.GOOS {
	case "darwin":
		if len(compatible) != 2 || compatible[1] != "amd64" {
			t.Errorf("macOS compatible set = %q, want the host plus amd64", compatible)
		}
	default:
		if len(compatible) != 1 {
			t.Errorf("%s compatible set = %q, want the host architecture alone", runtime.GOOS, compatible)
		}
	}
}

// TestIsDaemonAPIError pins what `_check_and_pull`'s inner `except APIError`
// catches, which is the whole of the update check's error policy: a daemon
// answer is swallowed with a debug line WHEREVER inside `check_for_updates` it
// came from — the subscriber's own pull included — while a transport failure
// and a cancelled context escape and fail the deploy.
func TestIsDaemonAPIError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"daemon 404", errors.New(daemonPrefix + "No such image: kathara/base:latest"), true},
		{"daemon rate limit", errors.New(daemonPrefix + "toomanyrequests: You have reached your pull rate limit"), true},
		// The SDK's own 404 for an empty reference, which never reached the
		// daemon and therefore carries no prefix. docker-py posts the empty
		// reference and is answered 404, so this is an APIError there.
		{"synthesised not found", cerrdefs.ErrNotFound, true},
		{"cancelled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"wrapped cancellation", fmt.Errorf("pulling: %w", context.Canceled), false},
		// A subscriber failure that is not a daemon answer — a terminal read
		// dying under the `Prompt` policy — is none of docker-py's exception
		// types either, so it escapes as Python's would.
		{"subscriber", errors.New("stdin is not a terminal"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDaemonAPIError(tt.err); got != tt.want {
				t.Errorf("isDaemonAPIError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
