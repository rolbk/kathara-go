// This file is `DockerManager.py`: the [kathara.Manager] surface, the
// lab-identifier resolution every method starts with, and the constructor's
// side effects.

package docker

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/docker/docker/client"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// BackendName is the `manager_type` value that selects this backend. It is
// compared exactly and case-sensitively ([kathara.Backend.Name]).
const BackendName = "docker"

// FormattedName is `get_formatted_manager_name` (`DockerManager.py:1051`).
const FormattedName = "Docker (Kathara)"

// Backend is the registry row `cmd/kathara` registers.
//
// There is no `init()` that registers it: registration order is the declared
// docker → kubernetes order and import order is not a declared order
// (PACKAGE_GRAPH.md §1.2), and the `nok8s` build leaves `client-go` out of the
// binary by simply not naming the other backend.
func Backend() kathara.Backend {
	return kathara.Backend{
		Name:          BackendName,
		FormattedName: FormattedName,
		New:           New,
	}
}

// Manager is `DockerManager` (`DockerManager.py:57`).
//
// Its four Python slots — `client`, `docker_image`, `docker_machine`,
// `docker_link` — are here, plus the two things PORT_SPEC §0.2 #10 took away
// from the constructor and made parameters: the settings and the event
// dispatcher.
//
// It is safe for concurrent use to the extent Python's was: the Docker client
// is shared across the fan-out goroutines, as docker-py's was across pool
// threads, and the event dispatcher does its own locking.
type Manager struct {
	api        *client.Client
	settings   *settings.Settings
	dispatcher *event.Dispatcher
	defaults   model.Defaults

	image   *imageService
	machine *machineService
	link    *linkService
	plugin  *pluginService
}

var _ kathara.Manager = (*Manager)(nil)

// New is `DockerManager.__init__` wrapped in `check_docker_status`
// (`DockerManager.py:35,61`), and the ORDER of its side effects is the contract
// — PORT_SPEC §9's goldens encode when a daemon-down error appears relative to
// the rest of a command (analysis/manager-foundation.md §7 gotcha 9):
//
//  1. build the client — local socket, or the remote URL with a CA-verified
//     TLS config;
//  2. check, install, enable and reconfigure the network plugin;
//  3. read the engine version (`DockerMachine.__init__`);
//  4. PING — the decorator wraps the constructor and pings AFTER it returns, so
//     a daemon that has gone away is reported after the plugin work, not
//     before.
//
// Step 4 reading last is not an accident of the decorator: `check_docker_status`
// calls `method(*args)` first and `client.ping()` second (`:41,48`). A reader
// who expects the ping to gate everything will be surprised by a plugin error
// arriving from a dead daemon, and so will a golden.
//
// # max_pool_size
//
// Python passes `max_pool_size=cpu_count()` so the HTTP connection pool matches
// the worker count. Go's `http.Transport` does not cap concurrent connections —
// only idle ones — so the equivalent is `MaxIdleConnsPerHost`, set below for
// the same reason: without it the default of 2 makes the fan-out reopen a
// connection per request. Neither setting is observable in the API's answers.
//
// Errors: [kerrors.ErrDaemonConnection] for a client that cannot be built or a
// daemon that will not answer, then whatever the plugin check says.
func New(ctx context.Context, cfg kathara.Config) (kathara.Manager, error) {
	api, err := newAPIClient(cfg.Settings)
	if err != nil {
		return nil, kerrors.NewDaemonConnection(err)
	}

	m := &Manager{
		api:        api,
		settings:   cfg.Settings,
		dispatcher: cfg.Dispatcher,
		defaults:   cfg.Defaults,
	}

	architecture, err := util.GetArchitecture()
	if err != nil {
		return nil, err
	}
	m.plugin = &pluginService{
		manager:     m,
		currentName: NetworkDriver(pluginNameOf(cfg.Settings), architecture),
	}
	if err := m.plugin.CheckAndDownload(ctx); err != nil {
		return nil, err
	}

	m.image = &imageService{manager: m}

	// `DockerMachine.__init__`'s `client.version()` — the FIRST of the two the
	// constructor makes; `get_release_version` makes the second on demand.
	version, err := api.ServerVersion(ctx)
	if err != nil {
		return nil, kerrors.NewDaemonConnection(err)
	}
	m.machine = &machineService{manager: m, engineVersion: util.ParseDockerEngineVersion(version.Version)}
	m.link = &linkService{manager: m}

	// `check_docker_status`'s ping, after the constructor body.
	if _, err := api.Ping(ctx); err != nil {
		return nil, kerrors.NewDaemonConnection(err)
	}

	return m, nil
}

