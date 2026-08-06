package tmuxdrv

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Window describes one device's window inside a scenario session.
type Window struct {
	// Name is the window name. Kathara sets it to the device name; that is
	// the whole addressing scheme for `kathara connect --tmux <device>`.
	Name string

	// Command is the shell command tmux runs in the window (Kathara passes
	// "<kathara> connect -l <device>"). Empty means tmux's default shell,
	// which also means the window outlives any command.
	Command string

	// Dir is the window's start directory (tmux -c). Empty means inherit.
	Dir string

	// Env are "KEY=VALUE" pairs injected into the window (tmux -e).
	//
	// This matters more than it looks: a window created in an *existing*
	// tmux server inherits that server's environment, captured whenever the
	// server first started, not the environment of the kathara process
	// creating the window.
	Env []string

	// RemainOnExit keeps the window (dead) after its command exits, instead
	// of letting tmux close it. Off by default, matching 3.8.3. Turning it
	// on is how a failing `kathara connect` stops being invisible.
	RemainOnExit bool
}

// WindowInfo is one row of `list-windows -F`.
type WindowInfo struct {
	Index  int
	Name   string
	ID     string // tmux window id, e.g. "@3"
	Active bool
	Dead   bool // pane_dead: command exited and remain-on-exit kept the window
}

// listWindowsFormat is asked for explicitly; tmux's default human output is
// never parsed. Tab-separated because window names are validated to exclude
// tabs (checkWindowName).
const listWindowsFormat = "#{window_index}\t#{window_name}\t#{window_id}\t#{window_active}\t#{pane_dead}"

// HasSession reports whether the exactly-named session exists.
//
// tmux exits 1 both for "no such session" and for "no server running", and the
// two are indistinguishable by exit status alone; both mean false here. A false
// negative is safe: EnsureSession still refuses to clobber, because tmux itself
// rejects a duplicate new-session.
//
// A name tmux would rewrite is rejected rather than probed: tmux could never
// have stored it, so the honest answer is "you cannot ask that", not a "false"
// the caller would act on forever.
func (d *Driver) HasSession(ctx context.Context, session string) (bool, error) {
	if err := checkSessionName(session); err != nil {
		return false, err
	}
	_, err := d.run(ctx, "has-session", "-t", sessionTarget(session))
	if err == nil {
		return true, nil
	}
	if isSessionAbsent(err) {
		return false, nil
	}
	return false, err
}

