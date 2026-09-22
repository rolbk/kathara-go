package term

// newPty on Linux is the creack/pty implementation (pty_unix.go). Kept as a
// distinct per-OS constructor so Linux-only behaviour has a home
// without touching the shared implementation.
func newPty(ws Winsize) (Pty, error) {
	return newUnixPty(ws)
}