// newAPIClient is the `docker.from_env` / `docker.DockerClient` fork of
// `DockerManager.__init__` (`DockerManager.py:63-73`).
//
// `timeout=None` is "no client-side timeout", which is the default for the Go
// SDK's transport too — a `docker exec` on a long-running command must not be
// cut off by a client deadline, and the context is what bounds an operation
// now (PORT_SPEC §0.2 #11).
//
// The remote fork builds `TLSConfig(ca_cert=Setting.cert_path)`: server
// verification against that CA, with no client certificate. A nil `cert_path`
// with a non-nil `remote_url` is legal in the schema and gives docker-py a
// TLSConfig with `verify=None`, i.e. the system roots.
func newAPIClient(s *settings.Settings) (*client.Client, error) {
	// ORDER IS LOAD-BEARING. `WithHTTPClient` REPLACES the client wholesale,
	// while `WithHost`, `FromEnv` and `WithTLSClientConfig` all reach into the
	// existing `*http.Transport` — `WithHost` to install the unix-socket or
	// npipe dialer through `sockets.ConfigureTransport`, the other two to
	// install TLS. So the pool-sized client goes FIRST and everything that
	// configures a transport goes after it; the reverse order silently discards
	// the dialer and leaves a client that cannot reach a local daemon at all.
	opts := []client.Opt{
		client.WithHTTPClient(&http.Client{Transport: &http.Transport{
			MaxIdleConnsPerHost: util.PoolSize(),
		}}),
	}

	if s.RemoteURL == nil {
		// `WithHost` runs `sockets.ConfigureTransport`, which installs the
		// unix-socket (or npipe) dialer on the pool-sized transport above.
		// `FromEnv` alone is a no-op when DOCKER_HOST is unset and would leave
		// the transport dialing TCP to the socket path. Explicit default first;
		// FromEnv still overrides it when DOCKER_HOST is set (docker-py
		// `from_env` parity).
		opts = append(opts, client.WithHost(client.DefaultDockerHost), client.FromEnv)
	} else {
		opts = append(opts, client.WithHost(*s.RemoteURL))
		if s.CertPath != nil {
			opts = append(opts, client.WithTLSClientConfig(*s.CertPath, "", ""))
		}
	}

	opts = append(opts, client.WithAPIVersionNegotiation())
	return client.NewClientWithOpts(opts...)
}

// GetFormattedManagerName is `get_formatted_manager_name`.
func (m *Manager) GetFormattedManagerName() string { return FormattedName }

