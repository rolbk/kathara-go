// This file is `DockerMachine.py`: devices as Docker containers.
//
// The payload construction lives next door in containerconfig.go, the command
// templates in startup.go and the fan-out shape in pool.go; what is left here
// is the order of operations, which is the part a golden sees.

package docker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// mountVolumesOption is the scenario option `deploy_machines` writes before the
// fan-out and deletes after it (`DockerMachine.py:154,188`). The interactive
// volume prompt writes it too, which is how a declined prompt reaches
// [model.Machine.GetVolumes].
const mountVolumesOption = "_mount_volumes"

// bridgeConnectedMeta is the `_bridge_connected` meta `create` sets and `start`
// deletes (`DockerMachine.py:270,574-575`): a flag saying the bridge was
// already attached at creation, so `start` must not attach it again.
const bridgeConnectedMeta = "_bridge_connected"

// eosProbe is the probe `_wait_startup_execution` runs (`DockerMachine.py:922`)
// against the sentinel the startup script touches last (:96).
//
// Python passes the single string `"cat /tmp/EOS"` and docker-py's
// `exec_create` shlex-splits it (`utils.split_command`); the split is done here
// instead, because the Go SDK takes a `[]string` and never splits anything.
var eosProbe = []string{"cat", "/tmp/EOS"}

