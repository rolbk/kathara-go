package tmuxdrv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AttachArgs returns the argv that hands the current terminal to the session.
// Exposed so callers can exec it themselves, and so tests can assert on it
// without needing a TTY.
func (d *Driver) AttachArgs(session string) []string {
	return d.Argv("attach-session", "-t", sessionTarget(session))
}

// SwitchClientArgs returns the argv used instead of AttachArgs when kathara is
// running inside a client of the same tmux server.
func (d *Driver) SwitchClientArgs(session string) []string {
	return d.Argv("switch-client", "-t", sessionTarget(session))
}

// InsideTmux reports whether this process is running inside a tmux pane.
// tmux sets $TMUX for every process it spawns.
func InsideTmux() bool { return os.Getenv("TMUX") != "" }

// outerSocketPath returns the socket path of the tmux server this process is
// running under, or "" when it is not inside tmux. $TMUX is
// "<socket-path>,<server-pid>,<session-id>".
func outerSocketPath() string {
	v := os.Getenv("TMUX")
	if v == "" {
		return ""
	}
	path, _, _ := strings.Cut(v, ",")
	return path
}

// serverSocketPath asks the target server where its own socket is. Used to tell
// "we are inside the very server we are attaching to" (nesting, must switch
// client) from "we are inside some other tmux server" (not nesting, attaching
// is fine once $TMUX is out of the way).
func (d *Driver) serverSocketPath(ctx context.Context) (string, error) {
	return d.run(ctx, "display-message", "-p", "#{socket_path}")
}

// Attach gives the caller's terminal to the scenario session, optionally
// selecting one device's window first. This is `kathara connect --tmux
// <device>`: select-window to point at the device, then attach.
func (d *Driver) Attach(ctx context.Context, session, window string) error {
	if err := checkSessionName(session); err != nil {
		return err
	}

	exists, err := d.HasSession(ctx, session)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, session)
	}

	if window != "" {
		if err := d.SelectWindow(ctx, session, window); err != nil {
			return err
		}
	}

	outer := outerSocketPath()
	if outer != "" {
		ours, err := d.serverSocketPath(ctx)
		if err != nil {
			// Cannot tell same-server from different-server. Guessing
			// "different" would exec with $TMUX stripped, which is exactly the
			// combination that silently mirrors a terminal into its own pane,
			// so this fails instead. The session existed a moment ago, so a
			// server that has since gone is reported as such.
			if isNoServer(err) {
				return fmt.Errorf("%w: %s", ErrSessionNotFound, session)
			}
			return err
		}
		if sameSocket(ours, outer) {
			if _, err := d.run(ctx, "switch-client", "-t", sessionTarget(session)); err != nil {
				if isSessionAbsent(err) {
					return fmt.Errorf("%w: %s", ErrSessionNotFound, session)
				}
				return err
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return d.execAttach(d.AttachArgs(session), environWithoutTmux())
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	return d.execAttach(d.AttachArgs(session), os.Environ())
}

// sameSocket reports whether two socket paths name the same tmux server.
func sameSocket(a, b string) bool {
	if a == b {
		return a != ""
	}
	if a == "" || b == "" {
		return false
	}
	ra, err := resolveSocket(a)
	if err != nil {
		return false
	}
	rb, err := resolveSocket(b)
	if err != nil {
		return false
	}
	return ra == rb
}

func resolveSocket(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// environWithoutTmux copies the environment with $TMUX removed, so the child
// does not carry a variable naming a server it is not talking to. Only ever
// used on the different-server path: see Attach for why the same-server path
// must keep it.
func environWithoutTmux() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
