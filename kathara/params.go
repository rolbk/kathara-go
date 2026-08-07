// This file holds the parameter types of the [Manager] contract: the
// lab-identifier triple, the device/collision-domain filter sets, the `wait`
// union, the `command` union and the `copy_files` mapping. Each of them is a
// place where a Python signature says one thing and the code does another, and
// the register rows that record the gap (NILABILITY.tsv:54-61, ORDERING.tsv
// row `utils.py:452`) are quoted where they bite.

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
//
// The order is the message. `utils.check_required_single_not_none_var` joins
// the kwargs in call order, PEP 468 makes that order deterministic, and
// ORDERING.tsv pins it: the text is "You must specify a parameter among
// lab_hash, lab_name, lab", never any other permutation.
var labRefParamNames = []string{"lab_hash", "lab_name", "lab"}

// LabRef is the `(lab_hash, lab_name, lab)` triple that opens most of
// [Manager] (NILABILITY.tsv:54).
//
// Exactly one field is meant to be set, and the zero value means "the caller
// passed nothing". Which of the two guards applies is per method and not
// per type: the singular getters and the mutating operations require one
// ([LabRef.RequireSingle]), while the plural getters accept none and read it as
// "every scenario of this user" ([LabRef.AtMostOne]). [Manager] says which on
// every method.
//
// Python separates "not provided" (`None`) from "provided but empty" (`""`),
// and the separation is load-bearing in exactly one direction: the *guard*
// counts `is not None`, so `lab_name=""` passes it, while the *dispatch* right
// after is `if lab: … elif lab_name: …`, so `lab_name=""` falls through both
// arms and leaves `lab_hash` as `None` — an unfiltered query where the caller
// asked for one scenario. NILABILITY.tsv:54 resolves this by collapsing the
// two: an empty string here is absent, which loses only that pathological
// path. A caller that means "the scenario whose name is the empty string"
// cannot spell it, and neither could any real 3.8.3 caller.
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

// count is Python's `len([x for x in kwargs.values() if x is not None])` over
// the three fields, with "" reading as absent per NILABILITY.tsv:54.
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
//
// The two failures carry different text and Python tests the "none" case
// first, which is why this is a switch and not two ifs.
//
// The counting is spelled here rather than delegated to
// `util.CheckRequiredSingleNotNoneVar`, which is the general form of the same
// function, because PACKAGE_GRAPH.md §1.2 gives this package no edge to
// `internal/util`. The two must agree; `TestLabRefMessagesMatchUtil` in
// `params_test.go` puts them side by side over all eight shapes of the triple,
// so a drift in either copy fails a test rather than a user's terminal.
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
//
// The nil set and the empty set are NOT the same thing, and which one an empty
// set behaves like flips between the two halves of the API. This is the single
// most dangerous convention in the codebase (SYNTHESIS.md §1.7,
// NILABILITY.tsv:55-57), so it is spelled out on each option field rather than
// here; the type's only job is to keep nil and empty distinguishable, which a
// Go map does and a normalising constructor would not.
//
// Iteration is deliberately not offered. A Go map has no order, and PORT_SPEC
// §10 rejects any `for … range` over a map whose iteration order can reach a
// container.
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
//
// The sort is not a convenience. A backend has to enumerate the set at one
// place — `selected_machines - set(lab.machines.keys())`, the difference that
// feeds `MachineNotFoundError`'s "The following devices are not in the network
// scenario: {…}" (`DockerManager.py:150-155`) — and enumerating a Go map
// directly would put an unordered iteration on a path that reaches a container,
// which PORT_SPEC §10 rejects. Sorting costs nothing here: the only consumers
// are that message, which `kerrors.NewMachineNotFoundSet` sorts anyway, and
// membership decisions, which do not care. Python's own iteration order is
// hash order and reproduces nowhere, so no observable behaviour depended on it.
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
//
// Both filters are read for **truthiness** on this path, so a nil set and an
// empty set both mean "no filter, deploy everything", and the
// "selected and excluded are mutually exclusive" error fires only when both are
// non-empty (NILABILITY.tsv:55). `lstart` passes possibly-empty sets and means
// "all" by it.
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
//
// The filters are read for **is-not-None** on this path, which inverts what an
// empty set means: nil = "no filter, undeploy everything", and a non-nil empty
// set = "matches nothing, undeploy nothing" (NILABILITY.tsv:56-57). `lclean`
// passes nil and means "all" by it. Never normalise one into the other.
//
// The manager-level pre-check that rejects "both selected and excluded" is
// still truthiness, so `undeploy_lab` with two empty sets passes it and then
// trips the identity check inside the machine layer (SYNTHESIS.md §1.7).
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