// machineService is `DockerMachine` (`DockerMachine.py:112`).
type machineService struct {
	manager *Manager
	// engineVersion is `self._engine_version`
	// (`DockerMachine.py:118`): `parse_docker_engine_version(client.version()
	// ['Version'])`, read once in the constructor. It can be the empty string
	// — that is what the parser answers for a version starting with a
	// non-digit — and every comparison then fails with Python's own
	// `int('')` ValueError, at the call rather than here.
	engineVersion string
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// DeployMachines is `deploy_machines` (`DockerMachine.py:121`).
//
// Order, all of it observable:
//
//  1. the both-filters guard (TRUTHINESS here, unlike [machineService.Undeploy]);
//  2. filter the scenario's devices, preserving scenario order;
//  3. check and pull every distinct image, sequentially;
//  4. write the `_mount_volumes` option and dispatch `machines_with_volumes`
//     when any device asks for volumes, so the CLI can prompt;
//  5. create the shared folder, unless the daemon is remote;
//  6. `machines_deploy_started`, then the fan-out — parallel when the scenario
//     has no `lab.dep`, strictly sequential in dependency order when it has —
//     then `machines_deploy_ended`;
//  7. remove `_mount_volumes`.
//
// Step 7 is a `del` in Python and there is no [model.Lab] method for it. What is
// written back instead is the value step 4 computed, which is
// BEHAVIOURALLY IDENTICAL for every reader: [model.Machine.GetVolumes] falls
// back to exactly `policy in ("Prompt", "Always")` when the option is absent.
// The one thing it preserves that leaving the prompt's answer in place would
// not is that a declined prompt does not persist into the next deploy of the
// same [model.Lab]. PROPOSED-DIVERGENCES.md asks for `Lab.RemoveOption`.
// (Python leaks the option on the error path too — `del` is not in a `finally`
// — and so does this: the restore is skipped when the fan-out fails.)
//
// Errors: [kerrors.ErrSelectedOrExcludedMachines], then whatever the image
// pass, the shared folder or any worker answers.
func (s *machineService) DeployMachines(ctx context.Context, lab *model.Lab, selected, excluded kathara.NameSet) error {
	if len(selected) > 0 && len(excluded) > 0 {
		return kerrors.ErrSelectedOrExcludedMachines
	}

	machines := filterMachines(lab.Machines(), selected, excluded)

	// `lab_images = set(map(lambda x: x[1].get_image(), machines))`, whose
	// iteration order is hash-randomised and decides the order of the pull
	// progress bars and update prompts. ORDERING.tsv rows 29 and 46 rule the
	// port dedupes preserving first occurrence instead.
	seen := make(map[string]struct{}, len(machines))
	images := make([]string, 0, len(machines))
	for _, machine := range machines {
		image := machine.GetImage()
		if _, dup := seen[image]; dup {
			continue
		}
		seen[image] = struct{}{}
		images = append(images, image)
	}
	if err := s.manager.image.CheckFromList(ctx, images); err != nil {
		return err
	}

	policy := s.manager.settings.VolumeMountPolicy
	canMount := policy == "Prompt" || policy == "Always"
	lab.AddOption(mountVolumesOption, model.Bool(canMount))

	withVolumes := make([]*model.Machine, 0, len(machines))
	for _, machine := range machines {
		if machine.Meta.Volumes.Len() > 0 {
			withVolumes = append(withVolumes, machine)
		}
	}
	if len(withVolumes) > 0 {
		if err := event.Dispatch(s.manager.dispatcher, event.MachinesWithVolumes{Lab: lab, Machines: withVolumes}); err != nil {
			return err
		}
	}

	// `lab.general_options['shared_mount'] if present else Setting.shared_mount`
	// — the CORRECT read of the option, unlike the one in `create` (see
	// [machineService.volumeBinds]). The two halves of the feature disagree
	// and the disagreement is the bug both reproduce.
	sharedMount := s.manager.settings.SharedMount
	if option, ok := lab.GeneralOption("shared_mount"); ok {
		sharedMount = option.Truthy()
	}
	if sharedMount {
		if s.manager.settings.RemoteURL != nil {
			slog.Warn("Shared folder cannot be mounted with a remote Docker connection.")
		} else if err := lab.CreateSharedFolder(); err != nil {
			return err
		}
	}

	if err := event.Dispatch(s.manager.dispatcher, event.MachinesDeployStarted{Machines: machines}); err != nil {
		return err
	}

	var deployErr error
	if !lab.HasDependencies {
		deployErr = runChunked(ctx, machines, s.deployAndStart)
	} else {
		// CONCURRENCY.tsv row 6: a plain loop, first error aborts
		// immediately, and the order is `lab.dep`'s — which
		// `Lab.ApplyDependencies` has already stable-sorted into
		// `lab.machines`. No DAG scheduler: Python never had one and the row
		// says not to add one.
		for _, machine := range machines {
			if deployErr = s.deployAndStart(ctx, machine); deployErr != nil {
				break
			}
		}
	}
	if deployErr != nil {
		return deployErr
	}

	if err := event.Dispatch(s.manager.dispatcher, event.MachinesDeployEnded{}); err != nil {
		return err
	}

	lab.AddOption(mountVolumesOption, model.Bool(canMount))
	return nil
}

// deployAndStart is `_deploy_and_start_machine` (`DockerMachine.py:190`): the
// unit of work the pool runs, which dispatches its own completion event from
// the worker goroutine (CONCURRENCY.tsv row 5).
func (s *machineService) deployAndStart(ctx context.Context, machine *model.Machine) error {
	if err := s.Create(ctx, machine); err != nil {
		return err
	}
	if err := s.Start(ctx, machine); err != nil {
		return err
	}
	return event.Dispatch(s.manager.dispatcher, event.MachineDeployed{Machine: machine})
}

// filterMachines is the dict comprehension of `deploy_machines`
// (`DockerMachine.py:139-147`).
//
// Both filters are TRUTHINESS-tested here, so an empty set is "no filter" and
// `lstart`'s empty sets mean "everything" — the exact inverse of
// [machineService.Undeploy] (SYNTHESIS §1.7, NILABILITY.tsv:55-57). The
// comprehension preserves scenario order, which is submission order, which is
// chunk order.
func filterMachines(machines []*model.Machine, selected, excluded kathara.NameSet) []*model.Machine {
	switch {
	case len(selected) > 0:
		out := make([]*model.Machine, 0, len(machines))
		for _, machine := range machines {
			if selected.Has(machine.Name) {
				out = append(out, machine)
			}
		}
		return out
	case len(excluded) > 0:
		out := make([]*model.Machine, 0, len(machines))
		for _, machine := range machines {
			if !excluded.Has(machine.Name) {
				out = append(out, machine)
			}
		}
		return out
	}
	return machines
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// Create is `DockerMachine.create` (`DockerMachine.py:206`): build the
// container and, if the device ships files, push them in before it starts.
//
// The container is created, not started; [machineService.Start] does that and
// attaches the remaining collision domains afterwards.
//
// Errors: [kerrors.ErrMachineAlreadyExists], the model's option errors,
// [kerrors.ErrPrivilege] for a privileged device without root, a
// [kerrors.ErrPermission] for an unmountable volume, and the daemon's own.
func (s *machineService) Create(ctx context.Context, machine *model.Machine) error {
	slog.Debug("Creating device...", "device", machine.Name)

	user, err := util.GetCurrentUserName()
	if err != nil {
		return err
	}

	existing, err := s.getByFilters(ctx, machine.Lab.Hash, machine.Name, user)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return kerrors.NewMachineAlreadyExists(machine.Name)
	}

	image := machine.GetImage()
	memory, err := machine.GetMem()
	if err != nil {
		return err
	}
	cpus, err := machine.GetCPU(1_000_000_000)
	if err != nil {
		return err
	}
	ulimits := ulimitList(machine.Ulimits())
	bindings, exposed := portBindings(machine.Ports())

	// `if "bridged" in global_machine_metadata and not machine.is_bridged()`.
	// The presence test is the whole gate — a scenario-wide `bridged=False`
	// still turns bridging ON here, because only the KEY is looked at
	// (`DockerMachine.py:242`). `is_bridged` itself, unlike `is_privileged`,
	// never consults the scenario-wide metadata, which is what makes this
	// hand-rolled propagation necessary in the first place.
	if _, ok := machine.Lab.GlobalMachineMetadata("bridged"); ok && !machine.IsBridged() {
		if _, _, err := machine.AddMeta("bridged", "True"); err != nil {
			return err
		}
	}

	if bindings != nil && !machine.IsBridged() {
		slog.Warn("To expose ports of a device on the host, you have to specify the `bridged` option on that device.",
			"device", machine.Name)
	}

	if execCommand, ok := machine.Lab.GlobalMachineMetadata("exec"); ok {
		if _, _, err := machine.AddMeta("exec", execCommand.String()); err != nil {
			return err
		}
	}

	firstIface, hasFirstIface, err := firstInterface(machine)
	if err != nil {
		return err
	}
	// `first_network = first_machine_iface.link.api_object` (`:261`) is a plain
	// READ and cannot fail; the `.name` that dies on an undeployed collision
	// domain is not reached until the networking config is built at `:341` —
	// after the volume PermissionError (`:320`) and the PrivilegeError (`:328`)
	// have had their chance. So the nil travels and the AttributeError is
	// raised down there, in Python's own place.
	var firstNetwork *Network
	if hasFirstIface {
		firstNetwork, _ = networkOf(firstIface.Link.APIObject)
	}

	if machine.IsBridged() {
		// `max(machine.interfaces.keys()) + 1 if machine.interfaces else 0` —
		// over every slot INCLUDING tombstones, since a removed interface's
		// number stays taken. The seed is the first slot's number and not
		// zero: `max()` over the keys can be negative, which the API can
		// produce by numbering an interface explicitly.
		bridgedIface := 0
		if slots := machine.Interfaces(); len(slots) > 0 {
			bridgedIface = slots[0].Number
			for _, slot := range slots[1:] {
				bridgedIface = max(bridgedIface, slot.Number)
			}
			bridgedIface++
		}
		// `add_meta("bridged_iface", <int>)` stores a Python int, and the int
		// is what lets `Machine.check()` count it as an occupied slot; a
		// string there is the TypeError of DIVERGENCES.md 1. `AddMeta` takes
		// only strings, so the typed field is written directly.
		machine.Meta.BridgedIface = model.Int(int64(bridgedIface))
	}

	if !hasFirstIface && machine.IsBridged() {
		// No declared interfaces but bridging asked for: the Docker bridge
		// becomes the container's first network, and the flag tells `start`
		// not to attach it a second time. `get_docker_bridge` may have found
		// nothing, in which case the api_object is nil and the truthiness test
		// below leaves the container on "none".
		firstNetwork, _ = networkOf(machine.Lab.GetOrNewLink(model.BridgeLinkName).APIObject)
		machine.Meta.Extras.Set(bridgeConnectedMeta, model.Bool(true))
	}

	sysctls, err := containerSysctls(s.engineVersion, machine, hasFirstIface)
	if err != nil {
		return err
	}

	binds, mountPoints, err := s.volumeBinds(machine)
	if err != nil {
		return err
	}

	privileged := machine.IsPrivileged()
	admin, err := util.IsAdmin()
	if err != nil {
		return err
	}
	if privileged && !admin {
		return kerrors.NewPrivilegeMachinePrivileged(machine.Name)
	}
	if s.manager.settings.RemoteURL != nil && privileged {
		privileged = false
		slog.Warn("Privileged flag is ignored with a remote Docker connection.")
	}

	var endpoint *network.EndpointSettings
	if hasFirstIface {
		driverOpt, err := createDriverOpt(s.engineVersion, machine, firstIface,
			ifaceSysctls(machine.Sysctls(), firstIface.Number))
		if err != nil {
			return err
		}
		if firstNetwork == nil {
			// `networking_config = {first_network.name: …}` (`:341`) on a
			// collision domain that was never deployed — and the dict key is
			// evaluated AFTER `_create_driver_opt`, which is why the failure
			// sits below it.
			return newPyAttributeError("name")
		}
		endpoint = &network.EndpointSettings{DriverOpts: driverOpt}
	}

	var bridgedIface *int
	if machine.IsBridged() {
		// Always an int here: the block above has just written one.
		if n, ok := bridgedIfaceNumber(machine); ok {
			bridgedIface = &n
		}
	}
	labels := ContainerLabels(machine.Name, machine.Lab.Hash, user, machine.GetShell(), bridgedIface)

	entrypoint, args, err := entrypointAndArgs(machine)
	if err != nil {
		return err
	}

	nanoCPUs := int64(0)
	if cpus != nil {
		nanoCPUs = *cpus
	}

	request := createArgs(
		ContainerName(s.manager.settings.DevicePrefix, user, machine.Name, machine.Lab.Hash),
		image,
		machine.Name,
		privileged,
		firstNetwork.Name(),
		endpoint,
		envList(machine.Envs()),
		sysctls,
		parseMemory(memory),
		nanoCPUs,
		bindings,
		exposed,
		binds,
		mountPoints,
		labels,
		ulimits,
		entrypoint,
		args,
	)

	created, err := s.manager.api.ContainerCreate(ctx, request.Config, request.HostConfig, request.Networking, nil, request.Name)
	if err != nil {
		// `except APIError as e: raise e` — an identity re-raise
		// (`DockerMachine.py:385-386`), dead in Python and a plain return
		// here.
		return err
	}

	inspected, err := s.manager.api.ContainerInspect(ctx, created.ID)
	if err != nil {
		return err
	}
	machineContainer := &Container{ID: created.ID, Attrs: inspected}

	tarData, err := packData(machine)
	if err != nil {
		return err
	}
	if tarData != nil {
		if err := s.copyFiles(ctx, machineContainer, "/", tarData); err != nil {
			return err
		}
	}

	machine.APIObject = machineContainer
	return nil
}

// firstInterface is `machine.interfaces[0]` guarded by `if machine.interfaces:`
// (`DockerMachine.py:259-261`).
//
// The guard tests whether the device has ANY interface and the lookup then asks
// for the one numbered ZERO, which are not the same question. Python's two
// failures are reproduced rather than smoothed over (docker-backend.md gotcha
// 9), because both are reachable through `connect_machine_to_link`, which can
// leave a device with a hole at 0 that `Machine.check()` never sees:
//
//   - a device with interfaces but none numbered 0 → `KeyError: 0`;
//   - a device whose slot 0 is a TOMBSTONE — `remove_interface` leaves the
//     number taken and the value None — → `AttributeError: 'NoneType' object
//     has no attribute 'link'`, from the `.link` on the next line.
func firstInterface(machine *model.Machine) (model.Interface, bool, error) {
	slots := machine.Interfaces()
	if len(slots) == 0 {
		return model.Interface{}, false, nil
	}

	for _, slot := range slots {
		if slot.Number != 0 {
			continue
		}
		if slot.IsTombstone() {
			return model.Interface{}, false, newPyAttributeError("link")
		}
		return slot, true, nil
	}
	return model.Interface{}, false, newPyKeyErrorInt(0)
}

// entrypointAndArgs is `DockerMachine.create`'s pair at `:358-361`.
//
// `entrypoint` is shlex-split whenever the meta is PRESENT — no truthiness gate,
// so `entrypoint=""` splits to the empty list and is sent as an empty
// entrypoint, which RESETS the image's rather than inheriting it. `args` has
// one: `if "args" in meta and meta["args"]`, so an empty
// value is dropped entirely. Then `shlex.split(args) if type(args) == str else
// args`, which is the string/list union [model.Scalar] keeps — lab.conf gives a
// string, the CLI's argparse REMAINDER gives a list.
func entrypointAndArgs(machine *model.Machine) (entrypoint, args []string, err error) {
	if machine.Meta.Entrypoint.IsSet() {
		if entrypoint, err = ShlexSplit(machine.Meta.Entrypoint.String()); err != nil {
			return nil, nil, err
		}
		if len(entrypoint) == 0 {
			// `shlex.split("")` is `[]`, and docker-py posts it verbatim as
			// `'Entrypoint': []` — an EMPTY ARRAY, which tells the daemon to
			// drop the image's entrypoint. A nil slice marshals as `null`,
			// which means "keep the image's", so the empty list is made
			// explicit. `strslice.StrSlice` has no `omitempty`, so `[]` reaches
			// the wire.
			entrypoint = []string{}
		}
	}

	if !machine.Meta.Args.Truthy() {
		return entrypoint, nil, nil
	}
	if list, isList := machine.Meta.Args.AsStrings(); isList {
		return entrypoint, list, nil
	}
	if args, err = ShlexSplit(machine.Meta.Args.String()); err != nil {
		return nil, nil, err
	}
	return entrypoint, args, nil
}

// volumeBinds is the `volumes` dict of `create` (`DockerMachine.py:300-325`),
// already rendered as `HostConfig.Binds` strings in Python's insertion order:
// `/shared` first, `/hosthome` second, the device's own volumes after
// (ORDERING.tsv row 36 — the list order is golden-visible).
//
// # The reproduced bug
//
//	shared_mount = ['shared_mount'] if 'shared_mount' in lab_options else Setting…
//
// The true branch is a LITERAL LIST holding the option's NAME, not its value,
// and a one-element list is always truthy. So a per-scenario
// `shared_mount=False` cannot suppress the `/shared` mount here; only the
// absence of `lab.shared_path` can (docker-backend.md gotcha 2). The folder
// creation in `deploy_machines` reads the same option CORRECTLY, so the two
// halves of the feature disagree: the folder is not created and the mount is
// still requested — of a path that is then empty.
//
// `hosthome_mount` has no such bug and is additionally gated on a local daemon:
// there is no host home to bind on a remote one.
//
// The device's own volumes come from [model.Machine.GetVolumes], whose
// MountDenied is caught and downgraded to a warning exactly as Python does
// (`:324-325`) — that catch is live, not dead: `get_volumes` is inside the
// `try`. A PermissionError from the per-directory check is NOT caught and
// propagates.
//
// # Two payload fields, not one
//
// docker-py splits the same `volumes` dict across BOTH of the create request's
// halves: `host_config_kwargs['binds'] = volumes` gives `HostConfig.Binds`, and
// `create_kwargs['volumes'] = [v.get('bind') for v in volumes.values()]`
// (`models/containers.py:1173-1177`) gives `Config.Volumes`, which
// `ContainerConfig` turns into `{"/shared": {}, "/hosthome": {}, …}`
// (`types/containers.py:778`). `docker inspect .Config.Volumes` shows the
// second, so the guest halves are returned alongside the bind strings rather
// than re-derived from them — a Windows bind (`C:\x:/guest:rw`) cannot be split
// back apart unambiguously.
func (s *machineService) volumeBinds(machine *model.Machine) (binds, mountPoints []string, err error) {
	add := func(hostPath, guestPath, mode string) {
		binds = append(binds, bind(hostPath, guestPath, mode))
		mountPoints = append(mountPoints, guestPath)
	}

	sharedMount := s.manager.settings.SharedMount
	if _, ok := machine.Lab.GeneralOption("shared_mount"); ok {
		sharedMount = true // the bug: presence, not value
	}
	if sharedMount && machine.Lab.SharedPath != "" {
		add(machine.Lab.SharedPath, "/shared", "rw")
	}

	hosthomeMount := s.manager.settings.HosthomeMount
	if option, ok := machine.Lab.GeneralOption("hosthome_mount"); ok {
		hosthomeMount = option.Truthy()
	}
	if hosthomeMount && s.manager.settings.RemoteURL == nil {
		home, err := util.GetCurrentUserHome()
		if err != nil {
			return nil, nil, err
		}
		add(home, "/hosthome", "rw")
	}

	volumes, err := machine.GetVolumes()
	if err != nil {
		if errors.Is(err, kerrors.ErrMountDenied) {
			slog.Warn("Volumes of device will not be mounted.", "device", machine.Name)
			return binds, mountPoints, nil
		}
		return nil, nil, err
	}

	for _, entry := range volumes.Entries() {
		missing, err := util.CheckDirectoryPermissions(entry.Key, entry.Value.Mode)
		if err != nil {
			return nil, nil, err
		}
		if len(missing) > 0 {
			return nil, nil, kerrors.NewVolumePermission(entry.Key, entry.Value.GuestPath, missing)
		}
		add(entry.Key, entry.Value.GuestPath, entry.Value.Mode)
	}

	return binds, mountPoints, nil
}

// ---------------------------------------------------------------------------
// Start
// ---------------------------------------------------------------------------

// Start is `DockerMachine.start` (`DockerMachine.py:493`).
//
//  1. start the container, translating two daemon 500s;
//  2. attach every collision domain AFTER the first — Python's comment says
//     Docker orders pre-start attachments nondeterministically, which is why
//     interface 0 is bound at create and the rest here;
//  3. attach the Docker bridge if bridging was asked for and `create` did not
//     already do it;
//  4. rewrite the device's exec commands with an echo before each;
//  5. run the whole startup script DETACHED, downgrading a missing shell to a
//     warning plus `num_terms=0`;
//  6. reload, and drop the `_bridge_connected` flag.
//
// Step 4 mutates the model destructively, as Python does: a second `start` on
// the same [model.Machine] wraps the echoes again (docker-backend.md gotcha
// 14).
func (s *machineService) Start(ctx context.Context, machine *model.Machine) error {
	slog.Debug("Starting device...", "device", machine.Name)

	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("start")
	}

	if err := s.manager.api.ContainerStart(ctx, machineContainer.ID, container.StartOptions{}); err != nil {
		switch {
		case isMountsDenied(err):
			return kerrors.ErrHostDriveNotShared
		case isPluginInconsistentState(err):
			return kerrors.ErrInconsistentState
		default:
			return err
		}
	}

	// `islice(machine.interfaces.items(), 1, None)` — every slot but the
	// FIRST-INSERTED, which `Machine.check()` has already made the
	// numerically-lowest one (ORDERING.tsv row 41). The ordered slice makes
	// that literally "all but the first element"; `islice` of an empty
	// sequence is empty rather than an error, hence the length guard.
	slots := machine.Interfaces()
	if len(slots) > 0 {
		slots = slots[1:]
	}
	for _, iface := range slots {
		if iface.IsTombstone() {
			// Python would dereference `machine_iface.link` and die; a
			// tombstone here means `remove_interface` ran between check and
			// start, which only the API can do.
			return newPyAttributeError("link")
		}
		slog.Debug("Connecting device to collision domain...",
			"device", machine.Name, "collision_domain", iface.Link.Name, "interface", iface.Number)
		if err := s.ConnectInterface(ctx, machine, iface); err != nil {
			return err
		}
	}

	if _, alreadyConnected := machine.Meta.Extras.Get(bridgeConnectedMeta); !alreadyConnected && machine.IsBridged() {
		bridge, ok := networkOf(machine.Lab.GetOrNewLink(model.BridgeLinkName).APIObject)
		if !ok {
			return newPyAttributeError("connect")
		}
		if err := s.manager.api.NetworkConnect(ctx, bridge.ID, machineContainer.ID, nil); err != nil {
			return err
		}
	}

	if len(machine.Meta.ExecCommands) > 0 {
		machine.Meta.ExecCommands = interleaveExecCommands(machine.Meta.ExecCommands)
	}

	startupCommandsString := renderStartupCommands(machine.Name, machine.Meta.ExecCommands)
	slog.Debug("Executing startup command.", "device", machine.Name, "command", startupCommandsString)

	// The shell comes from the container's own `shell` LABEL, not from the
	// model — `create` wrote it there and `start` reads it back
	// (`DockerMachine.py:556`). Not privileged: the startup script runs with
	// the container's ordinary permissions even for a privileged device.
	_, err := s.execRun(ctx, machineContainer, execRunOptions{
		Cmd:    []string{machineContainer.Label(labelShell), "-c", startupCommandsString},
		Stdout: true,
		Stderr: true,
		Detach: true,
	})
	if err != nil {
		var binaryErr *kerrors.BinaryError
		if !errors.As(err, &binaryErr) {
			return err
		}
		// A device whose shell is missing gets no startup commands and no
		// terminal, and the deploy continues (`:562-569`).
		machine.Meta.NumTerms = model.Int(0)
		slog.Warn("Shell not found in image of device. Startup commands will not be executed "+
			"and terminal will not open. Please specify a valid shell for this device.",
			"shell", binaryErr.Binary, "image", machine.GetImage(), "device", machine.Name)
	}

	if err := reloadContainer(ctx, s.manager.api, machineContainer); err != nil {
		return err
	}

	machine.Meta.Extras.Delete(bridgeConnectedMeta)
	return nil
}

