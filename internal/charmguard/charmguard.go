// Package charmguard keeps one side effect of linking bubbletea out of the
// fourteen commands that are not a terminal UI.
//
// # The problem
//
// `bubbletea@v1.3.10/tea_init.go` runs, in its package `init()`:
//
//	_ = lipgloss.HasDarkBackground()
//
// which — the first time anything asks — writes `ESC]11;? ESC\` and `ESC[6n`
// to **os.Stdout** and blocks reading os.Stdin until the terminal answers or
// `termenv.OSCTimeout` (5 seconds) expires. It is bubbletea's own documented
// workaround for a v1 ordering bug and its comment says it goes away in v2.
//
// Because it is an `init()`, it runs for every command in the binary, not only
// for `kathara settings`. Measured on this tree, with the query left in place:
//
//	stdout is a pipe                       0.01 s, nothing written  (termenv
//	                                       short-circuits on a non-tty)
//	stdout is a terminal that answers      0.01 s, 10 bytes written
//	stdout is a pty with no emulator       5.02 s, 10 bytes written
//	TERM=dumb / screen* / tmux*            0.01 s, nothing written
//
// The third row is `kathara -v` taking five seconds under `script`, `expect`,
// a pty-based CI runner or a serial console — on every invocation — while
// swallowing whatever the user typed during the wait. PORT_SPEC §3.2 item 3
// asks for a settings screen that "works over SSH"; it does not ask for a
// five-second pause in front of `kathara lstart`.
//
// The golden recordings are not affected: the harness captures through pipes,
// which is the first row.
//
// # The fix
//
// `lipgloss.SetHasDarkBackground` marks the answer as explicitly set, and
// `HasDarkBackground` then returns it without asking the terminal anything. So
// pinning the value before bubbletea's init runs removes the query entirely.
//
// `true` is not an arbitrary choice: it is what the query itself produces when
// it fails or times out, because `termenv.Output.HasDarkBackground` converts
// the unanswered `NoColor{}` to black and reports a luminance below 0.5. A
// terminal that *would* have answered "light" now gets the dark answer, which
// costs nothing today — this port renders no `lipgloss.AdaptiveColor`, only
// bold and faint, which are the same SGR codes on either background. Anything
// that starts rendering adaptive colours should revisit this.
//
// # The ordering assumption
//
// Go guarantees only that a package's imports are initialized before the
// package itself; the order between two packages that do not import each other
// is not specified by the language. gc resolves it by walking the importer's
// imports in sorted path order, and
// "github.com/KatharaFramework/kathara-go/internal/charmguard" sorts before
// "github.com/charmbracelet/bubbletea" (`K` is 0x4B, `c` is 0x63), so this
// init runs first. Verified by running the built binary under a pty with no
// responder and timing it.
//
// If a future toolchain changed that order the guard would stop working and
// the five-second wait would come back — a degradation, not a breakage.
// PROPOSED-DIVERGENCES.md records the whole thing, because the durable answer
// is bubbletea v2, and PACKAGE_GRAPH.md §5 pins v1.
//
// Import it for effect from `cmd/kathara` only. It is deliberately not imported
// from `internal/cliout`, which the public `kathara` package depends on: a
// library consumer building their own bubbletea program must keep their own
// background detection.
package charmguard

import "github.com/charmbracelet/lipgloss"

func init() {
	lipgloss.SetHasDarkBackground(true)
}
