//go:build unix

package util

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// waitUserInputTimeout is the 0.1 s of `select.select([sys.stdin], [], [], 0.1)`
// (utils.py:168). It is the poll interval of the startup wait loop, not a
// deadline for anything.
const waitUserInputTimeout = 100 * time.Millisecond

// WaitUserInput is utils.wait_user_input_linux (utils.py:165), the "did the
// user hit a key?" probe of the device-startup wait
// (DockerMachine.py:935-939): while Kathará waits for `/tmp/EOS` to appear it
// checks, once per iteration, whether the user has asked for the terminal
// back.
//
// It reports whether standard input is *readable*, and it does not read it —
// so the keystroke stays in the buffer for whatever runs next. The Windows arm
// cannot do that and consumes the key instead; the asymmetry is Python's and
// is preserved (PACKAGE_GRAPH.md §4).
//
// "Readable" is not "a key was pressed". A redirected or closed stdin is
// readable at EOF and always has been, so `kathara lstart < /dev/null` breaks
// out of the wait on its first iteration in both implementations.
func WaitUserInput() (bool, error) {
	fd := int(os.Stdin.Fd())

	// PEP 475: a signal restarts the select, but with the time already spent
	// deducted, so the call as a whole still returns after 0.1 s. The deadline
	// is therefore computed once and the timeout shrinks on every retry —
	// re-arming the full 0.1 s would let a signal storm stretch the probe
	// without bound.
	deadline := time.Now().Add(waitUserInputTimeout)
	remaining := waitUserInputTimeout

	// select(2) rewrites both the set and the timeout, so both are rebuilt per
	// attempt.
	for {
		var readFds unix.FdSet
		readFds.Set(fd)
		timeout := unix.NsecToTimeval(int64(remaining))

		ready, err := unix.Select(fd+1, &readFds, nil, nil, &timeout)
		if errors.Is(err, unix.EINTR) {
			// An expired deadline still gets a zero-timeout select rather than
			// a straight `false`: CPython retries either way, and stdin may
			// have become readable while the handler ran.
			if remaining = time.Until(deadline); remaining < 0 {
				remaining = 0
			}
			continue
		}
		if err != nil {
			return false, err
		}

		return ready > 0, nil
	}
}