// ConnectInterface is `connect_interface` (`DockerMachine.py:395`): attach a
// running container to one more collision domain, unless it is already on it.
//
// The membership test is by DOCKER NETWORK NAME against the container's current
// endpoint map, so it is the deployed network that decides, not the model.
//
// Errors: [kerrors.ErrInconsistentState] for the two 500s that mean the plugin
// and the daemon disagree about what exists; everything else propagates
// untranslated, including the 510 of the Python suite's
// `test_connect_interface_plugin_api_error`.
func (s *machineService) ConnectInterface(ctx context.Context, machine *model.Machine, iface model.Interface) error {
	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("attrs")
	}
	linkNetwork, ok := networkOf(iface.Link.APIObject)
	if !ok {
		return newPyAttributeError("name")
	}

	if _, attached := machineContainer.Networks()[linkNetwork.Name()]; attached {
		return nil
	}

	driverOpt, err := createDriverOpt(s.engineVersion, machine, iface,
		ifaceSysctls(machine.Sysctls(), iface.Number))
	if err != nil {
		return err
	}

	err = s.manager.api.NetworkConnect(ctx, linkNetwork.ID, machineContainer.ID,
		&network.EndpointSettings{DriverOpts: driverOpt})
	if err != nil {
		if isPluginInconsistentState(err) {
			return kerrors.ErrInconsistentState
		}
		return err
	}
	return nil
}

