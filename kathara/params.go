package kathara

import (
	"io"
	"slices"
	"time"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// labRefParamNames is `', '.join(kwargs.keys())` for every
// `check_*_not_none_var(lab_hash=…, lab_name=…, lab=…)` call in the two
// backends — eleven sites in `DockerManager.py` (:335, :397, :468, :564, :600,
// :631, :667, :866, :901, :958, :991) and eleven in `KubernetesManager.py`
// (:281, :397, :465, :558, :591, :625, :658, :768, :803, :859, :894).
var labRefParamNames = []string{"lab_hash", "lab_name", "lab"}

type LabRef struct {
	// Hash is `lab_hash`: the scenario hash, already computed.
	Hash string

	// Name is `lab_name`: the scenario name, which the backend turns into a
	// hash with `utils.generate_urlsafe_hash` (`DockerManager.py:472`). That
	// function lives in `internal/util`, which this package does not import,
	// so resolution is the backend's step and not a method here.
	Name string

	// Lab is `lab`: the scenario object, whose [model.Lab.Hash] the backend
	// reads directly.
	Lab *model.Lab
}

func (r LabRef) count() int {
	n := 0
	if r.Hash != "" {
		n++
	}
	if r.Name != "" {
		n++
	}
	if r.Lab != nil {
		n++
	}
	return n
}

// RequireSingle is `check_required_single_not_none_var(lab_hash=…, lab_name=…,
// lab=…)` (`utils.py:117`): exactly one of the three must be set.
func (r LabRef) RequireSingle() error {
	switch n := r.count(); {
	case n == 0:
		return kerrors.NewOneParameter(labRefParamNames)
	case n > 1:
		return kerrors.NewOnlyOneParameter(labRefParamNames)
	}
	return nil
}

// AtMostOne is `check_single_not_none_var(lab_hash=…, lab_name=…, lab=…)`
// (`utils.py:110`): at most one of the three may be set, and none at all is
// fine — on the plural getters that is "every scenario of this user"
// (`DockerManager.py:600,667,866,958`).
func (r LabRef) AtMostOne() error {
	if r.count() > 1 {
		return kerrors.NewOnlyOneParameter(labRefParamNames)
	}
	return nil
}

// NameSet is Python's `Optional[Set[str]]` — the device and collision-domain
// filters of `deploy_lab` and `undeploy_lab`.
type NameSet map[string]struct{}

// NewNameSet builds a set from the given names. It never returns nil, so
// `NewNameSet()` is the *empty* set — "undeploy nothing" on the undeploy path
// and "deploy everything" on the deploy path. For "no filter" on both paths,
// pass a nil NameSet.
func NewNameSet(names ...string) NameSet {
	s := make(NameSet, len(names))
	for _, name := range names {
		s[name] = struct{}{}
	}
	return s
}

// Has reports membership. It is safe on a nil NameSet, which has no members —
// callers must test the set itself for nil-ness where nil means "no filter".
func (s NameSet) Has(name string) bool {
	_, ok := s[name]
	return ok
}

// Names is the members, sorted, as a fresh slice.
func (s NameSet) Names() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// DeployLabOptions is the tail of `deploy_lab(lab, selected_machines,
// excluded_machines)`.
type DeployLabOptions struct {
	// SelectedMachines is `selected_machines`: deploy only these devices.
	// Nil or empty = all.
	SelectedMachines NameSet

	// ExcludedMachines is `excluded_machines`: deploy everything but these.
	// Nil or empty = exclude nothing.
	ExcludedMachines NameSet
}

// UndeployLabOptions is the tail of `undeploy_lab(lab_hash, lab_name, lab,
// selected_machines, excluded_machines, selected_links)`.
type UndeployLabOptions struct {
	// SelectedMachines is `selected_machines`: undeploy only these devices.
	// Nil = all; empty = none.
	SelectedMachines NameSet

	// ExcludedMachines is `excluded_machines`: undeploy everything but these.
	// Nil = exclude nothing; empty = exclude nothing.
	ExcludedMachines NameSet

	// SelectedLinks is `selected_links`: undeploy only these collision
	// domains. Nil = all the scenario's; empty = none.
	SelectedLinks NameSet
}

type WaitPolicy struct {
	// Enabled is Python's `should_wait`.
	Enabled bool

	// Retries is the tuple's first element, nil for "retry forever"
	// (Python's `n_retries = None`). It is meaningless when Enabled is false.
	Retries *int

	// Interval is the tuple's second element, the sleep between checks.
	Interval time.Duration
}

// NoWait is `wait=False`, the default of `exec` and `exec_obj`. It is the zero
// WaitPolicy and exists so a call site can say so out loud.
func NoWait() WaitPolicy { return WaitPolicy{} }

// WaitForever is `wait=True`, the default of `connect_tty` and
// `connect_tty_obj`: block until the startup commands finish, re-checking every
// second (`DockerMachine.py:686-688`).
func WaitForever() WaitPolicy {
	return WaitPolicy{Enabled: true, Interval: time.Second}
}

// WaitRetries is `wait=(retries, interval)`: check at most retries times,
// sleeping interval in between. A retries of zero is the tuple `(0, …)`, which
// Python accepts and which means "check once, do not retry".
func WaitRetries(retries int, interval time.Duration) WaitPolicy {
	return WaitPolicy{Enabled: true, Retries: &retries, Interval: interval}
}

// Command is the `command: Union[List[str], str]` parameter of `exec` and
// `exec_obj`. The two shapes are not interchangeable and the difference is
// observable, so the union survives instead of being flattened.
type Command struct {
	argv  []string
	line  string
	shell bool
}

// NewCommand is the `List[str]` shape: the words as given, no splitting.
func NewCommand(argv ...string) Command { return Command{argv: argv} }

// NewShellCommand is the `str` shape: one string the backend word-splits the
// way `shlex.split` would.
func NewShellCommand(line string) Command { return Command{line: line, shell: true} }

// Argv returns the words of a [NewCommand] value. The second result is false
// when the value is a [NewShellCommand] one, which has no words yet.
func (c Command) Argv() ([]string, bool) {
	if c.shell {
		return nil, false
	}
	return c.argv, true
}

// Line returns the unsplit string of a [NewShellCommand] value. The second
// result is false for a [NewCommand] one.
func (c Command) Line() (string, bool) {
	if !c.shell {
		return "", false
	}
	return c.line, true
}

// CopyEntry is one pair of `copy_files`' `guest_to_host: Dict[str, Union[str,
// io.IOBase]]` — the mapping whose keys are *guest* paths and whose values say
// where the bytes come from on the host, despite the parameter's name reading
// the other way round.
type CopyEntry struct {
	// GuestPath is the dict key: the destination path inside the device.
	// Backslashes become forward slashes when the tar header is written
	// ("Tar files must have Linux-style paths", `utils.py:443`).
	GuestPath string

	// HostPath is the `str` half of the value union: a path on the host,
	// whose contents get the `convert_win_2_linux` pass (binary sniff, BOM
	// strip, CRLF collapse) on the way in (`utils.py:431`).
	// Exactly one of HostPath and Content is set. When both are, Content
	// wins — a choice with no Python original to match, because
	// `pack_file_for_tar`'s `isinstance` ladder (`utils.py:430-434`) tests
	// `str` before `io.IOBase` on a value that cannot be both, so its order
	// carries no information about precedence. The state is unreachable from
	// any faithful caller; the rule is here so that two backends cannot
	// disagree about it.
	HostPath string

	// Content is the `io.IOBase` half: the bytes themselves, shipped
	// verbatim with no newline conversion (`utils.py:435`).
	Content io.Reader
}

// ConnectTTYOptions is the tail of `connect_tty(machine_name, lab_hash,
// lab_name, lab, shell, logs, wait)`.
type ConnectTTYOptions struct {
	// Shell is `shell`: the shell to run inside the device.
	Shell string

	// Logs is `logs`: print the device's startup-command log before handing
	// over the terminal. It is gated a second time by
	// `Setting.print_startup_log` (`DockerMachine.py:701`).
	Logs bool

	// Wait is `wait`. The zero [WaitPolicy] is `wait=False` here as it is
	// on `exec`; what differs is the *signature default*, which is `True` on
	// this method and which [DefaultConnectTTYOptions] is how you get.
	Wait WaitPolicy

	// LogWriter receives the startup-log block described by Logs.
	LogWriter io.Writer
}

// DefaultConnectTTYOptions is the signature default of `connect_tty` and
// `connect_tty_obj`: no shell override, no startup logs, and `wait=True`.
func DefaultConnectTTYOptions() ConnectTTYOptions {
	return ConnectTTYOptions{Wait: WaitForever()}
}