// GetReleaseVersion is `get_release_version` (`DockerManager.py:1042`):
// `client.version()["Version"]`, raw — NOT run through
// `parse_docker_engine_version`, so a build suffix survives into what the CLI
// prints.
func (m *Manager) GetReleaseVersion(ctx context.Context) (string, error) {
	version, err := m.api.ServerVersion(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

// ---------------------------------------------------------------------------
// Lab-identifier resolution
// ---------------------------------------------------------------------------

// resolveRequired is the two lines that open eleven methods
// (`DockerManager.py:335-339` and its ten twins):
//
//	check_required_single_not_none_var(lab_hash=…, lab_name=…, lab=…)
//	if lab: lab_hash = lab.hash
//	elif lab_name: lab_hash = generate_urlsafe_hash(lab_name)
//
// The guard counts non-None values; the dispatch below it is a truthiness
// chain. Python can therefore pass the guard with `lab_name=""` and fall
// through both arms, leaving `lab_hash` as None — an unfiltered query where the
// caller asked for one scenario. [kathara.LabRef] collapses "" into "absent"
// (NILABILITY.tsv:54), which loses only that path.
func resolveRequired(ref kathara.LabRef) (string, error) {
	if err := ref.RequireSingle(); err != nil {
		return "", err
	}
	return resolveHash(ref), nil
}

// resolveAtMostOne is the same pair with `check_single_not_none_var`
// (`DockerManager.py:600,667,866,958`): none at all is legal and means "every
// scenario of the selected users".
func resolveAtMostOne(ref kathara.LabRef) (string, error) {
	if err := ref.AtMostOne(); err != nil {
		return "", err
	}
	return resolveHash(ref), nil
}

// resolveHash is the `if lab: … elif lab_name: …` dispatch, in Python's order:
// the object wins over the name, and the name is hashed with the one function
// that names everything (SYNTHESIS §1.1).
func resolveHash(ref kathara.LabRef) string {
	switch {
	case ref.Lab != nil:
		return ref.Lab.Hash
	case ref.Name != "":
		return util.GenerateURLSafeHash(ref.Name)
	default:
		return ref.Hash
	}
}

// scopedUser is `utils.get_current_user_name() if not all_users else None`,
// the eight-site idiom that turns the `all_users` flag into a label filter.
// The empty string is Python's None: [ObjectFilters] drops a falsy user.
//
// The error is the identity chain's own — a passwd lookup that fails, or a
// hostname the platform will not report — which Python has no way to raise
// because `platform.node()` and `pwd.getpwuid` do not fail there.
func scopedUser(allUsers bool) (string, error) {
	if allUsers {
		return "", nil
	}
	return util.GetCurrentUserName()
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// DeployMachine is `deploy_machine` (`DockerManager.py:83`).
//
// It deploys the device's collision domains first and the device second, which
// is the same links-then-machines order [Manager.DeployLab] uses. The link set
// is built from the device's interfaces, INCLUDING tombstones — Python's
// `{x.link.name for x in machine.interfaces.values()}` dereferences `.link` on
// every slot and dies on a removed one, which is reproduced.
func (m *Manager) DeployMachine(ctx context.Context, machine *model.Machine) error {
	if machine.Lab == nil {
		return kerrors.NewLabNotFoundDevice(machine.Name)
	}

	if err := machine.Check(); err != nil {
		return err
	}

	links, err := interfaceLinkNames(machine)
	if err != nil {
		return err
	}
	if err := m.link.DeployLinks(ctx, machine.Lab, links, nil); err != nil {
		return err
	}
	return m.machine.DeployMachines(ctx, machine.Lab, kathara.NewNameSet(machine.Name), nil)
}

// interfaceLinkNames is `{x.link.name for x in machine.interfaces.values()}`,
// the set comprehension `deploy_machine` and `undeploy_machine` both build
// (`DockerManager.py:103,289`).
//
// A tombstone — the slot `remove_interface` leaves behind — has no `.link`, and
// Python's comprehension has no guard, so it crashes with
// `AttributeError: 'NoneType' object has no attribute 'link'`. Reproduced
// rather than skipped: the set is what decides which collision domains are
// deployed, and silently dropping a slot would deploy a different scenario.
func interfaceLinkNames(machine *model.Machine) (kathara.NameSet, error) {
	names := kathara.NewNameSet()
	for _, iface := range machine.Interfaces() {
		if iface.IsTombstone() {
			return nil, newPyAttributeError("link")
		}
		names[iface.Link.Name] = struct{}{}
	}
	return names, nil
}

// DeployLink is `deploy_link` (`DockerManager.py:106`).
func (m *Manager) DeployLink(ctx context.Context, link *model.Link) error {
	if link.Lab == nil {
		return kerrors.NewLabNotFoundCollisionDomain(link.Name)
	}
	return m.link.DeployLinks(ctx, link.Lab, kathara.NewNameSet(link.Name), nil)
}

// DeployLab is `deploy_lab` (`DockerManager.py:124`).
//
// The order below is the order errors surface in, and it is observable:
//
//  1. `lab.check_integrity()` — before the filters are even looked at, so a
//     scenario with a non-sequential interface fails the same way whatever was
//     selected;
//  2. selected-and-excluded;
//  3. selected names not in the scenario, then excluded names not in it, each
//     naming the offending set;
//  4. narrow the collision domains, then deploy links, then machines.
//
// # The link narrowing
//
// `selected_machines` narrows to the collision domains those devices touch.
// `excluded_machines` computes the domains of the REMAINING devices and
// subtracts them from the excluded devices' domains, so a collision domain
// shared with a device that is still being deployed survives — only domains
// used exclusively by excluded devices are dropped.
//
// Errors: [kerrors.ErrSelectOrExcludeDevices], [kerrors.ErrMachineNotFound]
// naming the set (sorted here, where Python's set repr is hash-ordered —
// ORDERING.tsv rows 52-53).
func (m *Manager) DeployLab(ctx context.Context, lab *model.Lab, opts kathara.DeployLabOptions) error {
	if err := lab.CheckIntegrity(); err != nil {
		return err
	}

	selected, excluded := opts.SelectedMachines, opts.ExcludedMachines
	if len(selected) > 0 && len(excluded) > 0 {
		return kerrors.ErrSelectOrExcludeDevices
	}

	if len(selected) > 0 && !lab.HasMachines(selected.Names()) {
		return kerrors.NewMachineNotFoundSet(missingMachines(lab, selected))
	}
	if len(excluded) > 0 && !lab.HasMachines(excluded.Names()) {
		return kerrors.NewMachineNotFoundSet(missingMachines(lab, excluded))
	}

	var selectedLinks, excludedLinks kathara.NameSet
	if len(selected) > 0 {
		links, err := lab.GetLinksFromMachines(selected.Names())
		if err != nil {
			return err
		}
		selectedLinks = links
	}
	if len(excluded) > 0 {
		remaining := make([]string, 0, len(lab.MachineNames()))
		for _, name := range lab.MachineNames() {
			if !excluded.Has(name) {
				remaining = append(remaining, name)
			}
		}
		runningLinks, err := lab.GetLinksFromMachines(remaining)
		if err != nil {
			return err
		}
		excludedOnly, err := lab.GetLinksFromMachines(excluded.Names())
		if err != nil {
			return err
		}
		for name := range runningLinks {
			delete(excludedOnly, name)
		}
		excludedLinks = excludedOnly
	}

	if err := m.link.DeployLinks(ctx, lab, selectedLinks, excludedLinks); err != nil {
		return err
	}
	return m.machine.DeployMachines(ctx, lab, selected, excluded)
}

// missingMachines is `selected_machines - set(lab.machines.keys())`, the
// difference the MachineNotFound message names.
//
// Python interpolates a `set`, whose repr order is hash-randomised; ORDERING.tsv
// rows 52-53 rule the port sorts, and `kerrors.NewMachineNotFoundSet` does the
// sorting itself (ERROR_CODES.md §0.2).
func missingMachines(lab *model.Lab, names kathara.NameSet) []string {
	missing := make([]string, 0, len(names))
	for _, name := range names.Names() {
		if !lab.HasMachine(name) {
			missing = append(missing, name)
		}
	}
	return missing
}

// ---------------------------------------------------------------------------
// Connect / disconnect
// ---------------------------------------------------------------------------

// ConnectMachineToLink is `connect_machine_to_link` (`DockerManager.py:177`).
//
// The guards run in Python's order and the middle one is a LIVE reload: the
// device's cached api_object is refreshed before its status is compared to
// "running", so a container that exited since the scenario was loaded is
// reported as not running rather than connected to.
//
// # Interface numbering on a bridged device
//
// `add_interface` would auto-assign `len(interfaces)`, which on a bridged
// device collides with the slot the bridge occupies. So the number is computed
// here instead (`:213-220`):
//
//	bridged_iface absent from meta → read it back off the container LABEL
//	no interfaces, or bridged_iface above every interface number
//	                              → bridged_iface + 1
//	otherwise                     → max(interface numbers) + 1
//
// which is what makes successive connections on a bridged device land at 1, 2,
// … and at 2 when the device already had an eth0.
//
// Errors: [kerrors.ErrLabNotFound] for either object,
// [kerrors.ErrMachineNotRunning], [kerrors.ErrMachineCollisionDomain] when the
// device is already attached.
func (m *Manager) ConnectMachineToLink(ctx context.Context, machine *model.Machine, link *model.Link, macAddress string) error {
	if machine.Lab == nil {
		return kerrors.NewLabNotFoundDevice(machine.Name)
	}

	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return kerrors.NewMachineNotRunning(machine.Name)
	}
	if err := reloadContainer(ctx, m.api, machineContainer); err != nil {
		return err
	}
	if machineContainer.Status() != "running" {
		return kerrors.NewMachineNotRunning(machine.Name)
	}

	if link.Lab == nil {
		return kerrors.NewLabNotFoundCollisionDomain(link.Name)
	}
	if link.HasMachine(machine.Name) {
		return kerrors.NewManagerMachineAlreadyConnected(machine.Name, link.Name)
	}

	var ifaceNumber *int
	if machine.IsBridged() {
		if !machine.Meta.BridgedIface.IsSet() {
			// `int(machine.api_object.labels['bridged_iface'])` — the label is
			// a string and `add_meta` stores the int, which is what makes the
			// arithmetic below work. The label is INDEXED, so an absent one is
			// a KeyError; only a malformed one reaches `int()`'s ValueError.
			raw, ok := machineContainer.Labels()[labelBridgedIface]
			if !ok {
				return newPyKeyError(labelBridgedIface)
			}
			number, err := pyInt(raw)
			if err != nil {
				return err
			}
			machine.Meta.BridgedIface = model.Int(int64(number))
		}

		slots := machine.Interfaces()
		bridged, isInt := bridgedIfaceNumber(machine)
		if !isInt {
			// The meta is a string, which is all lab.conf can store
			// (DIVERGENCES.md 1). Python then compares it to an int, or adds an
			// int to it, and dies either way — with a different message per
			// branch, which is why the branch is taken before the failure.
			if len(slots) == 0 {
				return newPyTypeError(`can only concatenate str (not "int") to str`)
			}
			return newPyTypeError("'>' not supported between instances of 'str' and 'int'")
		}

		highest := 0
		if len(slots) > 0 {
			highest = slots[0].Number
			for _, iface := range slots[1:] {
				highest = max(highest, iface.Number)
			}
		}
		next := highest + 1
		if len(slots) == 0 || bridged > highest {
			next = bridged + 1
		}
		ifaceNumber = &next
	}

	iface, err := machine.AddInterface(link, model.AddInterfaceOptions{Number: ifaceNumber, MAC: macAddress})
	if err != nil {
		return err
	}

	if err := m.DeployLink(ctx, link); err != nil {
		return err
	}
	return m.machine.ConnectInterface(ctx, machine, iface)
}

// DisconnectMachineFromLink is `disconnect_machine_from_link`
// (`DockerManager.py:227`), with the same guard sequence and the same live
// reload.
//
// The collision domain is undeployed afterwards unless keepLink is set, and
// [linkService.Undeploy]'s "only networks with zero attached containers" rule
// is what keeps a still-used one alive.
func (m *Manager) DisconnectMachineFromLink(ctx context.Context, machine *model.Machine, link *model.Link, keepLink bool) error {
	if machine.Lab == nil {
		return kerrors.NewLabNotFoundDevice(machine.Name)
	}

	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return kerrors.NewMachineNotRunning(machine.Name)
	}
	if err := reloadContainer(ctx, m.api, machineContainer); err != nil {
		return err
	}
	if machineContainer.Status() != "running" {
		return kerrors.NewMachineNotRunning(machine.Name)
	}

	if link.Lab == nil {
		return kerrors.NewLabNotFoundCollisionDomain(link.Name)
	}
	if !link.HasMachine(machine.Name) {
		return kerrors.NewManagerMachineNotConnected(machine.Name, link.Name)
	}

	if err := machine.RemoveInterface(link); err != nil {
		return err
	}

	if err := m.machine.DisconnectFromLink(ctx, machine, link); err != nil {
		return err
	}
	if keepLink {
		return nil
	}
	return m.UndeployLink(ctx, link)
}

// ---------------------------------------------------------------------------
// Undeploy
// ---------------------------------------------------------------------------

// UndeployMachine is `undeploy_machine` (`DockerManager.py:269`): the device,
// then — unless keepLinks — the collision domains it was attached to.
func (m *Manager) UndeployMachine(ctx context.Context, machine *model.Machine, keepLinks bool) error {
	if machine.Lab == nil {
		return kerrors.NewLabNotFoundDevice(machine.Name)
	}

	if err := m.machine.Undeploy(ctx, machine.Lab.Hash, kathara.NewNameSet(machine.Name), nil); err != nil {
		return err
	}
	if keepLinks {
		return nil
	}

	links, err := interfaceLinkNames(machine)
	if err != nil {
		return err
	}
	return m.link.Undeploy(ctx, machine.Lab.Hash, links)
}

// UndeployLink is `undeploy_link` (`DockerManager.py:292`).
func (m *Manager) UndeployLink(ctx context.Context, link *model.Link) error {
	if link.Lab == nil {
		return kerrors.NewLabNotFoundCollisionDomain(link.Name)
	}
	return m.link.Undeploy(ctx, link.Lab.Hash, kathara.NewNameSet(link.Name))
}

// UndeployLab is `undeploy_lab` (`DockerManager.py:310`): machines first, then
// links — the reverse of the deploy order.
//
// The manager-level both-filters guard is TRUTHINESS, and the machine layer's
// is `is not None`, so two non-nil EMPTY sets pass here and trip there
// (SYNTHESIS §1.7). That is not a bug being routed around: it is the reason
// `lclean` passes nil and `lstart` passes empty sets and both mean "all".
func (m *Manager) UndeployLab(ctx context.Context, ref kathara.LabRef, opts kathara.UndeployLabOptions) error {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return err
	}

	if len(opts.SelectedMachines) > 0 && len(opts.ExcludedMachines) > 0 {
		return kerrors.ErrSelectOrExcludeDevices
	}

	if err := m.machine.Undeploy(ctx, labHash, opts.SelectedMachines, opts.ExcludedMachines); err != nil {
		return err
	}
	return m.link.Undeploy(ctx, labHash, opts.SelectedLinks)
}

