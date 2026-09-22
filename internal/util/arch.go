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
func GetArchitecture() (string, error) {
	return architectureOf(machineName())
}

// architectureOf is the dispatch half of get_architecture, split out so the
// table can be tested against every machine string the oracle was asked about
// rather than only against this host's.
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
