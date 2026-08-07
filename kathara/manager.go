// This file is `foundation/manager/IManager.py`: the thirty-method contract
// both backends implement and the facade proxies. The Python file is pure
// interface plus docstrings, and so is this one — the docstrings are where the
// behaviour actually lives, which is why they are reproduced rather than
// summarised.
//
// Three shape changes the register rows force, each argued at its method:
// `exec`'s `stream` flag becomes two methods (NILABILITY.tsv:61), the stats
// generators become streams whose eagerness matches Python's generator/plain
// split, and `connect_tty` returns the transport instead of running the
// terminal itself (PACKAGE_GRAPH.md D-5).

package kathara

import (
	"context"

	"github.com/KatharaFramework/kathara-go/model"
)

// Manager is the virtualization-backend contract
// (`foundation/manager/IManager.py`), implemented by `backend/docker` and
// `backend/kubernetes` and proxied by [Client].
//
// Every method takes a context.Context first (PORT_SPEC §0.2 #11). Cancelling
// it is the Ctrl-C path: JSON_CLI_CONTRACT.md §6.2 turns a cancelled operation
// into exit 0 with a warning, so an implementation must return promptly and
// must not treat cancellation as a partial-failure condition to retry.
//
// Two of the thirty Python methods are not `@abstractmethod`:
// `disconnect_machine_from_link`, which both real backends override anyway, and
// the static `get_formatted_manager_name`. Go has no such gradation and both
// are ordinary methods here (analysis/manager-foundation.md §7 gotcha 25).
//
// Errors are the `kerrors` taxonomy, matched with errors.Is against the
// aliases in errors.go. Partial failures across a fan-out are joined with
// errors.Join, which errors.Is still sees through (PORT_SPEC §4.3).
type Manager interface {
	// DeployMachine is `deploy_machine`: deploy one device.
	//
	// Errors: [ErrLabNotFound] when the device belongs to no scenario.
	DeployMachine(ctx context.Context, machine *model.Machine) error

	// DeployLink is `deploy_link`: deploy one collision domain.
	//
	// Errors: [ErrLabNotFound] when the collision domain belongs to no
	// scenario.
	DeployLink(ctx context.Context, link *model.Link) error

	// DeployLab is `deploy_lab`: deploy a whole scenario, optionally filtered.
	//
	// The filters are truthiness-tested here — nil and empty both mean "no
	// filter" — which is the opposite of [Manager.UndeployLab]. See
	// [DeployLabOptions].
	//
	// Errors: [ErrInvocation] (`ErrSelectOrExcludeDevices`) when both filters
	// are non-empty.
	DeployLab(ctx context.Context, lab *model.Lab, opts DeployLabOptions) error

	// ConnectMachineToLink is `connect_machine_to_link`: attach a running
	// device to a collision domain.
	//
	// macAddress is `mac_address`: empty lets the network plugin derive one
	// (NILABILITY.tsv:20).
	//
	// Errors: [ErrLabNotFound] for either object, [ErrMachineCollisionDomain]
	// when the device is already attached.
	ConnectMachineToLink(ctx context.Context, machine *model.Machine, link *model.Link, macAddress string) error

	// DisconnectMachineFromLink is `disconnect_machine_from_link`.
	//
	// keepLink is `keep_link`: leave the collision domain deployed even when
	// the departing device was its last member.
	//
	// Errors: [ErrLabNotFound] for either object, [ErrMachineCollisionDomain]
	// when the device is not attached.
	DisconnectMachineFromLink(ctx context.Context, machine *model.Machine, link *model.Link, keepLink bool) error

	// UndeployMachine is `undeploy_machine`.
	//
	// keepLinks is `keep_links`: leave behind the device's collision domains
	// instead of collecting the ones that fall empty.
	//
	// Errors: [ErrLabNotFound] when the device belongs to no scenario.
	UndeployMachine(ctx context.Context, machine *model.Machine, keepLinks bool) error

	// UndeployLink is `undeploy_link`.
	//
	// Errors: [ErrLabNotFound] when the collision domain belongs to no
	// scenario.
	UndeployLink(ctx context.Context, link *model.Link) error

	// UndeployLab is `undeploy_lab`: tear down a running scenario, optionally
	// filtered.
	//
	// ref is required-exactly-one ([LabRef.RequireSingle]). The filters are
	// is-not-None-tested below the manager layer, so a non-nil empty set
	// selects *nothing* — the inverse of [Manager.DeployLab]. See
	// [UndeployLabOptions].
	//
	// Errors: [ErrInvocation] for a bad ref or for both machine filters being
	// non-empty.
	UndeployLab(ctx context.Context, ref LabRef, opts UndeployLabOptions) error

	// Wipe is `wipe`: undeploy every running scenario.
	//
	// allUsers widens it past the current user, which needs root.
	//
	// Errors: [ErrPrivilege] when allUsers is set without root.
	Wipe(ctx context.Context, allUsers bool) error

	// ConnectTTY is `connect_tty`: open an interactive shell on a running
	// device.
	//
	// Python returns None because it runs the terminal loop itself, inside
	// the backend, through `TerminalRunner`. The port splits that in two
	// (PACKAGE_GRAPH.md D-5): the backend owns the transport, which is what
	// comes back here, and `term` owns the UI. The split is forced by the
	// import graph — `term` imports this package, so no backend can import
	// `term`.
	//
	// A nil session with a nil error is the `startup_waited == 2` early
	// return (`DockerMachine.py:698-699`): an APIError while probing the
	// startup commands — the device disappeared out from under the poll —
	// which `_wait_startup_execution` reports as 2 and nothing else does
	// (`DockerMachine.py:951-952`, and its own docstring at :907-908). Python
	// then returns None without raising, so there is nothing to attach to.
	//
	// A user keypress during the wait is *not* this case. It sets
	// `startup_waited` to 0 or 1 and breaks the loop
	// (`DockerMachine.py:935-938`), and Python goes on to attach the shell —
	// so an implementation must still return a session for it. `exec`'s twin
	// site reads the same 2 as [ErrMachineNotRunning]
	// (`DockerMachine.py:800-801`).
	//
	// ref is required-exactly-one. The zero [WaitPolicy] is `wait=False`
	// here as it is everywhere else — no backend remaps it, or `wait=False`
	// would become unreachable on a method Python accepts it on. A caller
	// that wants Python's signature default (`wait=True`) starts from
	// [DefaultConnectTTYOptions], which carries [WaitForever].
	//
	// Errors: [ErrInvocation] for a bad ref, [ErrMachineNotRunning].
	ConnectTTY(ctx context.Context, machineName string, ref LabRef, opts ConnectTTYOptions) (TTYSession, error)

	// ConnectTTYObj is `connect_tty_obj`: [Manager.ConnectTTY] addressed by
	// device object.
	//
	// Errors: [ErrLabNotFound] when the device belongs to no scenario, then
	// whatever [Manager.ConnectTTY] answers.
	ConnectTTYObj(ctx context.Context, machine *model.Machine, opts ConnectTTYOptions) (TTYSession, error)

	// Exec is `exec(..., stream=False)`: run a command to completion and hand
	// back everything it wrote plus its exit code.
	//
	// The Python signature returns `Union[IExecStream, Tuple[bytes, bytes,
	// int]]` and picks by the `stream` flag; NILABILITY.tsv:61 splits it into
	// this method and [Manager.ExecStream] rather than returning an `any`
	// nothing type-checks. Output stays bytes the whole way — no decoding
	// happens at this layer.
	//
	// ref is required-exactly-one.
	//
	// Errors: [ErrInvocation] for a bad ref, [ErrMachineNotRunning],
	// [ErrMachineBinary] when the command's binary is missing. Python has two
	// ways of detecting that and only one of them is exclusive to this
	// method: `exec_start` itself can fail with an OCI-runtime APIError
	// (`DockerMachine.py:867-876`), which happens on both paths, while the
	// scan of the collected output for the same pattern
	// (`DockerMachine.py:879-890` — over *stdout*, and only when the exit
	// code is non-zero) needs the whole output and so runs here only. See
	// [Manager.ExecStream].
	Exec(ctx context.Context, machineName string, command Command, ref LabRef, wait WaitPolicy) (stdout, stderr []byte, exitCode int, err error)

	// ExecObj is `exec_obj(..., stream=False)`: [Manager.Exec] addressed by
	// device object.
	//
	// Errors: [ErrLabNotFound] when the device belongs to no scenario, then
	// whatever [Manager.Exec] answers.
	ExecObj(ctx context.Context, machine *model.Machine, command Command, wait WaitPolicy) (stdout, stderr []byte, exitCode int, err error)

	// ExecStream is `exec(..., stream=True)`: run a command and read its
	// output as it arrives. See [ExecStream] for the read contract and
	// [Manager.Exec] for the union this half comes from.
	//
	// ref is required-exactly-one.
	//
	// Errors: [ErrInvocation] for a bad ref, [ErrMachineNotRunning], and
	// [ErrMachineBinary] — this path reaches it too, through the
	// `exec_start` APIError branch (`DockerMachine.py:867-876`); what it
	// cannot reach is the output scan, which needs an output it never
	// collects.
	ExecStream(ctx context.Context, machineName string, command Command, ref LabRef, wait WaitPolicy) (ExecStream, error)

	// ExecStreamObj is `exec_obj(..., stream=True)`: [Manager.ExecStream]
	// addressed by device object.
	ExecStreamObj(ctx context.Context, machine *model.Machine, command Command, wait WaitPolicy) (ExecStream, error)

	// CopyFiles is `copy_files`: push files into a running device.
	//
	// The device must be deployed — [model.Machine.APIObject] is what the
	// backend writes through. files is ordered; see [CopyEntry].
	CopyFiles(ctx context.Context, machine *model.Machine, files []CopyEntry) error

	// RetrieveFiles is `retrieve_files`: pull src out of a running device
	// into dst on the host.
	RetrieveFiles(ctx context.Context, machine *model.Machine, src, dst string) error

	// GetMachineAPIObject is `get_machine_api_object`: the backend's own
	// handle for one running device — a Docker container or a Kubernetes pod.
	//
	// ref is required-exactly-one. allUsers widens the search past the
	// current user.
	//
	// Errors: [ErrInvocation] for a bad ref, [ErrMachineNotFound].
	GetMachineAPIObject(ctx context.Context, machineName string, ref LabRef, allUsers bool) (any, error)

	// GetMachinesAPIObjects is `get_machines_api_objects`: the handles for
	// every matching running device.
	//
	// ref is at-most-one here, and an empty ref means every scenario of the
	// selected users rather than an error.
	GetMachinesAPIObjects(ctx context.Context, ref LabRef, allUsers bool) ([]any, error)

	// GetLinkAPIObject is `get_link_api_object`: the backend's handle for one
	// deployed collision domain.
	//
	// ref is required-exactly-one.
	//
	// Errors: [ErrInvocation] for a bad ref, [ErrLinkNotFound].
	GetLinkAPIObject(ctx context.Context, linkName string, ref LabRef, allUsers bool) (any, error)

	// GetLinksAPIObjects is `get_links_api_objects`. ref is at-most-one, as
	// on [Manager.GetMachinesAPIObjects].
	GetLinksAPIObjects(ctx context.Context, ref LabRef, allUsers bool) ([]any, error)

	// GetLabFromAPI is `get_lab_from_api`: rebuild a [model.Lab] from what is
	// actually running.
	//
	// This one does not take a [LabRef], and the difference is not cosmetic.
	// Its guard is `if not lab_hash and not lab_name`
	// (`DockerManager.py:692`), a truthiness test rather than the
	// `is not None` count the rest of the interface uses, so here an empty
	// string genuinely is absent; and when both are given, lab_name wins
	// rather than erroring (NILABILITY.tsv:65). A scenario addressed by hash
	// comes back named "reconstructed_lab" with the hash forced onto it,
	// because the name that produced the hash is not recoverable.
	//
	// Errors: [ErrInvocation] (`ErrLabHashOrName`) when both are empty.
	GetLabFromAPI(ctx context.Context, labHash, labName string) (*model.Lab, error)

	// UpdateLabFromAPI is `update_lab_from_api`: refresh an existing
	// [model.Lab] in place from the running deployment.
	UpdateLabFromAPI(ctx context.Context, lab *model.Lab) error

	// GetMachinesStats is `get_machines_stats`: a stream of inventory
	// snapshots, one per step.
	//
	// ref is at-most-one, and the check runs *now* — the Python method holds
	// no `yield`, so it validates and then returns the generator its machine
	// layer built (`DockerManager.py:843`). [Manager.GetMachineStats], which
	// does hold a `yield`, behaves differently on purpose. Oracle-probed:
	// `DockerManager.get_machines_stats(lab_hash="h", lab_name="n", lab=lab)`
	// raises at the call, its singular sibling at the first `next()`.
	//
	// machineName filters by device name; empty is no filter
	// (NILABILITY.tsv:59). allUsers needs root.
	//
	// Errors: [ErrInvocation] for a bad ref. [ErrPrivilege] surfaces from the
	// stream's first step, not from here, because the generator that raises
	// it has not run yet.
	GetMachinesStats(ctx context.Context, ref LabRef, machineName string, allUsers bool) (MachinesStatsStream, error)

	// GetMachineStats is `get_machine_stats`: a one-element stream carrying
	// the inventory of a single device.
	//
	// It returns no error, and that is the faithful shape rather than an
	// oversight. `get_machine_stats` is a Python *generator function* — its
	// body holds `yield` (`DockerManager.py:908,910`,
	// `KubernetesManager.py:815,817`) — so calling it runs nothing at all:
	// the `check_required_single_not_none_var` at its top, and every failure
	// under it, surface at the first `next()`. Returning an error here would
	// move an observable error earlier. Call [MachineStatsStream.Next] and
	// read it there.
	//
	// The Docker one additionally carries `@privileged`, and
	// `inspect.isgeneratorfunction` therefore reports False for it — the
	// wrapper is an ordinary function. It does not change the timing: the
	// wrapper calls the generator function, which returns its generator
	// unrun, so the guard still fires at the first `next()`. Oracle-probed on
	// both backends.
	//
	// ref is required-exactly-one.
	GetMachineStats(ctx context.Context, machineName string, ref LabRef, allUsers bool) MachineStatsStream

	// GetMachineStatsObj is `get_machine_stats_obj`: [Manager.GetMachineStats]
	// addressed by device object.
	//
	// This one *does* return an error, for the same reason the other does
	// not: its Python body holds no `yield`, so its `if not machine.lab`
	// guard runs at call time (`DockerManager.py:929`).
	//
	// Errors: [ErrLabNotFound] when the device belongs to no scenario.
	GetMachineStatsObj(ctx context.Context, machine *model.Machine, allUsers bool) (MachineStatsStream, error)

	// GetLinksStats is `get_links_stats`: the collision-domain twin of
	// [Manager.GetMachinesStats], with the same eager check.
	//
	// linkName filters by collision-domain name; empty is no filter.
	GetLinksStats(ctx context.Context, ref LabRef, linkName string, allUsers bool) (LinksStatsStream, error)

	// GetLinkStats is `get_link_stats`: the collision-domain twin of
	// [Manager.GetMachineStats], lazy for the same reason.
	GetLinkStats(ctx context.Context, linkName string, ref LabRef, allUsers bool) LinkStatsStream

	// GetLinkStatsObj is `get_link_stats_obj`, eager for the same reason
	// [Manager.GetMachineStatsObj] is.
	//
	// Errors: [ErrLabNotFound] when the collision domain belongs to no
	// scenario.
	GetLinkStatsObj(ctx context.Context, link *model.Link, allUsers bool) (LinkStatsStream, error)

	// CheckImage is `check_image`: verify that an image name is usable.
	//
	// Errors: [ErrConnection] when the image is not local and no registry can
	// be reached, [ErrDockerImageNotFound] when it does not exist,
	// [ErrInvalidImageArchitecture] when it cannot run on this host.
	CheckImage(ctx context.Context, imageName string) error

	// GetReleaseVersion is `get_release_version`: the backend runtime's own
	// version — the Docker engine's, the Kubernetes cluster's.
	GetReleaseVersion(ctx context.Context) (string, error)

	// GetFormattedManagerName is `get_formatted_manager_name`: the display
	// name, "Docker (Kathara)" or "Kubernetes (Megalos)".
	//
	// Python declares it static and reads it off the *class*, without
	// constructing a manager, which is how `Kathara.get_available_managers_name`
	// lists the backends without connecting to a daemon. Go has no static
	// methods, so the same string is also declared on [Backend.FormattedName];
	// that is the copy [Registry] answers with, and this one is for a caller
	// that already holds a Manager.
	GetFormattedManagerName() string
}