// Wipe is `wipe` (`DockerManager.py:348`): every scenario, machines then links.
//
// `all_users` is silently DOWNGRADED on a remote daemon (`:361-363`) — there is
// no other user's identity to filter on across a socket — and the downgrade is
// a warning, not an error.
//
// The privilege check `all_users` implies is NOT here: `wipe` does not perform
// one, and the `@privileged` decorator it carries raises effective privileges
// rather than demanding them. The CLI's own check
// (`kerrors.ErrPrivilegeWipeAllUsers`) is what a user actually meets.
func (m *Manager) Wipe(ctx context.Context, allUsers bool) error {
	if m.settings.RemoteURL != nil && allUsers {
		allUsers = false
		slog.Warn("Cannot wipe devices of other users with a remote Docker connection.")
	}

	user, err := scopedUser(allUsers)
	if err != nil {
		return err
	}

	if err := m.machine.Wipe(ctx, user); err != nil {
		return err
	}
	return m.link.Wipe(ctx, user)
}

// ---------------------------------------------------------------------------
// Interactive
// ---------------------------------------------------------------------------

// ConnectTTY is `connect_tty` (`DockerManager.py:370`).
//
// The user scope is unconditional here — `connect_tty` has no `all_users` — so
// one user cannot attach to another's device even with the right hash.
func (m *Manager) ConnectTTY(ctx context.Context, machineName string, ref kathara.LabRef, opts kathara.ConnectTTYOptions) (kathara.TTYSession, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}
	user, err := util.GetCurrentUserName()
	if err != nil {
		return nil, err
	}
	return m.machine.Connect(ctx, labHash, machineName, user, opts)
}

