package util

import (
	"strings"

	"golang.org/x/sys/unix"
)

// IsWSLPlatform is utils.is_wsl_platform (utils.py:131): a Linux whose kernel
// release names Microsoft is a WSL guest.
//
// The substring test is on the lower-cased release, which is what makes it
// span both generations — WSL1 reports `4.4.0-19041-Microsoft` and WSL2
// reports `5.15.90.1-microsoft-standard-WSL2`.
//
// Nothing in 3.8.3 calls it. It is ported because PACKAGE_GRAPH.md §4 keeps
// the file, and because the Windows-side Docker integration is the obvious
// future caller.
func IsWSLPlatform() bool {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		// os.uname() raises here and Python does not catch it; uname(2)
		// cannot fail with a valid buffer, so this is unreachable and a
		// "not WSL" answer is the harmless reading.
		return false
	}

	return strings.Contains(strings.ToLower(utsString(uts.Release[:])), "microsoft")
}
