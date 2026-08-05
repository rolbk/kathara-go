package tmuxdrv

import (
	"context"
	"fmt"
	"os"
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
// tmux sets $TMUX for every process it spawns and refuses to nest an
// attach-session ("sessions should be nested with care, unset $TMUX to force").
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
//
// On Unix it replaces the current process image with tmux (execve), so it does
// not return on success; the shell that ran kathara ends up talking to tmux
// directly, with no proxy process in between to mangle signals, window-size
// changes or the exit status. When the user detaches (prefix-d), tmux exits and
// the session, its windows and the devices behind them keep running.
//
// Three cases, decided by $TMUX:
//
//   - not inside tmux: exec attach-session.
//   - inside a client of the same server: exec would nest a server inside
//     itself, so the existing client is switched to the session instead and
//     Attach returns normally.
//   - inside a client of a different server (kathara on the default socket,
//     user's tmux on `-L work`): not nesting, so attach normally, with $TMUX
//     dropped from the child environment because tmux only looks at its
//     presence.
//
// window may be empty to attach without changing the active window.
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
			// The session exists, so the server answered a moment ago; treat
			// an unexpected failure here as "different server" and attach,
			// which fails loudly rather than switching someone else's client.
			ours = ""
		}
		if ours != "" && ours == outer {
			_, err := d.run(ctx, "switch-client", "-t", sessionTarget(session))
			return err
		}
		return d.execAttach(d.AttachArgs(session), environWithoutTmux())
	}

	return d.execAttach(d.AttachArgs(session), os.Environ())
}

// environWithoutTmux copies the environment with $TMUX removed, so that tmux
// does not mistake a different server's client for nesting.
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
