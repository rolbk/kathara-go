//go:build !linux

// The macOS and Windows arms of `CheckCommand`'s platform line, where Python
// calls `platform.platform()`.

package main

import (
	"runtime"
	"strings"
)

// osVersion is `platform.platform()` reduced to what a Go binary can know
// without shelling out: the OS and the architecture.
//
// `platform.platform()` on macOS is `macOS-14.5-arm64-arm-64bit` and on Windows
// `Windows-10-10.0.19045-SP0`; both interpolate a release string that Go reads
// only through a syscall neither `runtime` nor `x/sys` exposes portably. The
// value is a diagnostic line in `kathara check`, is not compared by any golden,
// and is recorded in DIVERGENCES.md.
func osVersion() string {
	return strings.Join([]string{runtime.GOOS, runtime.GOARCH}, "-")
}