// ConnectTTYObj is `connect_tty_obj` (`DockerManager.py:413`).
func (m *Manager) ConnectTTYObj(ctx context.Context, machine *model.Machine, opts kathara.ConnectTTYOptions) (kathara.TTYSession, error) {
	if machine.Lab == nil {
		return nil, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	return m.ConnectTTY(ctx, machine.Name, kathara.LabRef{Lab: machine.Lab}, opts)
}

// Exec is `exec(..., stream=False)` (`DockerManager.py:437`).
//
// `tty=False` is forced here even though `DockerMachine.exec` defaults it to
// True (`:476`), which is what makes the output multiplexed and therefore
// demuxable into separate stdout and stderr.
func (m *Manager) Exec(ctx context.Context, machineName string, command kathara.Command, ref kathara.LabRef, wait kathara.WaitPolicy) ([]byte, []byte, int, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, nil, 0, err
	}
	user, err := util.GetCurrentUserName()
	if err != nil {
		return nil, nil, 0, err
	}

	result, err := m.machine.Exec(ctx, labHash, machineName, command, user, false, wait, false)
	if err != nil {
		return nil, nil, 0, err
	}

	exitCode := 0
	if result.ExitCode != nil {
		exitCode = *result.ExitCode
	}
	return result.Stdout, result.Stderr, exitCode, nil
}