// DisconnectFromLink is `disconnect_from_link` (`DockerMachine.py:477`), the
// mirror of [machineService.ConnectInterface] and, like it, a no-op when the
// container is not on that network.
func (s *machineService) DisconnectFromLink(ctx context.Context, machine *model.Machine, link *model.Link) error {
	machineContainer, ok := containerOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("attrs")
	}
	linkNetwork, ok := networkOf(link.APIObject)
	if !ok {
		return newPyAttributeError("name")
	}

	if _, attached := machineContainer.Networks()[linkNetwork.Name()]; !attached {
		return nil
	}
	return s.manager.api.NetworkDisconnect(ctx, linkNetwork.ID, machineContainer.ID, false)
}

// ---------------------------------------------------------------------------
// Undeploy / wipe
// ---------------------------------------------------------------------------

// Undeploy is `DockerMachine.undeploy` (`DockerMachine.py:577`).
//
// The filters are IS-NOT-NONE here, which inverts what an empty set means: a
// non-nil empty `selected` undeploys NOTHING, where the same value on the
// deploy path deploys everything (SYNTHESIS §1.7). The both-filters guard is
// `is not None` too, so two empty sets are an [kerrors.ErrSelectedOrExcludedMachines]
// even though the deploy-side guard would have let them through.
//
// The listing is always scoped to the current user — `undeploy` does not take
// a user parameter and hardcodes it — so one user cannot tear down another's
// devices even with a matching lab hash. Only [machineService.Wipe] can.
//
// The `_started`/`_ended` events bracket the fan-out on the CALLER's goroutine
// and are dispatched only when there is something to do (`len(containers) > 0`),
// which is why an empty selection produces no progress bar at all.
func (s *machineService) Undeploy(ctx context.Context, labHash string, selected, excluded kathara.NameSet) error {
	if selected != nil && excluded != nil {
		return kerrors.ErrSelectedOrExcludedMachines
	}

	user, err := util.GetCurrentUserName()
	if err != nil {
		return err
	}

	containers, err := s.getByFilters(ctx, labHash, "", user)
	if err != nil {
		return err
	}
	switch {
	case selected != nil:
		containers = filterContainers(containers, func(name string) bool { return selected.Has(name) })
	case excluded != nil:
		containers = filterContainers(containers, func(name string) bool { return !excluded.Has(name) })
	}

	if len(containers) == 0 {
		return nil
	}

	items := make([]event.APIObject, 0, len(containers))
	for _, c := range containers {
		items = append(items, c)
	}
	if err := event.Dispatch(s.manager.dispatcher, event.MachinesUndeployStarted{Containers: items}); err != nil {
		return err
	}

	if err := runChunked(ctx, containers, s.undeployMachine); err != nil {
		return err
	}

	return event.Dispatch(s.manager.dispatcher, event.MachinesUndeployEnded{})
}

