package term

// newPty on macOS is the creack/pty implementation (pty_unix.go). Kept as a
// distinct per-OS constructor so Darwin-only behaviour (e.g. the osascript
// external-adapter path, PACKAGE_GRAPH term/external_darwin.go) has a home in
// Phase 6 without touching the shared implementation.
func newPty(ws Winsize) (Pty, error) {
	return newUnixPty(ws)
}
