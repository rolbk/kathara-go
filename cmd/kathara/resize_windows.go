//go:build windows

// The Windows half of `connect`'s window-size forwarding.
//
// Windows has no SIGWINCH. The console API reports a resize through
// `ReadConsoleInput`'s `WINDOW_BUFFER_SIZE_EVENT`, which cannot be read while
// the same handle is being drained as a byte stream by the input pump — so a
// resize watcher here needs the ConPTY-based input layer that PORT_SPEC §3.3's
// built-in multiplexer brings with it, and that is Phase 6 work
// (docs/port/SPIKES/windows-terminal.md).
//
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
