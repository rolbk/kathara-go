package term

func newPty(ws Winsize) (Pty, error) {
	return newUnixPty(ws)
}
