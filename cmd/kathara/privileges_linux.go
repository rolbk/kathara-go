//go:build linux

// This file is the one carve-out from the deferred `auth/` bundle
// (PACKAGE_GRAPH.md row `Kathara/auth/PrivilegeHandler.py`): the
// drop-effective-privileges call the entrypoint makes on Linux before
// dispatch, kept for setuid/setgid-docker installs. The ref-counted
// raise/drop machinery around `nsenter` stays deferred with `lab.ext`
// (PORT_SPEC §0.3).

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// dropEffectivePrivileges is
// `PrivilegeHandler.get_instance().drop_effective_privileges()`
// (`utils.exec_by_platform(…, lambda: None, lambda: None)` — Linux only).
//
// Python's body is `os.setegid(user_gid); os.seteuid(user_uid)` with the ids
// taken from the *real* user, which on a plain (non-setuid) install are already
// the effective ones, making the call a no-op. It matters only when the binary
// is installed setuid or setgid so that it can reach the Docker socket: there,
// dropping to the invoking user is what stops the rest of the process from
// running with the escalated identity.
//
// Failures are ignored, as Python ignores them: `seteuid` fails when the real
// and saved ids give no way back, which is exactly the already-unprivileged
// case the call is a no-op for.
func dropEffectivePrivileges() {
	uid, gid := os.Getuid(), os.Getgid()
	// Group first, then user: raising the user id back is impossible after
	// the drop, so the group has to go while there is still the privilege to
	// change it. Python's order, for the same reason.
	//
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
