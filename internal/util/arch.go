// This file is utils.get_architecture (utils.py:396): the map from the name
// the kernel gives its own instruction set onto the token Kathará puts in a
// Docker plugin name (`kathara/katharanp:amd64`) and compares an image's
// manifest against.

package util

import (
	"log/slog"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// GetArchitecture is utils.get_architecture (utils.py:396).
//
// The value comes from the *kernel*, not from this binary: Python reads
// `platform.machine()`, so a 32-bit Python on a 64-bit kernel still reports
// `x86_64` and still pulls the amd64 plugin. [runtime.GOARCH] would answer the
// other question — what this executable was compiled for — and would break a
// 386 build on an amd64 host, so it is deliberately not used here
// (PACKAGE_GRAPH.md §4, OQ-20).
//
// An architecture outside the table is a HostArchitectureError, which is the
// only outcome for riscv64, ppc64le, s390x and — because Python's table has
// `i686` but not `i386` — for a kernel that spells 32-bit x86 the other way.
func GetArchitecture() (string, error) {
	return architectureOf(machineName())
}

// architectureOf is the dispatch half of get_architecture, split out so the
// table can be tested against every machine string the oracle was asked about
// rather than only against this host's.
//
// `machine().lower()` is Python's full Unicode case mapping, which
// [strings.ToLower] is not, so the fold is [pyLower]. Every name a kernel
// actually reports is ASCII, where the two agree — but the folded value is
// what HostArchitectureError interpolates, so the fold has to be the right one
// for the values that are not.
func architectureOf(machine string) (string, error) {
	architecture := pyLower(machine)

	// utils.py:399. Python logs this at DEBUG, which `debug_level = DEBUG` in
	// the settings file makes visible; the level is off by default
	// (Setting.py:30 defaults to INFO).
	slog.Debug("Machine architecture is `" + architecture + "`.")

	switch architecture {
	case "x86_64", "amd64":
		return "amd64", nil
	case "i686":
		return "386", nil
	case "arm64", "aarch64":
		return "arm64", nil
	case "armv7l":
		return "armv7", nil
	case "armv6l":
		return "armv6", nil
	}

	return "", kerrors.NewHostArchitecture(architecture)
}
