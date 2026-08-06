//go:build unix

// This file exports `os.access` for the packages that may not import
// `golang.org/x/sys` themselves. PACKAGE_GRAPH.md §5 puts the x/sys grant here
// and holds `settings` (and every other layer under the §7 client) to the
// standard library, while `Setting.check_terminal` needs the real access(2) —
// the one call the standard library does not expose portably.

package util

import "golang.org/x/sys/unix"

// AccessXOK is `os.access(path, os.X_OK)`.
//
// It is access(2), which answers for the **real** uid rather than the
// effective one, and that is not an implementation detail: Go's own
// `exec.LookPath` uses `AT_EACCESS` and answers for the effective uid, so the
// two disagree exactly on the setuid/setgid install `cmd/kathara`'s privilege
// drop exists for. [whichAccessCheck] makes the same choice for the same
// reason.
//
// Unix only. Python reaches `os.access` on the Windows arm of nothing this
// port calls — `check_terminal`'s Windows branch is `lambda: True` — so there
// is no Windows spelling to get wrong here.
func AccessXOK(path string) bool {
	return unix.Access(path, unix.X_OK) == nil
}
