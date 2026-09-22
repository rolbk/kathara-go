package util

import (
	"strings"

	"golang.org/x/sys/unix"
)

// IsWSLPlatform is utils.is_wsl_platform (utils.py:131): a Linux whose kernel
// release names Microsoft is a WSL guest.
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