// Wipe is `DockerMachine.wipe` (`DockerMachine.py:614`): every device of one
// user, or of every user when user is empty.
//
// No events: the wipe path has no progress bar (CONCURRENCY.tsv row 8), and the
// pool is entered unconditionally — an empty list is an empty chunk list, so
// there is nothing to skip.
func (s *machineService) Wipe(ctx context.Context, user string) error {
	containers, err := s.getByFilters(ctx, "", "", user)
	if err != nil {
		return err
	}
	return runChunked(ctx, containers, s.undeployMachine)
}

// undeployMachine is `_undeploy_machine` (`DockerMachine.py:632`), which
// dispatches its completion event from the worker goroutine.
func (s *machineService) undeployMachine(ctx context.Context, c *Container) error {
	if err := s.deleteMachine(ctx, c); err != nil {
		return err
	}
	return event.Dispatch(s.manager.dispatcher, event.MachineUndeployed{Container: c})
}

// deleteMachine is `_delete_machine` (`DockerMachine.py:1087`): run the
// shutdown script if the container is running, then force-remove it WITH its
// anonymous volumes.
//
// The shutdown exec is PRIVILEGED, where the startup exec deliberately is not
// (`:1107` vs `:559`), and a missing shell is a warning rather than a failure —
// the container is removed either way.
func (s *machineService) deleteMachine(ctx context.Context, c *Container) error {
	name := c.Label(labelName)
	shutdownCommandsString := renderShutdownCommands(name)
	slog.Debug("Executing shutdown commands.", "device", name, "command", shutdownCommandsString)

	if c.Status() == "running" {
		_, err := s.execRun(ctx, c, execRunOptions{
			Cmd:        []string{c.Label(labelShell), "-c", shutdownCommandsString},
			Stdout:     true,
			Privileged: true,
		})
		if err != nil {
			var binaryErr *kerrors.BinaryError
			if !errors.As(err, &binaryErr) {
				return err
			}
			// Python reports `container.image.tags[0]` here, which IndexErrors
			// on an untagged image. The image reference from the inspect is
			// used when there is no tag, because a log line must not be able
			// to fail a teardown (PORT_SPEC §10).
			slog.Warn("Shell not found in image of device. Shutdown commands will not be executed.",
				"shell", binaryErr.Binary, "image", s.manager.imageLabel(ctx, c), "device", name)
		}
	}

	return s.manager.api.ContainerRemove(ctx, c.ID, container.RemoveOptions{RemoveVolumes: true, Force: true})
}