// WaitPolicy is the `wait: Union[bool, Tuple[int, float]]` parameter of
// `connect_tty`, `connect_tty_obj`, `exec` and `exec_obj`
// (NILABILITY.tsv:60), which decides whether an operation blocks until the
// device's startup commands have finished.
//
// Python's three shapes and their translation
// (`DockerMachine.py:679-690,783-794`):
//
//   - `False` — do not wait. The zero WaitPolicy.
//   - `True` — wait forever, re-checking every second. Python's bool arm
//     hardcodes `retry_interval = 1`, which is why [WaitForever] sets Interval
//     and a hand-built `WaitPolicy{Enabled: true}` does not mean the same
//     thing: with Retries nil and Interval zero that one is an unbounded
//     zero-interval spin, which is `(None, 0.0)` — a shape no Python `wait`
//     value produces, since the bool arm forces the interval to 1 and the
//     tuple arm forces a retry count.
//   - `(n_retries, interval_seconds)` — bounded. [WaitRetries].
//
// The union's fourth shape, "anything else", is `ValueError("Invalid `wait`
// value.")` (`kerrors.ErrInvalidWaitValue`). A Go caller cannot reach it, and
// nothing here manufactures it.
//
// The defaults differ per method and the difference is easy to lose: `wait` is
// `True` for `connect_tty`/`connect_tty_obj` and `False` for `exec`/`exec_obj`.
// The zero value matches `exec`; [DefaultConnectTTYOptions] carries the other.
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
//
// A list is passed to the daemon as-is. A string is split with `shlex` before
// it goes, and it is Kathará itself that splits it, not the SDK
// (`DockerMachine.py:803`, `command = shlex.split(command) if type(command) is
// str else command`; the same at `:673-675` for shells) — a Go backend has to
// do the splitting with shlex semantics of its own, because the Go Docker SDK
// takes a `[]string` and never splits anything. So `NewShellCommand("echo 'a
// b'")` runs `echo` with one argument and `NewCommand("echo 'a b'")` runs
// `echo` with the quotes intact. The CLI produces both: `ExecCommand.py:96` passes its argv
// list through when it holds more than one word and *pops it to a bare string*
// when it holds exactly one, which is how `kathara exec pc1 "ls -la"` comes to
// run two words.
//
// The zero Command is the empty argv list, which no backend accepts.
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
//
// The parameter is a slice and not a map because the archive is written in
// iteration order and duplicate guest paths resolve last-wins on extraction
// (ORDERING.tsv row `utils.py:452`: "API takes ordered pairs"). A Go map has no
// order to preserve.
type CopyEntry struct {
	// GuestPath is the dict key: the destination path inside the device.
	// Backslashes become forward slashes when the tar header is written
	// ("Tar files must have Linux-style paths", `utils.py:443`).
	GuestPath string

	// HostPath is the `str` half of the value union: a path on the host,
	// whose contents get the `convert_win_2_linux` pass (binary sniff, BOM
	// strip, CRLF collapse) on the way in (`utils.py:431`).
	//
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
//
// The zero value is NOT the Python signature default: `wait` defaults to `True`
// on both connect methods. Start from [DefaultConnectTTYOptions] unless you mean
// otherwise — the default is the caller's to apply, and a backend never
// substitutes one, or `wait=False`, which Python accepts here, could not be
// asked for.
type ConnectTTYOptions struct {
	// Shell is `shell`: the shell to run inside the device. Empty falls back
	// to the device's own configured shell — the `shell` container label on
	// Docker, `_MEGALOS_SHELL` on Kubernetes — and then to
	// `Setting.device_shell` (NILABILITY.tsv:58). Python's annotation says
	// `str` and its default is `None`; both spellings reach the same falsy
	// check, so "" is the whole of "unset" here.
	Shell string

	// Logs is `logs`: print the device's startup-command log before handing
	// over the terminal. It is gated a second time by
	// `Setting.print_startup_log` (`DockerMachine.py:701`).
	Logs bool

	// Wait is `wait`. The zero [WaitPolicy] is `wait=False` here as it is
	// on `exec`; what differs is the *signature default*, which is `True` on
	// this method and which [DefaultConnectTTYOptions] is how you get.
	Wait WaitPolicy

	// LogWriter receives the startup-log block described by Logs. Python
	// writes it to `sys.stdout` directly from inside the backend
	// (`DockerMachine.py:717-724`); a Go backend cannot, because stream
	// assignment belongs to the CLI (JSON_CLI_CONTRACT.md §1.3). Nil means
	// os.Stdout, which is the Python behaviour.
	LogWriter io.Writer
}

// DefaultConnectTTYOptions is the signature default of `connect_tty` and
// `connect_tty_obj`: no shell override, no startup logs, and `wait=True`.
func DefaultConnectTTYOptions() ConnectTTYOptions {
	return ConnectTTYOptions{Wait: WaitForever()}
}