// ExecObj is `exec_obj(..., stream=False)` (`DockerManager.py:479`).
func (m *Manager) ExecObj(ctx context.Context, machine *model.Machine, command kathara.Command, wait kathara.WaitPolicy) ([]byte, []byte, int, error) {
	if machine.Lab == nil {
		return nil, nil, 0, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	return m.Exec(ctx, machine.Name, command, kathara.LabRef{Lab: machine.Lab}, wait)
}

// ExecStream is `exec(..., stream=True)`.
func (m *Manager) ExecStream(ctx context.Context, machineName string, command kathara.Command, ref kathara.LabRef, wait kathara.WaitPolicy) (kathara.ExecStream, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}
	user, err := util.GetCurrentUserName()
	if err != nil {
		return nil, err
	}

	result, err := m.machine.Exec(ctx, labHash, machineName, command, user, false, wait, true)
	if err != nil {
		return nil, err
	}
	return &execStream{manager: m, frames: result.Stream, execID: result.ID}, nil
}

// ExecStreamObj is `exec_obj(..., stream=True)`.
func (m *Manager) ExecStreamObj(ctx context.Context, machine *model.Machine, command kathara.Command, wait kathara.WaitPolicy) (kathara.ExecStream, error) {
	if machine.Lab == nil {
		return nil, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	return m.ExecStream(ctx, machine.Name, command, kathara.LabRef{Lab: machine.Lab}, wait)
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// CopyFiles is `copy_files` (`DockerManager.py:508`): pack the pairs into a tar
// and extract it at the container's root.
//
// The destination is always "/" and the guest paths are the archive's member
// names, which is why an entry's `GuestPath` must be absolute for the file to
// land where the caller means. Duplicate guest paths resolve last-wins on
// extraction, which is why the parameter is an ordered slice (ORDERING.tsv row
// `utils.py:452`).
func (m *Manager) CopyFiles(ctx context.Context, machine *model.Machine, files []kathara.CopyEntry) error {
	entries := make([]util.TarEntry, 0, len(files))
	for _, file := range files {
		content, err := copyEntryContent(file)
		if err != nil {
			return err
		}
		entries = append(entries, util.TarEntry{Path: file.GuestPath, Content: content})
	}

	tarData, err := util.PackFilesForTar(entries)
	if err != nil {
		return err
	}

	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("put_archive")
	}
	return m.machine.copyFiles(ctx, machineContainer, "/", tarData)
}

// copyEntryContent is `pack_file_for_tar`'s value branch (`utils.py:430-435`):
// a host path gets the `convert_win_2_linux` pass, a reader is shipped
// verbatim.
//
// Content wins when both are set. There is no Python original for that
// precedence — the `isinstance` ladder tests two types a value cannot both have
// — and [kathara.CopyEntry] fixes it so two backends cannot disagree.
func copyEntryContent(entry kathara.CopyEntry) ([]byte, error) {
	if entry.Content != nil {
		return io.ReadAll(entry.Content)
	}
	return util.ConvertWin2Linux(entry.HostPath)
}

// RetrieveFiles is `retrieve_files` (`DockerManager.py:527`).
func (m *Manager) RetrieveFiles(ctx context.Context, machine *model.Machine, src, dst string) error {
	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("get_archive")
	}
	return m.machine.retrieveFiles(ctx, machineContainer, src, dst)
}