// filterContainers is the list comprehension of `undeploy`
// (`DockerMachine.py:598,600`), which filters on the container's `name` LABEL —
// the device name — and not on the container's Docker name.
func filterContainers(containers []*Container, keep func(name string) bool) []*Container {
	out := make([]*Container, 0, len(containers))
	for _, c := range containers {
		if keep(c.Label(labelName)) {
			out = append(out, c)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// getByFilters is `get_machines_api_objects_by_filters`
// (`DockerMachine.py:997`).
func (s *machineService) getByFilters(ctx context.Context, labHash, machineName, user string) ([]*Container, error) {
	return listContainers(ctx, s.manager.api, ObjectFilters(user, labHash, machineName))
}

// ---------------------------------------------------------------------------
// Exec
// ---------------------------------------------------------------------------

// execRunOptions is the keyword set of `_exec_run` (`DockerMachine.py:820`),
// minus the two Python passes as constants everywhere (`user=”`, no
// environment or workdir).
type execRunOptions struct {
	Cmd        []string
	Stdout     bool
	Stderr     bool
	Stdin      bool
	Tty        bool
	Privileged bool
	Detach     bool
	Stream     bool
	Demux      bool
}

// execRunResult is the `{exit_code, Id, output}` dict `_exec_run` returns
// (`DockerMachine.py:893,895`).
type execRunResult struct {
	// ExitCode is nil for a stream or socket result, and also for an exec that
	// has not finished — `exec_inspect` reports null until then.
	ExitCode *int
	// ID is the Docker exec id, `resp['Id']`.
	ID string
	// Stdout and Stderr are the collected output of a non-stream run. Python's
	// demux hands back `None` for a side that produced nothing; nil here is
	// that None, and the JSON CLI already treats the two alike
	// (JSON_CLI_CONTRACT.md §4.2).
	Stdout []byte
	Stderr []byte
	// Stream is set for a streaming run: the live frame source.
	Stream *execFrames
}

// execRun is `_exec_run` (`DockerMachine.py:820`), docker-py's `exec_run` plus
// the missing-binary detection Kathará wraps around it.
//
// The detection has TWO independent paths and only one of them is on every
// call:
//
//   - the OCI-runtime `APIError` from `exec_start` itself (`:871-876`), which
//     fires in every mode including streaming;
//   - a scan of the collected STDOUT for the same pattern (`:879-890`), which
//     needs the whole output and a non-zero exit code, and therefore runs only
//     for a non-stream, non-socket call. A tty runtime prints the failure to
//     stdout instead of failing the API call, which is why the second path
//     exists at all.
//
// Streaming execs therefore NEVER raise `MachineBinaryError` from the scan, and
// the error text reaches the consumer as ordinary output — a deliberate
// asymmetry the Python suite pins with three pairs of tests.
//
// `exec_inspect` is called on every path, streaming included, before the
// result is assembled; its ExitCode is null while the command runs, which is
// what makes the streaming exit code nil here.
func (s *machineService) execRun(ctx context.Context, c *Container, opts execRunOptions) (*execRunResult, error) {
	created, err := s.manager.api.ContainerExecCreate(ctx, c.ID, container.ExecOptions{
		Cmd:          opts.Cmd,
		AttachStdout: opts.Stdout,
		AttachStderr: opts.Stderr,
		AttachStdin:  opts.Stdin,
		Tty:          opts.Tty,
		Privileged:   opts.Privileged,
	})
	if err != nil {
		return nil, err
	}

	if opts.Detach {
		// `exec_start(detach=True)` returns nothing and the daemon runs the
		// command in the background.
		if err := s.manager.api.ContainerExecStart(ctx, created.ID, container.ExecStartOptions{Detach: true, Tty: opts.Tty}); err != nil {
			if binaryErr := ociBinaryError(err, c.Label(labelName)); binaryErr != nil {
				return nil, binaryErr
			}
			return nil, err
		}
		inspect, err := s.manager.api.ContainerExecInspect(ctx, created.ID)
		if err != nil {
			return nil, err
		}
		return &execRunResult{ExitCode: execExitCode(inspect), ID: created.ID}, nil
	}

	attached, err := s.manager.api.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: opts.Tty})
	if err != nil {
		if binaryErr := ociBinaryError(err, c.Label(labelName)); binaryErr != nil {
			return nil, binaryErr
		}
		return nil, err
	}

	if opts.Stream {
		// `exec_inspect` runs at `:878`, BEFORE the `if socket or stream:
		// return` at `:892`, so the streaming path pays the call too and an
		// APIError from it propagates instead of a live stream being handed
		// back. The value is discarded: a streaming result reports
		// `'exit_code': None` whatever the inspect said.
		if _, err := s.manager.api.ContainerExecInspect(ctx, created.ID); err != nil {
			// Python drops the response on the floor for the GC; a hijacked
			// connection is not a thing to leak, and nothing observable
			// depends on it staying open once the error wins.
			attached.Close()
			return nil, err
		}
		return &execRunResult{
			ID:     created.ID,
			Stream: newExecFrames(attached, opts.Tty),
		}, nil
	}

	frames := newExecFrames(attached, opts.Tty)
	stdout, stderr, err := frames.ReadAll(ctx)
	closeErr := frames.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}

	inspect, err := s.manager.api.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	exitCode := execExitCode(inspect)

	if exitCode != nil && *exitCode != 0 {
		// `(stdout_out, _) = exec_output if demux else (exec_output, None)` —
		// the scan is over STDOUT only, whether or not the caller asked for
		// demuxing. Python decodes with chardet first; the pattern is ASCII
		// and the regexp runs on the bytes here, which finds the same matches
		// for every encoding chardet would have detected (DIVERGENCES).
		if binaryErr := ociBinaryErrorFromOutput(stdout, c.Label(labelName)); binaryErr != nil {
			return nil, binaryErr
		}
	}

	return &execRunResult{ExitCode: exitCode, ID: created.ID, Stdout: stdout, Stderr: stderr}, nil
}

