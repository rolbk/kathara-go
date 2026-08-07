//go:build !windows

// The Unix half of `connect`'s window-size forwarding: SIGWINCH.

package main

import (
	"os"
	"os/signal"

	"github.com/KatharaFramework/kathara-go/kathara"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// watchResize forwards every terminal resize to the device until the returned
// function is called.
//
// Python never did this: `TerminalRunner` sized the session once at attach and
// left it. A shell that has never been told the window grew wraps its prompt at
// the old width, which is one of the "terminal integration is the largest
// single source of user-facing failures" items PORT_SPEC §3.3 is about.
func watchResize(out *os.File, session kathara.TTYSession) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, unix.SIGWINCH)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ch:
				if cols, rows, err := term.GetSize(int(out.Fd())); err == nil {
					_ = session.Resize(uint16(cols), uint16(rows))
				}
			case <-done:
				return
			}
		}
	}()

	return func() {
		signal.Stop(ch)
		close(done)
	}
}
