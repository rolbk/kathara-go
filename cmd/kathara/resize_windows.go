//go:build windows

// The Windows half of `connect`'s window-size forwarding.
// Windows has no SIGWINCH.
// Until then `connect` on Windows sizes the session once at attach, which is
// exactly what Python's `TerminalRunner` did on every platform.

package main

import (
	"os"

	"github.com/KatharaFramework/kathara-go/kathara"
)

// watchResize is a no-op on Windows; the initial size has already been sent by
// the caller.
func watchResize(*os.File, kathara.TTYSession) func() { return func() {} }
