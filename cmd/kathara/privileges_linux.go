//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropEffectivePrivileges is
// `PrivilegeHandler.get_instance().drop_effective_privileges()`
// (`utils.exec_by_platform(…, lambda: None, lambda: None)` — Linux only).
func dropEffectivePrivileges() {
	uid, gid := os.Getuid(), os.Getgid()
	// Group first, then user: raising the user id back is impossible after
	// the drop, so the group has to go while there is still the privilege to
	// change it. Python's order, for the same reason.
	// `x/sys/unix` exposes the per-thread `setegid`/`seteuid`; Go's runtime
	// multiplexes goroutines over threads, so the credentials have to change
	// for the whole process, which is what `syscall.Setegid`/`Seteuid` do
	// (they issue the glibc-style process-wide variants through the runtime).
	_ = unixSetegid(gid)
	_ = unixSeteuid(uid)
}

// unixSetegid and unixSeteuid are the process-wide credential calls, named
// through variables so that the two arms below stay one line each.
var (
	unixSetegid = syscallSetegid
	unixSeteuid = syscallSeteuid
)

func syscallSetegid(gid int) error { return unix.Setregid(-1, gid) }
func syscallSeteuid(uid int) error { return unix.Setreuid(-1, uid) }