// ---------------------------------------------------------------------------
// API objects
// ---------------------------------------------------------------------------

// GetMachineAPIObject is `get_machine_api_object` (`DockerManager.py:541`).
// `containers.pop()` takes the LAST match (ORDERING.tsv row 10).
func (m *Manager) GetMachineAPIObject(ctx context.Context, machineName string, ref kathara.LabRef, allUsers bool) (any, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}

	user, err := scopedUser(allUsers)
	if err != nil {
		return nil, err
	}

	containers, err := m.machine.getByFilters(ctx, labHash, machineName, user)
	if err != nil {
		return nil, err
	}
	if len(containers) > 0 {
		return containers[len(containers)-1], nil
	}
	return nil, kerrors.NewMachineNotFoundQuoted(machineName)
}

// GetMachinesAPIObjects is `get_machines_api_objects`
// (`DockerManager.py:579`): at-most-one ref, and none at all means every
// scenario of the selected users.
func (m *Manager) GetMachinesAPIObjects(ctx context.Context, ref kathara.LabRef, allUsers bool) ([]any, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}

	user, err := scopedUser(allUsers)
	if err != nil {
		return nil, err
	}

	containers, err := m.machine.getByFilters(ctx, labHash, "", user)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(containers))
	for _, c := range containers {
		out = append(out, c)
	}
	return out, nil
}

// GetLinkAPIObject is `get_link_api_object` (`DockerManager.py:609`).
func (m *Manager) GetLinkAPIObject(ctx context.Context, linkName string, ref kathara.LabRef, allUsers bool) (any, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}

	user, err := scopedUser(allUsers)
	if err != nil {
		return nil, err
	}

	networks, err := m.link.getByFilters(ctx, labHash, linkName, user)
	if err != nil {
		return nil, err
	}
	if len(networks) > 0 {
		return networks[len(networks)-1], nil
	}
	return nil, kerrors.NewLinkNotFoundQuoted(linkName)
}

