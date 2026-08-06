// Package tmuxdrv drives the tmux(1) binary through its documented
// command-line interface.
//
// It is the transport half of the spec §3.3 tmux backend (§0.2 #2): Kathara
// never links a tmux client library and never parses tmux's human-readable
// output. Every query asks tmux for an explicit format string (-F) and every
// decision is taken on an exit status or on a value tmux was asked to print.
//
// The package is deliberately dependency-free (standard library only) so it can
// be exercised without a Docker daemon, a lab, or a TTY. Error values are local
// sentinels; the `term` package maps them onto the kerrors taxonomy.
//
// # Targets
//
// tmux resolves a bare -t target by exact name, then prefix, then fnmatch, so
// "-t lab" happily selects the session "lab1". Every target this package emits
// is written in tmux's exact-match form ("-t=lab", "-t=lab:=pc1"), which is the
// only way to make "does session X exist" mean X and not X-something.
package tmuxdrv

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Driver executes tmux commands. The zero value is usable and talks to the
// user's default tmux server with the user's own configuration.
type Driver struct {
	// Bin is the tmux executable. Empty means "tmux" resolved through PATH.
	Bin string

	// Socket is the tmux server socket *name* (tmux -L). Empty means tmux's
	// default socket, which is what production uses: Kathara sessions then
	// show up in the user's ordinary `tmux ls`. Tests set it to isolate
	// themselves from any server the user is running.
	Socket string

	// SocketPath is the tmux server socket *path* (tmux -S). Mutually
	// exclusive with Socket.
	SocketPath string

	// Config is the tmux configuration file (tmux -f). Empty means tmux's
	// default (~/.tmux.conf and friends). Tests set it to os.DevNull so that
	// user configuration cannot move window indices (base-index) or rename
	// windows underneath them.
	//
	// tmux only reads the configuration file when the *server* starts, so
	// this has no effect on a command that reaches an already-running server.
	Config string
}

// Sentinel errors. They are matched with errors.Is.
//
// "No server running on this socket" is deliberately not one of them: it is
// indistinguishable from "no such session" at the tmux CLI, and every operation
// here folds it into the same answer an absent session gets.
var (
	// ErrSessionNotFound means the named session does not exist.
	ErrSessionNotFound = errors.New("tmuxdrv: session not found")

	// ErrWindowNotFound means the named window does not exist in the session.
	ErrWindowNotFound = errors.New("tmuxdrv: window not found")

	// ErrInvalidName means a session or window name cannot be used as-is.
	ErrInvalidName = errors.New("tmuxdrv: invalid name")
)

// CommandError reports a tmux invocation that exited non-zero, or that could
// not be started at all. tmux exits 1 for every error it reports, so Stderr is
// the only thing distinguishing "no such session" from "permission denied";
// callers in this package only ever match it against the small set of stable
// message prefixes listed above isAbsence, and only ever to tell "absent" from
// "failed".
type CommandError struct {
	Args     []string // full argv, tmux binary included
	ExitCode int      // -1 when the process could not be started
	Stderr   string   // trimmed
	Err      error    // underlying *exec.ExitError, exec.ErrNotFound, ctx error, ...
}

func (e *CommandError) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("tmuxdrv: %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, msg)
}

func (e *CommandError) Unwrap() error { return e.Err }

func (d *Driver) bin() string {
	if d.Bin != "" {
		return d.Bin
	}
	return "tmux"
}

// globalArgs returns the server-selection flags that precede every tmux
// command. Order matches tmux(1)'s synopsis: tmux [-f file] [-L name] [-S path]
// command [flags].
func (d *Driver) globalArgs() []string {
	args := make([]string, 0, 6)
	if d.Config != "" {
		args = append(args, "-f", d.Config)
	}
	if d.Socket != "" {
		args = append(args, "-L", d.Socket)
	}
	if d.SocketPath != "" {
		args = append(args, "-S", d.SocketPath)
	}
	return args
}

// Argv returns the complete argv for a tmux invocation, binary included.
// Exposed so callers (and tests) can inspect or exec it themselves.
func (d *Driver) Argv(args ...string) []string {
	argv := make([]string, 0, len(args)+7)
	argv = append(argv, d.bin())
	argv = append(argv, d.globalArgs()...)
	return append(argv, args...)
}

// validate reports whether the driver's server selection is coherent.
func (d *Driver) validate() error {
	if d.Socket != "" && d.SocketPath != "" {
		return errors.New("tmuxdrv: Socket (-L) and SocketPath (-S) are mutually exclusive")
	}
	return nil
}