// execExitCode is `exec_inspect(...)['ExitCode']` with Python's None for "still
// running".
//
// The Go SDK reports the pair as `Running bool` + `ExitCode int` where the API
// sends `"ExitCode": null`, so the null is recovered from `Running`.
func execExitCode(inspect container.ExecInspect) *int {
	if inspect.Running {
		return nil
	}
	code := inspect.ExitCode
	return &code
}

// Exec is `DockerMachine.exec` (`DockerMachine.py:750`).
//
// wait is applied BEFORE the command runs, and a wait that ends in an API error
// is [kerrors.ErrMachineNotRunning] here — where [machineService.Connect]
// silently returns instead (docker-backend.md gotcha 17).
//
// `containers.pop()` takes the LAST match (ORDERING.tsv row 10), which is
// observable only when the filters match more than one container — the
// `machine_name` + `lab_hash` + `user` triple makes that impossible for a
// scenario this backend created.
func (s *machineService) Exec(
	ctx context.Context,
	labHash, machineName string,
	command kathara.Command,
	user string,
	tty bool,
	wait kathara.WaitPolicy,
	stream bool,
) (*execRunResult, error) {
	slog.Debug("Executing command on device.", "device", machineName)

	containers, err := s.getByFilters(ctx, labHash, machineName, user)
	if err != nil {
		return nil, err
	}
	if len(containers) == 0 {
		return nil, kerrors.NewMachineNotRunning(machineName)
	}
	c := containers[len(containers)-1]

	if wait.Enabled {
		waited, err := s.waitStartupExecution(ctx, c, wait.Retries, wait.Interval)
		if err != nil {
			return nil, err
		}
		if waited == startupWaitAPIError {
			return nil, kerrors.NewMachineNotRunning(machineName)
		}
	}

	cmd, err := commandWords(command)
	if err != nil {
		return nil, err
	}

	return s.execRun(ctx, c, execRunOptions{
		Cmd:    cmd,
		Stdout: true,
		Stderr: true,
		Tty:    tty,
		Stream: stream,
		Demux:  true,
	})
}

// ---------------------------------------------------------------------------
// Startup wait
// ---------------------------------------------------------------------------

// The three answers of `_wait_startup_execution` (`DockerMachine.py:897`),
// whose docstring says "bool" and whose body returns an int.
const (
	// startupWaitInterrupted is 0: the user asked for control before the
	// startup script finished.
	startupWaitInterrupted = 0
	// startupWaitCompleted is 1: `/tmp/EOS` appeared.
	startupWaitCompleted = 1
	// startupWaitAPIError is 2: an APIError while probing — the container
	// disappeared. `connect` returns silently on it and `exec` raises
	// MachineNotRunning.
	startupWaitAPIError = 2
)