// GetLinksAPIObjects is `get_links_api_objects` (`DockerManager.py:646`).
func (m *Manager) GetLinksAPIObjects(ctx context.Context, ref kathara.LabRef, allUsers bool) ([]any, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}

	user, err := scopedUser(allUsers)
	if err != nil {
		return nil, err
	}

	networks, err := m.link.getByFilters(ctx, labHash, "", user)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(networks))
	for _, n := range networks {
		out = append(out, n)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

// GetMachinesStats is `get_machines_stats` (`DockerManager.py:842`).
//
// The ref check runs NOW: this Python method holds no `yield`, so it validates
// and then returns the generator its machine layer built. Its singular sibling
// does hold one and behaves differently on purpose
// ([kathara.Manager.GetMachineStats]). Oracle-probed on both.
func (m *Manager) GetMachinesStats(ctx context.Context, ref kathara.LabRef, machineName string, allUsers bool) (kathara.MachinesStatsStream, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}
	user, err := scopedUser(allUsers)
	if err != nil {
		return nil, err
	}
	return &machinesStatsStream{
		manager:     m,
		labHash:     labHash,
		machineName: machineName,
		user:        user,
	}, nil
}

// GetMachineStats is `get_machine_stats` (`DockerManager.py:877`), a Python
// generator function: nothing in its body runs until the first `next()`, so
// the ref check is carried into the stream rather than performed here.
func (m *Manager) GetMachineStats(ctx context.Context, machineName string, ref kathara.LabRef, allUsers bool) kathara.MachineStatsStream {
	check := ref.RequireSingle()
	user, userErr := scopedUser(allUsers)
	if check == nil {
		// The identity failure is deferred with the guard: Python computes
		// `user_name` inside the generator body too, so nothing about it runs
		// until the first `next()`.
		check = userErr
	}
	return &machineStatsStream{
		check: check,
		inner: &machinesStatsStream{
			manager:     m,
			labHash:     resolveHash(ref),
			machineName: machineName,
			user:        user,
		},
	}
}

// GetMachineStatsObj is `get_machine_stats_obj` (`DockerManager.py:912`), whose
// body holds no `yield` — hence the eager LabNotFound.
func (m *Manager) GetMachineStatsObj(ctx context.Context, machine *model.Machine, allUsers bool) (kathara.MachineStatsStream, error) {
	if machine.Lab == nil {
		return nil, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	return m.GetMachineStats(ctx, machine.Name, kathara.LabRef{Lab: machine.Lab}, allUsers), nil
}

// GetLinksStats is `get_links_stats` (`DockerManager.py:934`).
func (m *Manager) GetLinksStats(ctx context.Context, ref kathara.LabRef, linkName string, allUsers bool) (kathara.LinksStatsStream, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}
	user, err := scopedUser(allUsers)
	if err != nil {
		return nil, err
	}
	return &linksStatsStream{
		manager:  m,
		labHash:  labHash,
		linkName: linkName,
		user:     user,
	}, nil
}

// GetLinkStats is `get_link_stats` (`DockerManager.py:967`), lazy for the same
// reason [Manager.GetMachineStats] is.
func (m *Manager) GetLinkStats(ctx context.Context, linkName string, ref kathara.LabRef, allUsers bool) kathara.LinkStatsStream {
	check := ref.RequireSingle()
	user, userErr := scopedUser(allUsers)
	if check == nil {
		check = userErr
	}
	return &linkStatsStream{
		check: check,
		inner: &linksStatsStream{
			manager:  m,
			labHash:  resolveHash(ref),
			linkName: linkName,
			user:     user,
		},
	}
}

// GetLinkStatsObj is `get_link_stats_obj` (`DockerManager.py:1005`), which uses
// the "Link" spelling of the LabNotFound message and is the only site that
// does.
func (m *Manager) GetLinkStatsObj(ctx context.Context, link *model.Link, allUsers bool) (kathara.LinkStatsStream, error) {
	if link.Lab == nil {
		return nil, kerrors.NewLabNotFoundLink(link.Name)
	}
	return m.GetLinkStats(ctx, link.Name, kathara.LabRef{Lab: link.Lab}, allUsers), nil
}

// ---------------------------------------------------------------------------
// Images
// ---------------------------------------------------------------------------

// CheckImage is `check_image` (`DockerManager.py:1026`): validate only, never
// pull.
func (m *Manager) CheckImage(ctx context.Context, imageName string) error {
	return m.image.Check(ctx, imageName)
}
