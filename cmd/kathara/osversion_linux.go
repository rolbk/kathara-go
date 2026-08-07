//go:build linux

// `CheckCommand.linux_platform_info`: `os.uname()` rendered as
// `sysname-release-machine`.

package main

import "golang.org/x/sys/unix"

// osVersion is the Linux arm of `utils.exec_by_platform(linux_platform_info,
// platform.platform, platform.platform)` (`CheckCommand.py:56-58`).
func osVersion() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return "unknown"
	}
	return cstr(uts.Sysname[:]) + "-" + cstr(uts.Release[:]) + "-" + cstr(uts.Machine[:])
}

// cstr trims a NUL-padded `utsname` field.
func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