// waitStartupExecution is `_wait_startup_execution`
// (`DockerMachine.py:897`): poll `cat /tmp/EOS` until it succeeds, the retries
// run out, or the user hits a key.
//
// The contract, item by item:
//
//   - nRetries nil is FOREVER (NILABILITY.tsv). A negative count is made
//     positive (`abs`), and a negative interval becomes 1 second — both
//     sanitised on entry, both reachable through the API's `wait=(-1, -1)`.
//   - Zero retries means "probe once and stop", because the counter is
//     compared BEFORE it is incremented.
//   - `machine_startup_wait_started` is dispatched at most once, the first
//     time the probe fails, so the CLI prints its "press ENTER" notice once.
//   - The user-input probe runs on EVERY iteration, including the one where
//     the probe just succeeded; when it fires, the answer is
//     `int(False or is_cmd_success)` — 1 if the startup had just finished,
//     0 otherwise.
//   - `except KeyboardInterrupt: pass` swallows Ctrl-C so the terminal does
//     not close mid-wait (docker-backend.md gotcha 18). Go's analogue is
//     context cancellation, and it is NOT swallowed: JSON_CLI_CONTRACT.md §6.2
//     turns a cancelled operation into an orderly exit 0, which needs the
//     cancellation to be returned rather than ignored, and the terminal it was
//     protecting has not been opened yet at this point.
//   - An APIError from the probe returns 2 immediately. A `MachineBinaryError`
//     is NOT an APIError and escapes instead — an image without `cat` fails
//     `exec --wait` and `connect` with the binary error, not with "not
//     running".
func (s *machineService) waitStartupExecution(ctx context.Context, c *Container, nRetries *int, retryInterval time.Duration) (int, error) {
	slog.Debug("Waiting startup commands execution...", "device", c.Label(labelName))

	if nRetries != nil && *nRetries < 0 {
		positive := -*nRetries
		nRetries = &positive
	}
	if retryInterval < 0 {
		retryInterval = time.Second
	}

	retries := 0
	startupWaited := startupWaitCompleted
	printed := false

	for {
		result, err := s.execRun(ctx, c, execRunOptions{Cmd: eosProbe, Stdout: true})
		if err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			// `except APIError: return 2` catches ONLY APIError, and
			// `MachineBinaryError` is not one (oracle: its MRO is
			// Exception → BaseException). An image without `cat` therefore
			// raises out of `_wait_startup_execution`, out of `connect` and
			// out of `exec` — it does not become the "container vanished"
			// answer 2, which `connect` swallows and `exec` reports as
			// MachineNotRunning.
			var binaryErr *kerrors.BinaryError
			if errors.As(err, &binaryErr) {
				return 0, err
			}
			return startupWaitAPIError, nil
		}

		success := result.ExitCode != nil && *result.ExitCode == 0

		if !printed && !success {
			if err := event.Dispatch(s.manager.dispatcher, event.MachineStartupWaitStarted{}); err != nil {
				return 0, err
			}
			printed = true
		}

		interrupted, err := util.WaitUserInput()
		if err != nil {
			return 0, err
		}
		if interrupted {
			startupWaited = startupWaitInterrupted
			if success {
				startupWaited = startupWaitCompleted
			}
			break
		}

		if success {
			break
		}

		if nRetries != nil {
			if retries == *nRetries {
				break
			}
			retries++
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(retryInterval):
		}
	}

	return startupWaited, nil
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// copyFiles is `DockerMachine.copy_files` (`DockerMachine.py:956`):
// `put_archive(path, tar_data)`.
func (s *machineService) copyFiles(ctx context.Context, c *Container, path string, tarData []byte) error {
	return s.manager.api.CopyToContainer(ctx, c.ID, path, bytes.NewReader(tarData),
		container.CopyToContainerOptions{})
}

// retrieveFiles is `DockerMachine.retrieve_files` (`DockerMachine.py:970`):
// pull a path out of the container as a tar and extract it into dst.
//
// # No member sanitisation
//
// Python calls `tarfile.extractall(path=dst)` with no `filter=`, i.e. the
// fully-trusted extraction, so an archive holding `../` members writes outside
// dst. The port reproduces that (docker-backend.md gotcha 28: "Go's `tar`
// extraction must not silently add safety that changes behaviour (record a
// PROPOSED-DIVERGENCE if hardening is wanted)"), and PROPOSED-DIVERGENCES.md
// carries the request to harden it. An ABSOLUTE member name is the one case
// that differs — it is rooted under dst rather than honoured — because Docker's
// `get_archive` cannot emit one (DIVERGENCES.md 69, [extractTar]). The source
// of the archive is the Docker daemon relaying a path chosen by the caller, so
// the exposure needs a container that is already hostile.
//
// Python streams the archive into a temporary file first and reopens it,
// because `tarfile` wants a seekable object; Go's `tar.Reader` is sequential,
// so the daemon's stream is extracted directly and the temporary file has no
// counterpart.
func (s *machineService) retrieveFiles(ctx context.Context, c *Container, src, dst string) error {
	bits, _, err := s.manager.api.CopyFromContainer(ctx, c.ID, src)
	if err != nil {
		return err
	}
	defer func() { _ = bits.Close() }()

	return extractTar(bits, dst)
}

// bridgedIfaceNumber reads `machine.meta['bridged_iface']` as the int Python's
// arithmetic needs.
//
// The second result is false when the meta holds something else, which is what
// every lab.conf produces: the parser stores every meta as a string, so a
// scenario that sets `bridged_iface` puts a `str` there and Python's `>` and
// `+` both raise (DIVERGENCES.md 1). Callers turn that into the TypeError of
// the branch they are in.
func bridgedIfaceNumber(machine *model.Machine) (int, bool) {
	if machine.Meta.BridgedIface.Kind() != model.KindInt {
		return 0, false
	}
	value, ok := machine.Meta.BridgedIface.Value().(int64)
	if !ok {
		return 0, false
	}
	return int(value), true
}