// run executes a tmux command and returns its trimmed stdout.
func (d *Driver) run(ctx context.Context, args ...string) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}

	argv := d.Argv(args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// tmux commands driven this way must never consult a terminal. Leaving
	// Stdin nil gives the child /dev/null, which is what we want: a stray
	// `attach-session` would fail loudly instead of stealing our stdin.

	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\n")
	if err == nil {
		return out, nil
	}

	code := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	}
	return out, &CommandError{
		Args:     argv,
		ExitCode: code,
		Stderr:   strings.TrimSpace(stderr.String()),
		Err:      err,
	}
}

// The tmux messages that mean "the thing you asked about is not there", as
// opposed to "tmux failed". tmux has no localization, so these strings are
// stable across builds and locales.
//
// The groups are deliberately narrow: each call site passes only the spellings
// the command it ran can actually produce, so a genuine failure that merely
// mentions an absent object elsewhere is not swallowed as "nothing to do".
// Every spelling below was observed on the pinned tmux 3.5a from the
// subcommand named beside it, and exists in that binary's string table; four
// entries that earlier versions of this list carried ("no current server",
// "session not found", "no client with tty", "can't establish current
// session") are absent from tmux 3.5a entirely and were dropped rather than
// left as unverifiable widening (see docs/port/SPIKES/tmux.md quirk 5).
var (
	// noServerMessages: any command, when the socket has no live server.
	noServerMessages = []string{
		"no server running on ", // socket file exists, server has exited
		"error connecting to ",  // socket file absent (server never started)
	}
	// sessionAbsentMessages: has-session, new-window, list-windows,
	// list-clients, select-window, kill-session, kill-window, attach-session,
	// switch-client.
	sessionAbsentMessages = []string{
		"can't find session", // session target did not resolve
		"no such session",    // set-option with a session target
	}
	// windowAbsentMessages: select-window, kill-window (and the chained
	// set-option in remainOnExitCommand, which uses a window target).
	windowAbsentMessages = []string{
		"can't find window", // window target did not resolve
		"no such window",    // set-option with a window target
	}
	// clientAbsentMessages: detach-client on a server with nothing attached.
	// tmux reports this even when the *session* target does not resolve, so
	// DetachSession sees it in place of "can't find session".
	clientAbsentMessages = []string{
		"no current client",
	}
	// duplicateSessionMessages: new-session for a name that already exists.
	duplicateSessionMessages = []string{
		"duplicate session:",
	}
)

// isAbsence reports whether err is a tmux error whose stderr carries one of the
// documented "not there" messages. Any other non-zero exit is a real failure.
//
// A message has to *begin a line* of stderr: matching anywhere inside it would
// let an unrelated failure that happens to quote one of these strings be read
// as absence, while anchoring to the very first byte would break on a command
// that starts the server and prints configuration diagnostics first.
func isAbsence(err error, prefixes ...string) bool {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) || cmdErr.ExitCode != 1 {
		return false
	}
	for _, line := range strings.Split(strings.ToLower(cmdErr.Stderr), "\n") {
		line = strings.TrimSpace(line)
		for _, p := range prefixes {
			if strings.HasPrefix(line, strings.ToLower(p)) {
				return true
			}
		}
	}
	return false
}

// isNoServer reports whether err means "there is no tmux server on this socket".
func isNoServer(err error) bool {
	return isAbsence(err, noServerMessages...)
}

// isSessionAbsent folds the two ways a command can report an unresolvable
// session, plus the no-server case that is indistinguishable from it.
func isSessionAbsent(err error) bool {
	return isNoServer(err) || isAbsence(err, sessionAbsentMessages...)
}

// Available reports whether the tmux binary can be found and executed.
// It does not start a server.
func (d *Driver) Available(ctx context.Context) error {
	_, err := d.Version(ctx)
	return err
}

// Version returns the tmux version string, e.g. "tmux 3.5a". `tmux -V` does not
// contact or start a server.
func (d *Driver) Version(ctx context.Context) (string, error) {
	if err := d.validate(); err != nil {
		return "", err
	}
	argv := d.Argv("-V")
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		return "", &CommandError{
			Args:     argv,
			ExitCode: code,
			Stderr:   strings.TrimSpace(stderr.String()),
			Err:      err,
		}
	}
	return strings.TrimSpace(stdout.String()), nil
}
