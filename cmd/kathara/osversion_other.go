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
func osVersion() string {
	return strings.Join([]string{runtime.GOOS, runtime.GOARCH}, "-")
}
