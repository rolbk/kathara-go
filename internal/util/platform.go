// This file covers the platform-dispatch primitive and the module constants of
// utils.py: the `MAC_OS`/`WINDOWS`/`LINUX` values, `EXCLUDED_FILES`,
// `RESERVED_MACHINE_NAMES`, `exec_by_platform` and `get_pool_size`.

package util

import (
	"runtime"
	"slices"
)

// The platforms utils.py branches on (utils.py:29-32). Python compares against
// `sys.platform`, Go against [runtime.GOOS], so the Windows spelling changes
// from `win32` to `windows`; `linux2` is dropped (see the package comment).
const (
	// MacOS is utils.MAC_OS.
	MacOS = "darwin"
	// Windows is utils.WINDOWS, respelled for [runtime.GOOS].
	Windows = "windows"
	// Linux is utils.LINUX.
	Linux = "linux"
)

// excludedFiles is utils.EXCLUDED_FILES (utils.py:35): names skipped when a
// device directory is walked into a tar. Kept private so no caller can append
// to the package's state; [ExcludedFiles] hands out a copy.
var excludedFiles = []string{".DS_Store"}

// reservedMachineNames is utils.RESERVED_MACHINE_NAMES (utils.py:38): keys that
// lab.conf and the folder parser must not read as device names.
var reservedMachineNames = []string{"shared", "_test"}

// ExcludedFiles returns utils.EXCLUDED_FILES in source order.
func ExcludedFiles() []string { return slices.Clone(excludedFiles) }

// ReservedMachineNames returns utils.RESERVED_MACHINE_NAMES in source order.
func ReservedMachineNames() []string { return slices.Clone(reservedMachineNames) }

// IsReservedMachineName reports whether name is one of
// utils.RESERVED_MACHINE_NAMES. Python spells this `name in RESERVED_MACHINE_NAMES`
// (LabParser.py:54, FolderParser.py:32) — an exact, case-sensitive comparison.
func IsReservedMachineName(name string) bool {
	return slices.Contains(reservedMachineNames, name)
}

// ExecByPlatform is utils.exec_by_platform (utils.py:138).
func ExecByPlatform[T any](funLinux, funWindows, funMac func() T) T {
	switch runtime.GOOS {
	case Linux:
		return funLinux()
	case Windows:
		return funWindows()
	case MacOS:
		return funMac()
	}
	var zero T
	return zero
}

// PoolSize is utils.get_pool_size (utils.py:106), the fan-out width of every
// parallel section and the Docker client's `max_pool_size`.
func PoolSize() int { return runtime.NumCPU() }
