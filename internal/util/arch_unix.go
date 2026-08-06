//go:build unix

package util

import "golang.org/x/sys/unix"

// machineName is `platform.machine()` on a Unix host, which CPython resolves
// to `os.uname().machine` — the `machine` field of `uname(2)`.
//
// `platform.machine()` returns the empty string when it cannot tell, and
// `get_architecture` then reports `Not implemented for host architecture
// \`\`.`; uname(2) cannot fail with a valid buffer, but the empty-string path
// stays wired so the two agree if it ever does.
func machineName() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		return ""
	}
	return utsString(uts.Machine[:])
}

// utsString reads a NUL-terminated field out of a `struct utsname`. The array
// is fixed-width and zero-padded, and the kernel is not required to terminate
// a name that exactly fills it.
func utsString(field []byte) string {
	for i, b := range field {
		if b == 0 {
			return string(field[:i])
		}
	}
	return string(field)
}