// ListSessions returns the names of all sessions on the server, in tmux's
// listing order. No server means no sessions, not an error.
//
// One row per session is safe even against session names a foreign process
// chose: tmux vis-escapes control characters in a session name before storing
// it (a literal newline is stored as the two characters '\' and 'n'), so a
// name cannot forge a row boundary in the newline-delimited -F output. Pinned
// by TestForeignSessionNameCannotForgeARow.
func (d *Driver) ListSessions(ctx context.Context) ([]string, error) {
	out, err := d.run(ctx, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if isNoServer(err) {
			return nil, nil
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// ListKatharaSessions returns only the sessions this package owns, i.e. those
// carrying SessionPrefix.
func (d *Driver) ListKatharaSessions(ctx context.Context) ([]string, error) {
	all, err := d.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, s := range all {
		if strings.HasPrefix(s, SessionPrefix) {
			out = append(out, s)
		}
	}
	return out, nil
}

// EnsureSession creates the session, detached, iff it does not already exist,
// and reports whether it created it.
//
// tmux cannot hold a session with zero windows, so creation needs the first
// window up front; that first window is `initial`. 3.8.3 instead created the
// session around a randomly-named placeholder window and killed the placeholder
// afterwards; making the first device the first window removes the placeholder,
// the cleanup step, and the "did I create it?" bookkeeping they needed.
//
// An existing session is never touched: no windows are added, renamed, killed
// or re-ordered, and no client is disturbed.
func (d *Driver) EnsureSession(ctx context.Context, session string, initial Window) (created bool, err error) {
	if err := checkSessionName(session); err != nil {
		return false, err
	}
	if err := checkWindowName(initial.Name); err != nil {
		return false, err
	}

	exists, err := d.HasSession(ctx, session)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	args := []string{"new-session", "-d", "-s", session, "-n", initial.Name}
	args = append(args, initial.creationFlags()...)
	args = append(args, initial.commandArgs()...)
	args = append(args, remainOnExitCommand(session, initial)...)

	if _, err := d.run(ctx, args...); err != nil {
		// Lost a creation race against another kathara process: the session
		// now exists and belongs to whoever won. Attach semantics, not
		// clobber semantics.
		if isAbsence(err, duplicateSessionMessages...) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// WindowOutcome reports what EnsureWindow had to do.
type WindowOutcome int

const (
	// WindowExisted: the session and a window with that name were both there.
	WindowExisted WindowOutcome = iota
	// WindowCreated: the session was there, the window was added to it.
	WindowCreated
	// SessionCreated: the session was absent and was created around the window.
	SessionCreated
)

func (o WindowOutcome) String() string {
	switch o {
	case WindowExisted:
		return "existed"
	case WindowCreated:
		return "window-created"
	case SessionCreated:
		return "session-created"
	default:
		return "unknown(" + strconv.Itoa(int(o)) + ")"
	}
}

// EnsureWindow is the one call the deploy path needs: it guarantees the
// scenario session exists and that it holds exactly one window named w.Name
// running w.Command.
//
// Re-running it for a device that already has a window is a no-op — tmux's own
// new-window happily creates a second window with the same name, so the
// duplicate check is ours to make.
func (d *Driver) EnsureWindow(ctx context.Context, session string, w Window) (WindowOutcome, error) {
	created, err := d.EnsureSession(ctx, session, w)
	if err != nil {
		return WindowExisted, err
	}
	if created {
		return SessionCreated, nil
	}

	has, err := d.HasWindow(ctx, session, w.Name)
	if err != nil {
		return WindowExisted, err
	}
	if has {
		return WindowExisted, nil
	}

	if err := d.AddWindow(ctx, session, w); err != nil {
		return WindowExisted, err
	}
	return WindowCreated, nil
}

// AddWindow adds a detached window to an existing session unconditionally.
// Prefer EnsureWindow; this exists for callers that have already established
// the window is absent — tmux window names are not unique, so calling this for
// a name the session already holds creates a *second* window with that name,
// and every later name-based target (including the chained remain-on-exit
// set-option below, and SelectWindow) then resolves to the older, lower-index
// one.
func (d *Driver) AddWindow(ctx context.Context, session string, w Window) error {
	if err := checkSessionName(session); err != nil {
		return err
	}
	if err := checkWindowName(w.Name); err != nil {
		return err
	}

	// The trailing ':' targets "the session, no particular window"; without it
	// tmux would resolve the target as a window name.
	args := []string{"new-window", "-d", "-t", sessionTarget(session) + ":", "-n", w.Name}
	args = append(args, w.creationFlags()...)
	args = append(args, w.commandArgs()...)
	args = append(args, remainOnExitCommand(session, w)...)

	if _, err := d.run(ctx, args...); err != nil {
		if isSessionAbsent(err) {
			return fmt.Errorf("%w: %s", ErrSessionNotFound, session)
		}
		return err
	}
	return nil
}

// creationFlags renders the flags new-session and new-window share.
func (w Window) creationFlags() []string {
	var args []string
	if w.Dir != "" {
		args = append(args, "-c", w.Dir)
	}
	for _, kv := range w.Env {
		args = append(args, "-e", kv)
	}
	return args
}

// commandArgs renders the shell-command positional, guarded by "--" so a
// command starting with '-' can never be read as a flag.
//
// "--" does not protect against tmux's *command-sequence* parser, which runs
// before argument assignment: an argument ending in an unescaped ';' is a
// command separator, and tmux drops the ';' and starts a new command after it.
// `new-session ... -- 'sleep 60;'` therefore runs `sleep 60` — silently, with
// exit status 0. escapeTrailingSemicolon puts the ';' back where the caller
// wanted it. A ';' anywhere else in the string is untouched, because only a
// trailing one separates (both verified on tmux 3.5a).
func (w Window) commandArgs() []string {
	if w.Command == "" {
		return nil
	}
	return []string{"--", escapeTrailingSemicolon(w.Command)}
}

// escapeTrailingSemicolon backslash-escapes a final ';' that tmux would
// otherwise eat as a command separator. A ';' the caller already escaped (an
// odd number of backslashes in front of it) is left alone.
func escapeTrailingSemicolon(cmd string) string {
	if !strings.HasSuffix(cmd, ";") {
		return cmd
	}
	backslashes := 0
	for i := len(cmd) - 2; i >= 0 && cmd[i] == '\\'; i-- {
		backslashes++
	}
	if backslashes%2 == 1 {
		return cmd
	}
	return cmd[:len(cmd)-1] + `\;`
}

// remainOnExitCommand appends a chained `; set-option -w remain-on-exit on` to
// the creating command.
//
// neither new-session nor new-window takes remain-on-exit as a flag, and a
// separate set-option invocation loses a race against any command that exits
// immediately — which is exactly the case worth keeping visible. Chaining the
// two commands into one tmux invocation removes the race: the server runs both
// before it returns to its event loop and notices the pane has died. Verified
// against `false` as the window command, which cannot exit any sooner.
//
// The alternative, `set-option -g -w remain-on-exit on`, would change every
// window on the user's tmux server, Kathara's or not.
func remainOnExitCommand(session string, w Window) []string {
	if !w.RemainOnExit {
		return nil
	}
	return []string{";", "set-option", "-t", windowTarget(session, w.Name), "-w", "remain-on-exit", "on"}
}

// ListWindows returns the session's windows in tmux's index order.
func (d *Driver) ListWindows(ctx context.Context, session string) ([]WindowInfo, error) {
	if err := checkSessionName(session); err != nil {
		return nil, err
	}
	out, err := d.run(ctx, "list-windows", "-t", sessionTarget(session), "-F", listWindowsFormat)
	if err != nil {
		if isSessionAbsent(err) {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, session)
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	lines := strings.Split(out, "\n")
	windows := make([]WindowInfo, 0, len(lines))
	for _, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("tmuxdrv: unparsable list-windows row %q (%d fields, want 5)", line, len(fields))
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("tmuxdrv: unparsable window index in row %q: %w", line, err)
		}
		windows = append(windows, WindowInfo{
			Index:  idx,
			Name:   fields[1],
			ID:     fields[2],
			Active: fields[3] == "1",
			Dead:   fields[4] == "1",
		})
	}
	return windows, nil
}

// WindowNames returns just the window names, in index order.
func (d *Driver) WindowNames(ctx context.Context, session string) ([]string, error) {
	windows, err := d.ListWindows(ctx, session)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(windows))
	for _, w := range windows {
		names = append(names, w.Name)
	}
	return names, nil
}

// HasWindow reports whether the session holds a window with exactly this name.
// A missing session is reported as false, not as an error, so callers can probe
// without a prior HasSession.
func (d *Driver) HasWindow(ctx context.Context, session, window string) (bool, error) {
	windows, err := d.ListWindows(ctx, session)
	if err != nil {
		if isSessionNotFound(err) {
			return false, nil
		}
		return false, err
	}
	for _, w := range windows {
		if w.Name == window {
			return true, nil
		}
	}
	return false, nil
}

// SelectWindow makes the named window the session's active one. This is the
// half of `kathara connect --tmux <device>` that picks the right device;
// Attach is the half that hands over the terminal.
func (d *Driver) SelectWindow(ctx context.Context, session, window string) error {
	if err := checkSessionName(session); err != nil {
		return err
	}
	if err := checkWindowName(window); err != nil {
		return err
	}
	if _, err := d.run(ctx, "select-window", "-t", windowTarget(session, window)); err != nil {
		if isAbsence(err, windowAbsentMessages...) {
			return fmt.Errorf("%w: %s in session %s", ErrWindowNotFound, window, session)
		}
		if isSessionAbsent(err) {
			return fmt.Errorf("%w: %s", ErrSessionNotFound, session)
		}
		return err
	}
	return nil
}

// KillSession kills the session and everything in it, and reports whether
// there was anything to kill. Killing is idempotent: an absent session (or an
// absent server) is not an error.
//
// Devices are unaffected. The windows run `kathara connect`, which is an attach
// to a running container; killing the window kills the attach, never the
// container.
func (d *Driver) KillSession(ctx context.Context, session string) (killed bool, err error) {
	if err := checkSessionName(session); err != nil {
		return false, err
	}
	if _, err := d.run(ctx, "kill-session", "-t", sessionTarget(session)); err != nil {
		// kill-session can only be absent-for-lack-of-session or of server;
		// anything else it says is a real failure.
		if isSessionAbsent(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// KillWindow removes one device's window, and reports whether there was one.
// Killing the last window of a session destroys the session; killing the last
// session on a socket makes the tmux server exit.
func (d *Driver) KillWindow(ctx context.Context, session, window string) (killed bool, err error) {
	if err := checkSessionName(session); err != nil {
		return false, err
	}
	if err := checkWindowName(window); err != nil {
		return false, err
	}
	if _, err := d.run(ctx, "kill-window", "-t", windowTarget(session, window)); err != nil {
		// Either half of the target can be the missing one.
		if isSessionAbsent(err) || isAbsence(err, windowAbsentMessages...) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DetachSession detaches every client attached to the session, leaving the
// session, its windows, their commands and the devices behind them running.
// This is the programmatic form of the user pressing prefix-d.
//
// Detaching when nothing is attached is a no-op, not an error: tmux exits 1
// with "no current client" in that case, and says the same thing when the
// session target does not resolve either (verified on 3.5a — it looks for a
// client before it looks at -s), so the two collapse into one no-op here.
func (d *Driver) DetachSession(ctx context.Context, session string) error {
	if err := checkSessionName(session); err != nil {
		return err
	}
	if _, err := d.run(ctx, "detach-client", "-s", sessionTarget(session)); err != nil {
		if isSessionAbsent(err) || isAbsence(err, clientAbsentMessages...) {
			return nil
		}
		return err
	}
	return nil
}

// ClientTTYs returns the ttys of the clients attached to the session; empty
// means the session is running detached.
func (d *Driver) ClientTTYs(ctx context.Context, session string) ([]string, error) {
	if err := checkSessionName(session); err != nil {
		return nil, err
	}
	out, err := d.run(ctx, "list-clients", "-t", sessionTarget(session), "-F", "#{client_tty}")
	if err != nil {
		if isSessionAbsent(err) {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, session)
		}
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func isSessionNotFound(err error) bool { return errors.Is(err, ErrSessionNotFound) }
