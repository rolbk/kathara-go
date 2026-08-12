// This file is `KubernetesMachine.py`: devices as Deployments, and the pods
// those Deployments produce.
//
// The command templates live in startup.go, the naming rules in naming.go and
// the fan-out shape in pool.go; what is left here is the order of operations and
// the pod watcher, which is the part a golden sees.

package kubernetes

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// rpFilterSysctl is `RP_FILTER_NAMESPACE` (`KubernetesMachine.py:36`).
const rpFilterSysctl = "net.ipv4.conf.%s.rp_filter"

// maxRestartCount is `MAX_RESTART_COUNT` (`KubernetesMachine.py:37`): the pod
// watcher stops waiting for a device that has restarted this many times.
const maxRestartCount = 3

// maxTimeError is `MAX_TIME_ERROR` (`KubernetesMachine.py:38`): 180 seconds
// without a single pod event ends the deploy.
const maxTimeError = 180 * time.Second

// mountVolumesOption is the scenario option `deploy_machines` writes before the
// fan-out and deletes after it (`KubernetesMachine.py:177,207`). The interactive
// volume prompt writes it too, which is how a declined prompt reaches
// [model.Machine.GetVolumes].
const mountVolumesOption = "_mount_volumes"

// realNameMeta is the meta `create` writes with the Deployment name
// (`KubernetesMachine.py:357`) and that `_build_definition` reads back four
// times — as the container name, the pod hostname, the Deployment name and the
// ConfigMap name's first half.
const realNameMeta = "real_name"

// machineService is `KubernetesMachine` (`KubernetesMachine.py:134`).
type machineService struct {
	clientset  kubernetes.Interface
	namespace  *namespaceService
	configMap  *configMapService
	executor   executorFactory
	dispatcher *event.Dispatcher
	settings   *settings.Settings

	// startupTimeout is `MAX_TIME_ERROR` (`KubernetesMachine.py:38`), the pod
	// watcher's watchdog interval. It is a field rather than the constant so a
	// test can drive the timeout without waiting three minutes; nothing outside
	// the tests sets it to anything but [maxTimeError].
	startupTimeout time.Duration

	// shutdownTimeout is the same interval on the undeploy path, where Python
	// has NO timer at all and `wait_thread.join()` can block forever
	// (CONCURRENCY.tsv row `KubernetesMachine.py:599`). The register rules the
	// port "add a sane timeout (deviation from Python's infinite hang, document
	// it)"; [maxTimeError] is that timeout, reused rather than invented, and
	// DIVERGENCES.md records it. A test drives it through this field.
	shutdownTimeout time.Duration

	// portName is `str(uuid.uuid4()).replace('-', '')[0:15]`
	// (`KubernetesMachine.py:419`), the name of a published container port.
	//
	// It is a field so a test can pin it: the value is random by construction
	// and k8s-backend.md G8 rules that a golden has to mask it. The default is
	// [randomPortName].
	portName func() string
}

// randomPortName is the port-name generator: fifteen lower-case hex characters,
// which is what `uuid4().hex[:15]` produces.
//
// It inherits a latent bug. A Kubernetes container-port name must be an
// IANA_SVC_NAME — at most fifteen characters, lower-case alphanumeric and `-`,
// and at least one non-digit — so the roughly one-in-1200 name that comes out
// all digits is rejected by the API server. Python has exactly the same odds
// and the same failure; DIVERGENCES.md records it rather than fixing it,
// because fixing it changes the name of every port on every device.
func randomPortName() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail on any supported platform; a name is still
		// needed, and an empty one is rejected by the API server the same way a
		// bad one is.
		return ""
	}
	return hex.EncodeToString(raw[:])[:15]
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// DeployMachines is `deploy_machines` (`KubernetesMachine.py:146`).
//
// Order, all of it observable:
//
//  1. the both-filters guard (TRUTHINESS here, unlike [machineService.Undeploy]);
//  2. filter the scenario's devices, preserving scenario order;
//  3. write the `_mount_volumes` option and dispatch `machines_with_volumes`
//     when any device asks for volumes, so the CLI can prompt;
//  4. start the pod watcher, which owns `machines_deploy_started`,
//     `machine_deployed` and `machines_deploy_ended`;
//  5. the fan-out — parallel when the scenario has no `lab.dep`, strictly
//     sequential in dependency order when it has;
//  6. join the watcher, then remove `_mount_volumes`.
//
// Step 3 has no image pass: unlike the Docker backend there is nothing to pull,
// `check_image` is a no-op and the cluster resolves the image itself.
//
// Step 6's `del lab.general_options['_mount_volumes']` has no [model.Lab]
// method, so the value step 3 computed is written back instead — behaviourally
// identical for every reader, since [model.Machine.GetVolumes] falls back to
// exactly `policy in ("Prompt", "Always")` when the option is absent. What it
// preserves is that a declined prompt does not persist into the next deploy of
// the same [model.Lab]. PROPOSED-DIVERGENCES.md asks for `Lab.RemoveOption`.
// Python leaks the option on the error path too — the `del` is not in a
// `finally` — and so does this.
//
// # The watcher and the watchdog
//
// Python starts `_wait_machines_startup` on a non-daemon thread and joins it
// after the fan-out; on a fan-out error the join is SKIPPED and the thread
// leaks, kept alive until its 180 s timer fires (k8s-backend.md G4). Here the
// watcher is a goroutine and is always joined, because a leaked goroutine holds
// a watch connection open for the life of the process and the leak has no
// observable behaviour to preserve beyond the watchdog — which is scoped to
// this call instead. DIVERGENCES.md records both halves.
//
// The watchdog itself is OQ-10's ruling (PACKAGE_GRAPH.md §2.8): Python's
// `os.kill(os.getpid(), SIGINT)` becomes a context cancellation, the
// `kubectl -n {hash} get pods` message is preserved verbatim, and the call
// answers [context.DeadlineExceeded] — which the CLI renders exactly as it
// renders a Ctrl-C, exit 0 with the interrupt warning
// (JSON_CLI_CONTRACT.md §6.2), which is what Python's SIGINT produced.
//
// Errors: [kerrors.ErrSelectedOrExcludedMachines], then whatever any worker
// answers, then the watchdog.
func (s *machineService) DeployMachines(ctx context.Context, lab *model.Lab, selected, excluded kathara.NameSet) error {
	if len(selected) > 0 && len(excluded) > 0 {
		return kerrors.ErrSelectedOrExcludedMachines
	}

	machines := filterMachines(lab.Machines(), selected, excluded)

	policy := s.settings.VolumeMountPolicy
	canMount := policy == "Prompt" || policy == "Always"
	lab.AddOption(mountVolumesOption, model.Bool(canMount))

	withVolumes := make([]*model.Machine, 0, len(machines))
	for _, machine := range machines {
		if machine.Meta.Volumes.Len() > 0 {
			withVolumes = append(withVolumes, machine)
		}
	}
	if len(withVolumes) > 0 {
		if err := event.Dispatch(s.dispatcher, event.MachinesWithVolumes{Lab: lab, Machines: withVolumes}); err != nil {
			return err
		}
	}

	// `set([k for k, _ in machines]) if selected_machines or excluded_machines
	// else None` — the wait set is the FILTERED names, or "everything" when no
	// filter was given.
	var watched kathara.NameSet
	if len(selected) > 0 || len(excluded) > 0 {
		watched = kathara.NewNameSet()
		for _, machine := range machines {
			watched[machine.Name] = struct{}{}
		}
	}

	// The watchdog cancels this, not the caller's context, so a timeout aborts
	// the deploy and nothing above it.
	opCtx, cancelOp := context.WithCancelCause(ctx)
	defer cancelOp(nil)

	// The watch is opened HERE and not inside the goroutine: ORDERING.tsv row
	// O17 requires the watch to be established before the objects it must not
	// miss events from are created, and Python only gets that by starting the
	// thread first and hoping.
	watcher, err := s.clientset.CoreV1().Pods(lab.Hash).Watch(opCtx, metav1.ListOptions{})
	if err != nil {
		return translateAPI(err)
	}

	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		defer watcher.Stop()
		s.waitMachinesStartup(opCtx, cancelOp, watcher, lab, watched)
	}()

	var deployErr error
	if !lab.HasDependencies {
		deployErr = runChunked(opCtx, machines, s.deployMachine)
	} else {
		// CONCURRENCY.tsv row `KubernetesMachine.py:201`: a plain loop, first
		// error aborts immediately, and the order is `lab.dep`'s — which
		// `Lab.ApplyDependencies` has already stable-sorted into `lab.machines`.
		for _, machine := range machines {
			if deployErr = s.deployMachine(opCtx, machine); deployErr != nil {
				break
			}
		}
	}

	if deployErr != nil {
		// Python SKIPS `wait_thread.join()` here and returns, leaking the
		// watcher until its own timer fires (k8s-backend.md G4). The watcher is
		// stopped instead — see the note above — and the failure is reported
		// with the same promptness.
		cancelOp(nil)
		<-waitDone
		return deployErr
	}

	// The join is what makes `lstart` block until the devices are actually
	// Ready: the watcher returns when every watched device has reported, when
	// the 180 s watchdog fires, or when the caller's context ends. A scenario
	// with NO devices waits too, because the termination test is only evaluated
	// on an event — Python's own behaviour, and unreachable from the CLI, which
	// rejects an empty scenario before it gets here.
	<-waitDone

	if cause := context.Cause(opCtx); errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}

	lab.AddOption(mountVolumesOption, model.Bool(canMount))
	return nil
}

// filterMachines is the dict comprehension of `deploy_machines`
// (`KubernetesMachine.py:163-171`).
//
// Both filters are TRUTHINESS-tested here, so an empty set is "no filter" and
// `lstart`'s empty sets mean "everything" — the exact inverse of
// [machineService.Undeploy] (SYNTHESIS §1.7, NILABILITY.tsv:55-57).
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

// deployMachine is `_deploy_machine` (`KubernetesMachine.py:284`): create, and
// nothing else.
//
// Unlike the Docker backend's worker it dispatches no `machine_deployed` event:
// on Megalos that event comes from the pod watcher, when the pod reports Ready
// (`KubernetesMachine.py:260`), so the progress bar tracks readiness rather than
// submission.
func (s *machineService) deployMachine(ctx context.Context, machine *model.Machine) error {
	return s.Create(ctx, machine)
}

// waitMachinesStartup is `_wait_machines_startup`
// (`KubernetesMachine.py:209`): count pods into Ready until every watched
// device has reported, or has restarted too many times to be worth waiting for.
//
// # The counting is event-based, not state-based
//
// A pod that reports Ready twice counts twice (k8s-backend.md G6), so the
// termination test `machines_ready + machines_failed == len(machines)` can fire
// early — and a device that never events at all makes it never fire, which is
// what the watchdog is for. Ported as-is; the fix is a different program.
//
// The `machines_deploy_ended` event is dispatched only when EVERY watched
// device reported Ready, so a failed one leaves the progress bar open
// (`KubernetesMachine.py:281-282`).
//
// # The pod without a `name` label
//
// Python indexes `event['object'].metadata.labels['name']` on every event of an
// UNFILTERED watch over the namespace, so a foreign pod raises KeyError, kills
// the watcher thread, and silently costs the deploy its `machines_deploy_ended`
// event. A goroutine may not crash (PORT_SPEC §10), so such a pod is skipped;
// the namespace belongs to one scenario, so nothing this backend creates is
// affected. DIVERGENCES.md records it.
func (s *machineService) waitMachinesStartup(ctx context.Context, cancelOp context.CancelCauseFunc, watcher watch.Interface, lab *model.Lab, watched kathara.NameSet) {
	// `{k: v for (k, v) in lab.machines.items() if k in selected_machines}` —
	// the DENOMINATOR of the termination test, recomputed from the scenario
	// rather than taken from the caller.
	items := make([]*model.Machine, 0, len(lab.Machines()))
	for _, machine := range lab.Machines() {
		if len(watched) == 0 || watched.Has(machine.Name) {
			items = append(items, machine)
		}
	}
	total := len(items)

	if err := event.Dispatch(s.dispatcher, event.MachinesDeployStarted{Machines: items}); err != nil {
		slog.Debug("Failed to dispatch machines_deploy_started.", "error", err)
	}

	ready, failed := 0, 0

	timeout := s.startupTimeout
	if timeout <= 0 {
		timeout = maxTimeError
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-timer.C:
			// `raise_timeout_error` (`KubernetesMachine.py:228`): the message is
			// preserved verbatim, the `os.kill(os.getpid(), SIGINT)` is the
			// context cancellation of the OQ-10 ruling.
			slog.Error("Network scenario startup is not responding for over " +
				strconv.Itoa(int(maxTimeError/time.Second)) + " seconds, exiting. " +
				"To check devices status, use the following command:\n\t" +
				"kubectl -n " + lab.Hash + " get pods")
			cancelOp(context.DeadlineExceeded)
			return

		case ev, open := <-watcher.ResultChan():
			if !open {
				return
			}

			// "Every new event, cancel and create the timer."
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)

			pod, ok := ev.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			machineName, labelled := pod.Labels[labelName]
			if !labelled {
				continue
			}

			if len(watched) == 0 || watched.Has(machineName) {
				// `f"Event: {event['type']} - Pod: {event['object'].metadata.name}
				// (Device {machine_name})"` (`KubernetesMachine.py:251`).
				slog.Debug("Event: " + string(ev.Type) + " - Pod: " + pod.Name + " (Device " + machineName + ")")

				if len(pod.Status.ContainerStatuses) > 0 {
					status := pod.Status.ContainerStatuses[0]
					restarts := int(status.RestartCount)

					switch {
					case status.Ready:
						ready++
						// `f"Device `{machine_name}` ready."`
						// (`KubernetesMachine.py:258`).
						slog.Debug("Device `" + machineName + "` ready.")
						if err := event.Dispatch(s.dispatcher, event.MachineDeployed{Name: machineName}); err != nil {
							slog.Debug("Failed to dispatch machine_deployed.", "error", err)
						}
					case restarts >= maxRestartCount:
						// `KubernetesMachine.py:263-267`, which names the device
						// inside the sentence and carries no trailing fields.
						slog.Warn("Stopping to wait device `" + machineName + "` since it restarted more than " +
							strconv.Itoa(maxRestartCount) + " times. " +
							"For a detailed log use the following command:\n\t" +
							"kubectl -n " + lab.Hash + " describe pod " + pod.Name)
						failed++
					case restarts > 0 && status.State.Waiting != nil && status.State.Waiting.Reason == "CrashLoopBackOff":
						// `f"Device `{machine_name}` has been restarted
						// {restart_count} times."` (`KubernetesMachine.py:273`).
						slog.Warn("Device `" + machineName + "` has been restarted " + strconv.Itoa(restarts) + " times.")
					}
				}
			}

			// Outside the name filter, exactly as Python has it.
			if ready+failed == total {
				if ready == total {
					if err := event.Dispatch(s.dispatcher, event.MachinesDeployEnded{}); err != nil {
						slog.Debug("Failed to dispatch machines_deploy_ended.", "error", err)
					}
				}
				return
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Create
// ---------------------------------------------------------------------------

// Create is `create` (`KubernetesMachine.py:297`): warn about everything
// Megalos cannot honour, merge the sysctls, name the Deployment, then submit
// the ConfigMap and the Deployment.
//
// The three warnings are unconditional refusals, not downgrades: `privileged`,
// `bridged` and `ulimits` have no Kubernetes counterpart here — every Megalos
// container is privileged anyway, because it has to be to run sysctls.
//
// # What is inside the try
//
// Python's `except ApiException` wraps the ConfigMap create, the definition
// build AND the Deployment create, so a 409 from the ConfigMap is reported as
// [kerrors.ErrMachineAlreadyExists] just as a 409 from the Deployment is. That
// is the shape below.
//
// Errors: [kerrors.ErrMachineAlreadyExists] on a 409,
// [kerrors.ErrKubernetesAPI] for every other API failure, plus the model's own
// option errors and the volume [kerrors.ErrPermission].
func (s *machineService) Create(ctx context.Context, machine *model.Machine) error {
	// `"Creating device `%s`..." % machine.name` (`KubernetesMachine.py:309`).
	slog.Debug("Creating device `" + machine.Name + "`...")

	_, hasGlobalPrivileged := machine.Lab.GlobalMachineMetadata("privileged")
	if hasGlobalPrivileged || machine.IsPrivileged() {
		slog.Warn("Privileged option is not supported on Megalos. It will be ignored on device `" + machine.Name + "`.")
	}

	_, hasGlobalBridged := machine.Lab.GlobalMachineMetadata("bridged")
	if hasGlobalBridged || machine.IsBridged() {
		slog.Warn("Bridged option is not supported on Megalos. It will be ignored on device `" + machine.Name + "`.")
	}

	if machine.Ulimits().Len() > 0 {
		slog.Warn("Ulimit option is not supported on Megalos. It will be ignored on device `" + machine.Name + "`.")
	}

	if execCommand, ok := machine.Lab.GlobalMachineMetadata("exec"); ok {
		if _, _, err := machine.AddMeta("exec", execCommand.String()); err != nil {
			return err
		}
	}

	if err := mergeSysctls(machine); err != nil {
		return err
	}

	if _, _, err := machine.AddMeta(realNameMeta, DeploymentName(s.settings.DevicePrefix, machine.Name)); err != nil {
		return err
	}

	configMap, err := s.configMap.DeployForMachine(ctx, machine)
	if err != nil {
		return s.translateCreateError(err, machine.Name)
	}

	definition, err := s.buildDefinition(machine, configMap)
	if err != nil {
		return s.translateCreateError(err, machine.Name)
	}

	created, err := s.clientset.AppsV1().Deployments(machine.Lab.Hash).Create(ctx, definition, metav1.CreateOptions{})
	if err != nil {
		return s.translateCreateError(err, machine.Name)
	}
	machine.APIObject = created

	return nil
}

// translateCreateError is the `except ApiException` of `create`
// (`KubernetesMachine.py:366-370`): a 409 is a duplicate device, everything
// else is re-raised — which here means wrapped in the taxonomy's
// passthrough code so the CLI can name it (ERROR_CODES.md §1.3).
//
// A non-API error — a model option failure, a sysctl TypeError, a volume
// permission refusal — passes straight through: Python's `except` does not
// catch those either.
func (s *machineService) translateCreateError(err error, machineName string) error {
	if !isAPIException(err) {
		return err
	}
	if isConflict(err) {
		return kerrors.NewMachineAlreadyExists(machineName)
	}
	return translateAPI(err)
}

// mergeSysctls is `machine.meta['sysctls'] = {**sysctl_parameters,
// **machine.meta['sysctls']}` (`KubernetesMachine.py:337-355`).
//
// The defaults come first, in their literal order, and the device's own entries
// overwrite by key WITHOUT moving it — a Python dict assignment keeps an
// existing key's position — so a device that sets `net.ipv4.ip_forward=0` sees
// its value at the DEFAULT's position, not appended at the end. That order is
// the order of the `sysctl -w -q` commands in the postStart hook
// (k8s-backend.md O4), which the goldens read.
//
// The IPv6 half is a branch, not an override: the two arms set different KEYS,
// so an IPv6-enabled device carries `accept_ra` and `icmp.ratelimit` that a
// disabled one does not, and a disabled one carries `default.forwarding` that an
// enabled one does not.
//
// This MUTATES the device, as Python does. The merged dict is what
// [SysctlCommands] renders and what a second `create` on the same object would
// merge again — idempotently, since the defaults are already present.
func mergeSysctls(machine *model.Machine) error {
	merged := model.NewOrderedMap[string, model.Scalar]()

	for _, iface := range []string{"all", "default", "lo"} {
		merged.Set(strings.Replace(rpFilterSysctl, "%s", iface, 1), model.Int(0))
	}
	merged.Set("net.ipv4.ip_forward", model.Int(1))
	merged.Set("net.ipv4.icmp_ratelimit", model.Int(0))

	ipv6, err := machine.IsIPv6Enabled()
	if err != nil {
		return err
	}
	if ipv6 {
		merged.Set("net.ipv6.conf.all.forwarding", model.Int(1))
		merged.Set("net.ipv6.conf.all.accept_ra", model.Int(0))
		merged.Set("net.ipv6.icmp.ratelimit", model.Int(0))
		merged.Set("net.ipv6.conf.default.disable_ipv6", model.Int(0))
		merged.Set("net.ipv6.conf.all.disable_ipv6", model.Int(0))
	} else {
		merged.Set("net.ipv6.conf.default.disable_ipv6", model.Int(1))
		merged.Set("net.ipv6.conf.all.disable_ipv6", model.Int(1))
		merged.Set("net.ipv6.conf.default.forwarding", model.Int(0))
		merged.Set("net.ipv6.conf.all.forwarding", model.Int(0))
	}

	for _, entry := range machine.Sysctls().Entries() {
		merged.Set(entry.Key, entry.Value)
	}

	machine.Meta.Sysctls = merged
	return nil
}

// realName reads the `real_name` meta `create` wrote
// (`KubernetesMachine.py:357`).
//
// Python indexes `machine.meta['real_name']` unguarded at four sites in
// `_build_definition`, so calling that function on a device `create` has not
// touched is a KeyError. The Python test suite does exactly that — its fixtures
// set `real_name` by hand — so the meta is treated as an ordinary read here and
// an absent one yields "", which is what a hand-built device gets.
func realName(machine *model.Machine) string {
	if value, ok := machine.Meta.Extras.Get(realNameMeta); ok {
		return value.String()
	}
	return ""
}

// buildDefinition is `_build_definition` (`KubernetesMachine.py:372`): the
// Deployment, built from a device and the ConfigMap holding its files.
//
// The construction order below is Python's statement order, and it matters in
// exactly one place — the volume loop, whose `PermissionError` must fire before
// anything else can fail — but it is kept throughout so that a reader can put
// the two files side by side.
//
// # The two volume lists, and why they disagree
//
// `volumeMounts` comes from `machine.get_volumes()`, which raises
// `MountDeniedError` when the policy forbids mounting — caught here, warned
// about, and the mounts are simply absent. `volumes` comes from
// `machine.meta["volumes"]` RAW, with no policy consulted. So a `Never` policy
// produces a pod that DECLARES its hostPath volumes and mounts none of them
// (k8s-backend.md G10, EXPECTATIONS-k8s `test_create_volume_never`). Ported
// as-is.
//
// Both loops enumerate the same ordered map, so `volume0`…`volumeN` line up
// between the two — unless the policy denied the mounts, in which case the
// mounts are absent entirely and no misalignment is possible. A Go map here
// would desynchronise them silently (ORDERING.tsv).
//
// Errors: [kerrors.ErrPermission] for a volume whose host directory the user
// cannot use, plus whatever the model's `get_mem`, `get_cpu` and `shlex`
// answer.
func (s *machineService) buildDefinition(machine *model.Machine, configMap *corev1.ConfigMap) (*appsv1.Deployment, error) {
	volumeMounts := []corev1.VolumeMount{}
	if configMap != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "hostlab", MountPath: "/tmp/kathara"})
	}
	if s.settings.HostShared {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "shared", MountPath: "/shared"})
	}

	volumes, err := machine.GetVolumes()
	switch {
	case err == nil:
		for idx, entry := range volumes.Entries() {
			missing, err := util.CheckDirectoryPermissions(entry.Key, entry.Value.Mode)
			if err != nil {
				return nil, err
			}
			if len(missing) > 0 {
				return nil, kerrors.NewVolumePermission(entry.Key, entry.Value.GuestPath, missing)
			}
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "volume" + strconv.Itoa(idx),
				MountPath: entry.Value.GuestPath,
				ReadOnly:  entry.Value.Mode == "ro",
			})
		}
	case errors.Is(err, kerrors.ErrMountDenied):
		slog.Warn("Volumes of device `" + machine.Name + "` will not be mounted.")
	default:
		return nil, err
	}

	// "Machine must be executed in privileged mode to run sysctls."
	privileged := true
	securityContext := &corev1.SecurityContext{Privileged: &privileged}

	var containerPorts []corev1.ContainerPort
	for _, entry := range machine.Ports().Entries() {
		containerPorts = append(containerPorts, corev1.ContainerPort{
			Name:          s.portName(),
			ContainerPort: int32(entry.Value),
			HostPort:      int32(entry.Key.HostPort),
			Protocol:      corev1.Protocol(strings.ToUpper(entry.Key.Protocol)),
		})
	}

	memory, err := machine.GetMem()
	if err != nil {
		return nil, err
	}
	cpus, err := machine.GetCPU(1000)
	if err != nil {
		return nil, err
	}

	// `if memory or cpus:` then `if memory:` / `if cpus:` — TRUTHINESS on both,
	// twice. [model.Machine.GetCPU] answers a pointer so that a real 0 can be
	// told from "unset", and Python cannot: `cpus=0.0005` scales to the int 0,
	// which is falsy, so no `cpu` limit is written and — with no memory limit —
	// no `resources` block at all.
	hasMemory := memory != ""
	hasCPUs := cpus != nil && *cpus != 0

	var resources corev1.ResourceRequirements
	if hasMemory || hasCPUs {
		limits := corev1.ResourceList{}
		if hasMemory {
			// `memory.upper()`: `64m` becomes `64M`, which Kubernetes reads as
			// 64 megabytes where `64m` would have been 64 MILLIbytes. The
			// uppercase is doing real work.
			//
			// It also produces strings Kubernetes has no suffix for.
			// `Machine.get_mem` accepts the units b/k/m/g (`model/Machine.py:499`),
			// so `mem=100k` and `mem=5b` arrive here as `100K` and `5B` — and the
			// quantity grammar knows lower-case `k` and no `B` at all. Python
			// does not notice: the string goes into the request body and the API
			// SERVER rejects it, which `create`'s `except ApiException` re-raises
			// as the `(ApiException)` line of ERROR_CODES.md §1.3. client-go
			// parses locally instead, so the refusal is the same refusal one API
			// call earlier and carries the same code. DIVERGENCES.md records the
			// missing request.
			quantity, err := resource.ParseQuantity(strings.ToUpper(memory))
			if err != nil {
				return nil, kerrors.NewKubernetesAPI(err)
			}
			limits[corev1.ResourceMemory] = quantity
		}
		if hasCPUs {
			quantity, err := resource.ParseQuantity(strconv.FormatInt(*cpus, 10) + "m")
			if err != nil {
				return nil, err
			}
			limits[corev1.ResourceCPU] = quantity
		}
		resources = corev1.ResourceRequirements{Limits: limits}
	}

	// `machine.meta["shell"] if "shell" in machine.meta else
	// Setting.device_shell` — `Machine.get_shell()` spelled inline. The two
	// branches are identical, and the model's accessor already carries the
	// settings default (OQ-4).
	shell := machine.GetShell()

	sysctlCommands, err := SysctlCommands(machine.Sysctls())
	if err != nil {
		return nil, err
	}
	machineCommands := ":"
	if commands := machine.ExecCommands(); len(commands) > 0 {
		machineCommands = strings.Join(commands, "; ")
	}

	// The postStart hook is launched asynchronously by the API server when the
	// container is Ready — at which point the pod has its volumes and its
	// network interfaces up, which is why the startup script can rely on both.
	lifecycle := &corev1.Lifecycle{
		PostStart: &corev1.LifecycleHandler{
			Exec: &corev1.ExecAction{
				Command: []string{shell, "-c", StartupCommandsString(machine.Name, sysctlCommands, machineCommands)},
			},
		},
	}

	env := []corev1.EnvVar{{Name: megalosShellEnv, Value: shell}}
	for _, entry := range machine.Envs().Entries() {
		env = append(env, corev1.EnvVar{Name: entry.Key, Value: entry.Value})
	}

	var entrypoint []string
	if machine.Meta.Entrypoint.IsSet() {
		entrypoint, err = ShlexSplit(machine.Meta.Entrypoint.String())
		if err != nil {
			return nil, err
		}
	}

	// `machine.meta["args"] if "args" in machine.meta and machine.meta["args"]
	// else None` — a truthiness test, so an empty string or an empty list is
	// "no args"; then `shlex.split` only for the string form, since the CLI's
	// argparse REMAINDER already hands over a list.
	var args []string
	if machine.Meta.Args.IsSet() && machine.Meta.Args.Truthy() {
		if list, isList := machine.Meta.Args.AsStrings(); isList {
			args = list
		} else {
			args, err = ShlexSplit(machine.Meta.Args.String())
			if err != nil {
				return nil, err
			}
		}
	}

	container := corev1.Container{
		Name:            realName(machine),
		Image:           machine.GetImage(),
		Lifecycle:       lifecycle,
		Stdin:           true,
		TTY:             true,
		ImagePullPolicy: corev1.PullPolicy(stringOrEmpty(s.settings.ImagePullPolicy)),
		Ports:           containerPorts,
		Resources:       resources,
		VolumeMounts:    volumeMounts,
		SecurityContext: securityContext,
		Env:             env,
		Command:         entrypoint,
		Args:            args,
	}

	attachments, err := networkAttachments(machine)
	if err != nil {
		return nil, err
	}

	podLabels := ObjectLabels(machine.Name)
	gracePeriod := int64(0)

	podVolumes := []corev1.Volume{}
	if configMap != nil {
		// The hostlab is the scenario's base64'd .tar.gz, deployed as a
		// ConfigMap; it is mounted into /tmp and extracted by the postStart hook.
		podVolumes = append(podVolumes, corev1.Volume{
			Name: "hostlab",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMap.Name},
				},
			},
		})
	}
	if s.settings.HostShared {
		hostPathType := corev1.HostPathDirectoryOrCreate
		podVolumes = append(podVolumes, corev1.Volume{
			Name: "shared",
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: "/home/shared", Type: &hostPathType},
			},
		})
	}
	for idx, entry := range machine.Meta.Volumes.Entries() {
		hostPathType := corev1.HostPathDirectoryOrCreate
		podVolumes = append(podVolumes, corev1.Volume{
			Name: "volume" + strconv.Itoa(idx),
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{Path: entry.Key, Type: &hostPathType},
			},
		})
	}

	imagePullSecrets := []corev1.LocalObjectReference{}
	if stringOrEmpty(s.settings.DockerConfigJSON) != "" {
		imagePullSecrets = append(imagePullSecrets, corev1.LocalObjectReference{Name: privateRegistrySecretName})
	}

	replicas := int32(1)
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:   realName(machine),
			Labels: podLabels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: podLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					DeletionGracePeriodSeconds: &gracePeriod,
					Annotations:                map[string]string{networkAttachmentAnnotation: attachments},
					Labels:                     podLabels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{container},
					Hostname:   realName(machine),
					DNSPolicy:  corev1.DNSNone,
					// A fake nameserver, purely to override the cluster's:
					// the startup script unmounts /etc/resolv.conf so the
					// scenario's own file can replace it.
					DNSConfig:        &corev1.PodDNSConfig{Nameservers: []string{"127.0.0.1"}},
					Volumes:          podVolumes,
					ImagePullSecrets: imagePullSecrets,
				},
			},
		},
	}, nil
}

// networkAttachments is the annotation loop of `_build_definition`
// (`KubernetesMachine.py:485-497`).
//
// The array order is the interface order and the `interface` field is
// `"net%d" % idx` — the interface NUMBER, not the array position — so the two
// carry different information and both are observable: Multus attaches in array
// order (ORDERING.tsv, k8s-backend.md O1) while the guest NIC name comes from
// the number. `get_lab_from_api` rebuilds interfaces from the array POSITION,
// which round-trips only because the two agree for a scenario with no holes.
//
// A tombstoned interface slot — what `remove_interface` leaves behind — has no
// `.link` and Python dies with an AttributeError, which is reproduced: the
// annotation is what wires the device, and silently skipping a slot would
// deploy a different topology.
//
// A collision domain that has not been deployed has `api_object is None` and
// `None["metadata"]` is a TypeError, which is the crash that makes
// "links before machines" a hard ordering and not a preference (k8s-backend.md
// G2/O9).
func networkAttachments(machine *model.Machine) (string, error) {
	attachments := make([]podNetworkAttachment, 0, len(machine.Interfaces()))

	for _, iface := range machine.Interfaces() {
		if iface.IsTombstone() {
			return "", newPyAttributeError("link")
		}
		network, ok := iface.Link.APIObject.(*Network)
		if !ok || network == nil {
			return "", newPySubscriptError()
		}
		attachments = append(attachments, podNetworkAttachment{
			Name:        NetworkNameOf(network),
			Namespace:   machine.Lab.Hash,
			Interface:   "net" + strconv.Itoa(iface.Number),
			KatharaLink: iface.Link.Name,
			MAC:         iface.MAC,
		})
	}

	return encodeNetworkAttachments(attachments), nil
}

// ---------------------------------------------------------------------------
// Undeploy
// ---------------------------------------------------------------------------

// Undeploy is `undeploy` (`KubernetesMachine.py:570`).
//
// The both-filters guard here is `is not None`, not truthiness, so two non-nil
// EMPTY sets are an error — while the manager's own pre-check is truthiness and
// lets them through (SYNTHESIS §1.7, k8s-backend.md G3). That is the pair of
// tests, not a bug being routed around.
//
// # The wait set
//
// It is not simply the filtered pods. Python computes it from the FULL pod
// listing and then narrows:
//
//	selected is not None → the selection ITSELF when non-empty, else every pod
//	excluded is not None → every pod when the exclusion is empty, else the
//	                       difference
//
// The `selected` arm is the interesting one: the wait set is the caller's names
// and not the pods that matched, so undeploying a device that is not running
// waits for a DELETED event that never comes — until [machineService.shutdownTimeout]
// fires or the context ends.
//
// # The wait
//
// `wait_thread.join()` (`KubernetesMachine.py:609`) is unconditional on the
// success path, so `lclean` BLOCKS until every watched device has produced a
// DELETED event — which is also what makes `machine_undeployed` and
// `machines_undeploy_ended` observable. The join is reproduced; what is added is
// the watchdog CONCURRENCY.tsv row `KubernetesMachine.py:599` asks for, because
// Python's join has no timeout and hangs forever when an event is missed.
//
// On a fan-out failure Python SKIPS the join and returns, leaking the thread;
// the watcher is stopped here instead, for the reason DIVERGENCES.md 72 gives
// about the deploy path.
func (s *machineService) Undeploy(ctx context.Context, labHash string, selected, excluded kathara.NameSet) error {
	if selected != nil && excluded != nil {
		return kerrors.ErrSelectedOrExcludedMachines
	}

	pods, err := s.getByFilters(ctx, labHash, "")
	if err != nil {
		return err
	}

	// `{item.metadata.labels["name"] for item in pods}` is indexed unguarded, so
	// a foreign pod that carries `app=kathara` without a `name` label is a
	// KeyError that fails the undeploy. Reproduced rather than skipped: this is
	// the caller's own goroutine and not a watcher (which DIVERGENCES.md 73
	// covers), and swallowing it would put `""` in the wait set — a name no
	// event can ever satisfy, which would turn Python's crash into a stall until
	// the watchdog.
	watched := kathara.NewNameSet()
	for _, pod := range pods {
		name, labelled := pod.Labels[labelName]
		if !labelled {
			return newPyKeyError(labelName)
		}
		watched[name] = struct{}{}
	}

	switch {
	case selected != nil:
		kept := make([]*corev1.Pod, 0, len(pods))
		for _, pod := range pods {
			if selected.Has(pod.Labels[labelName]) {
				kept = append(kept, pod)
			}
		}
		pods = kept
		if len(selected) > 0 {
			watched = selected
		}
	case excluded != nil:
		kept := make([]*corev1.Pod, 0, len(pods))
		for _, pod := range pods {
			if !excluded.Has(pod.Labels[labelName]) {
				kept = append(kept, pod)
			}
		}
		pods = kept
		if len(excluded) > 0 {
			for name := range excluded {
				delete(watched, name)
			}
		}
	}

	if len(pods) == 0 {
		return nil
	}

	// As on the deploy path, the watch is opened before the deletions so that no
	// DELETED event can be missed (ORDERING.tsv row O17).
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	watcher, err := s.clientset.CoreV1().Pods(labHash).Watch(watchCtx, metav1.ListOptions{})
	if err != nil {
		return translateAPI(err)
	}

	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		defer watcher.Stop()
		s.waitMachinesShutdown(watchCtx, watcher, watched)
	}()

	if undeployErr := runChunked(ctx, pods, s.undeployMachine); undeployErr != nil {
		// `with Pool(...)` raising skips `wait_thread.join()` entirely
		// (`KubernetesMachine.py:605-609`), so the failure is reported at once;
		// stopping the watcher rather than leaking it is DIVERGENCES.md 72.
		cancelWatch()
		<-waitDone
		return undeployErr
	}

	// `wait_thread.join()`: the deletions have been submitted, and this is what
	// makes the call block until the pods are actually gone.
	<-waitDone

	return nil
}

// waitMachinesShutdown is `_wait_machines_shutdown`
// (`KubernetesMachine.py:611`): count DELETED events until every watched device
// has produced one.
//
// Python has NO timer on this path — `_wait_machines_startup`'s
// `threading.Timer` has no counterpart here — so a DELETED event that never
// arrives hangs the joining caller forever. CONCURRENCY.tsv row
// `KubernetesMachine.py:599` rules the port adds "a sane timeout"; it is
// [maxTimeError], the same 180 s idle interval the startup watchdog uses, reset
// on every pod event. When it fires the wait simply ends: there is no Python
// message to preserve and no Python error to report, so the undeploy answers
// whatever the deletions answered and only `machines_undeploy_ended` is missing.
// DIVERGENCES.md records it, together with the context PORT_SPEC §0.2 #11 adds.
func (s *machineService) waitMachinesShutdown(ctx context.Context, watcher watch.Interface, watched kathara.NameSet) {
	names := watched.Names()
	if err := event.Dispatch(s.dispatcher, event.MachinesUndeployStarted{Names: names}); err != nil {
		slog.Debug("Failed to dispatch machines_undeploy_started.", "error", err)
	}

	timeout := s.shutdownTimeout
	if timeout <= 0 {
		timeout = maxTimeError
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	cleaned := 0
	for {
		select {
		case <-ctx.Done():
			return

		case <-timer.C:
			slog.Debug("Stopped waiting for device shutdown: no pod event for " +
				strconv.Itoa(int(timeout/time.Second)) + " seconds.")
			return

		case ev, open := <-watcher.ResultChan():
			if !open {
				return
			}

			// "Every new event, cancel and create the timer", as the startup
			// watchdog does (`KubernetesMachine.py:245`).
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(timeout)

			pod, ok := ev.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			machineName, labelled := pod.Labels[labelName]
			if !labelled {
				continue
			}

			if watched.Has(machineName) {
				// `f"Event: {event['type']} - Pod: {event['object'].metadata.name}
				// (Device {machine_name})"` (`KubernetesMachine.py:630`).
				slog.Debug("Event: " + string(ev.Type) + " - Pod: " + pod.Name + " (Device " + machineName + ")")
				if ev.Type == watch.Deleted {
					if err := event.Dispatch(s.dispatcher, event.MachineUndeployed{Name: machineName}); err != nil {
						slog.Debug("Failed to dispatch machine_undeployed.", "error", err)
					}
					cleaned++
				}
			}

			if cleaned == len(watched) {
				if err := event.Dispatch(s.dispatcher, event.MachinesUndeployEnded{}); err != nil {
					slog.Debug("Failed to dispatch machines_undeploy_ended.", "error", err)
				}
				return
			}
		}
	}
}

// Wipe is `wipe` (`KubernetesMachine.py:641`): every Kathará pod in the
// cluster, with no wait and no events.
//
// Nothing reaches it — `KubernetesManager.wipe` deletes namespaces instead
// (k8s-backend.md G26) — and it is ported because it is public surface.
func (s *machineService) Wipe(ctx context.Context) error {
	pods, err := s.getByFilters(ctx, "", "")
	if err != nil {
		return err
	}
	return runChunked(ctx, pods, s.undeployMachine)
}

// undeployMachine is `_undeploy_machine` → `_delete_machine`
// (`KubernetesMachine.py:656,668`): run the shutdown script inside the pod,
// then delete the ConfigMap and the Deployment.
//
// The exec is best-effort — an API failure and a device that is no longer
// running are both swallowed — but a [kerrors.ErrMachineBinary] is NOT: the
// shell named by `_MEGALOS_SHELL` being missing from the image escapes and
// fails the undeploy. That asymmetry is Python's (`except ApiException` /
// `except MachineNotRunningError`, and nothing else) and is preserved.
//
// The Deployment is what is deleted, never the pod: deleting the pod would have
// the Deployment recreate it.
func (s *machineService) undeployMachine(ctx context.Context, pod *corev1.Pod) error {
	machineName := pod.Labels[labelName]
	machineNamespace := pod.Namespace

	shell := EnvVarValueFromPod(pod, megalosShellEnv)
	if shell == "" {
		shell = s.settings.DeviceShell
	}

	_, err := s.exec(ctx, machineNamespace, machineName, []string{shell, "-c", ShutdownCommandsString(machineName)},
		execOptions{})
	switch {
	case err == nil:
	case isAPIException(err), errors.Is(err, kerrors.ErrKubernetesAPI):
	case errors.Is(err, kerrors.ErrMachineNotRunning):
	default:
		return err
	}

	deploymentName := DeploymentName(s.settings.DevicePrefix, machineName)
	s.configMap.DeleteForMachine(ctx, deploymentName, machineNamespace)

	// The one call of `_delete_machine` that is NOT wrapped in an `except`
	// (CONCURRENCY.tsv row `KubernetesMachine.py:605`): its ApiException fails
	// one worker of the fan-out and is the error `undeploy` reports.
	return translateAPI(
		s.clientset.AppsV1().Deployments(machineNamespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}))
}

// ---------------------------------------------------------------------------
// Interactive
// ---------------------------------------------------------------------------

// Connect is `connect` (`KubernetesMachine.py:696`): open an interactive shell
// on a running device.
//
// # The readiness test
//
// `'Running' not in deployment.status.phase` is a SUBSTRING test on the phase
// string, not an equality (`KubernetesMachine.py:718`). No phase Kubernetes
// defines contains "Running" as a substring except "Running" itself, so the two
// agree — but the spelling is Python's and the error it produces,
// [kerrors.ErrMachineNotReady], is a class of its own that only this backend
// raises.
//
// # The shell
//
// An explicit shell is split with `shlex`; an absent one falls back to the pod's
// `_MEGALOS_SHELL` and then to `Setting.device_shell`, and is split too. An
// EMPTY `_MEGALOS_SHELL` is falsy and therefore also falls back
// (NILABILITY.tsv, k8s-backend.md).
//
// Errors: [kerrors.ErrMachineNotRunning] when no pod matches,
// [kerrors.ErrMachineNotReady] when one does but is not Running.
func (s *machineService) Connect(ctx context.Context, labHash, machineName string, opts kathara.ConnectTTYOptions) (kathara.TTYSession, error) {
	pods, err := s.getByFilters(ctx, labHash, machineName)
	if err != nil {
		return nil, err
	}
	if len(pods) == 0 {
		return nil, kerrors.NewMachineNotRunning(machineName)
	}
	pod := pods[len(pods)-1]

	if !strings.Contains(string(pod.Status.Phase), "Running") {
		return nil, kerrors.NewMachineNotReady(machineName)
	}

	shellSource := opts.Shell
	if shellSource == "" {
		shellSource = EnvVarValueFromPod(pod, megalosShellEnv)
		if shellSource == "" {
			shellSource = s.settings.DeviceShell
		}
	}
	shell, err := ShlexSplit(shellSource)
	if err != nil {
		return nil, err
	}

	// `"Connect to device `%s` with shell: %s" % (machine_name, shell)`
	// (`KubernetesMachine.py:727`), where `shell` is the POST-`shlex.split`
	// list and so interpolates as the list's repr.
	slog.Debug("Connect to device `" + machineName + "` with shell: " + util.PythonStrListRepr(shell))

	if opts.Logs && s.settings.PrintStartupLog {
		if err := s.printStartupLog(ctx, labHash, machineName, opts.LogWriter); err != nil {
			return nil, err
		}
	}

	return newTTYSession(ctx, s.executor, execRequest{
		Namespace: labHash,
		Pod:       pod.Name,
		Command:   shell,
		Stdin:     true,
		Stderr:    true,
		TTY:       true,
	})
}

// printStartupLog is the `logs and Setting.print_startup_log` block of
// `connect` (`KubernetesMachine.py:729-745`): `cat` the three log paths and
// print what comes back between two banners.
//
// Python writes to `sys.stdout` from inside the backend and decodes each chunk
// with `chardet`. Neither survives: stream assignment belongs to the CLI
// (JSON_CLI_CONTRACT.md §1.3), so the destination is
// [kathara.ConnectTTYOptions.LogWriter], and the bytes are written through
// undecoded — the writer is a byte sink and re-encoding a chardet guess back to
// UTF-8 would corrupt exactly the inputs the guess got wrong. DIVERGENCES.md
// records the dropped decode, as it does for the Docker backend's twin.
//
// The command is a STRING, so it is `shlex.split` on the way in — which is why
// the `/var/kathara/*` glob is passed to the shell-less exec as a literal
// argument and matches nothing unless a file is named exactly that. Python has
// the same bug.
func (s *machineService) printStartupLog(ctx context.Context, labHash, machineName string, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}

	command, err := ShlexSplit("/bin/cat /var/log/shared.log /var/log/startup.log /var/kathara/*")
	if err != nil {
		return err
	}

	stream, err := s.execStream(ctx, labHash, machineName, command, execOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	if _, err := io.WriteString(out, "--- Startup Commands Log\n\n"); err != nil {
		return err
	}
	for {
		stdout, _, err := stream.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if _, err := out.Write(stdout); err != nil {
			return err
		}
	}
	_, err = io.WriteString(out, "\n--- End Startup Commands Log\n\n")
	return err
}

// ---------------------------------------------------------------------------
// Exec
// ---------------------------------------------------------------------------

// execOptions is the tail of `exec` (`KubernetesMachine.py:791`) minus the
// `is_stream` flag, which NILABILITY.tsv:61 splits into two methods.
type execOptions struct {
	TTY         bool
	Stdin       bool
	StdinBuffer [][]byte
	Stderr      bool
}

// resolveExecPod is the shared head of `exec` (`KubernetesMachine.py:819-824`):
// find the pod, or report the device as not running.
func (s *machineService) resolveExecPod(ctx context.Context, labHash, machineName string) (*corev1.Pod, error) {
	pods, err := s.getByFilters(ctx, labHash, machineName)
	if err != nil {
		// `except ApiException as e: raise e` — an identity re-raise, which
		// here means the taxonomy's passthrough code. Anything that is not an
		// ApiException — a cancelled context, say — is not caught in Python
		// either and travels untouched.
		return nil, translateAPI(err)
	}
	if len(pods) == 0 {
		return nil, kerrors.NewMachineNotRunning(machineName)
	}
	return pods[len(pods)-1], nil
}

// execCommandLogLine is the debug line both `exec` arms share:
// `"Executing command `%s` to device with name: %s" % (command, machine_name)`
// (`KubernetesMachine.py:817`). It is logged AFTER the `shlex.split` at
// `:816`, so `command` is always a list and interpolates as the list's repr —
// unlike the Docker backend's line, which sees the raw parameter.
func execCommandLogLine(command []string, machineName string) string {
	return "Executing command `" + util.PythonStrListRepr(command) +
		"` to device with name: " + machineName
}

// exec is `exec(..., is_stream=False)` → `_exec_all`.
func (s *machineService) exec(ctx context.Context, labHash, machineName string, command []string, opts execOptions) (execResult, error) {
	slog.Debug(execCommandLogLine(command, machineName))

	pod, err := s.resolveExecPod(ctx, labHash, machineName)
	if err != nil {
		return execResult{}, err
	}

	return runExecAll(ctx, s.executor, execRequest{
		Namespace: labHash,
		Pod:       pod.Name,
		Command:   command,
		Stdin:     opts.Stdin,
		Stderr:    opts.Stderr,
		TTY:       opts.TTY,
	}, machineName, opts.StdinBuffer)
}

// execStream is `exec(..., is_stream=True)` → `KubernetesExecStream`.
func (s *machineService) execStream(ctx context.Context, labHash, machineName string, command []string, opts execOptions) (kathara.ExecStream, error) {
	slog.Debug(execCommandLogLine(command, machineName))

	pod, err := s.resolveExecPod(ctx, labHash, machineName)
	if err != nil {
		return nil, err
	}

	return newExecStream(ctx, s.executor, execRequest{
		Namespace: labHash,
		Pod:       pod.Name,
		Command:   command,
		Stdin:     opts.Stdin,
		Stderr:    opts.Stderr,
		TTY:       opts.TTY,
	}, machineName, opts.StdinBuffer)
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// copyFiles is `copy_files` (`KubernetesMachine.py:922`): push a tar into the
// device and extract it at path.
//
// Python opens a STREAMING exec, writes the archive to its stdin and then takes
// a single `next()` — which, because `_exec_stream` breaks out of its loop
// before yielding once the buffer empties, closes the websocket immediately
// afterwards without waiting for `tar` to finish (k8s-backend.md G19). This
// waits instead: the Go transport owns the stdin reader and closes the remote
// side at EOF, and cutting it off early would truncate an upload Python does
// not truncate only because its write is synchronous. The command's output and
// exit status are still ignored, as Python ignores them. DIVERGENCES.md records
// the wait.
func (s *machineService) copyFiles(ctx context.Context, obj machineObject, path string, tarData []byte) error {
	machineName := obj.GetLabels()[labelName]
	machineNamespace := obj.GetNamespace()

	_, err := s.exec(ctx, machineNamespace, machineName,
		[]string{"tar", "xvfz", "-", "-C", path},
		execOptions{Stdin: true, StdinBuffer: [][]byte{tarData}})
	return err
}

// retrieveFiles is `retrieve_files` (`KubernetesMachine.py:948`): `tar` the
// device path to stdout, buffer it, extract it into dst.
//
// Python stages the stream in a `NamedTemporaryFile` because `tarfile` wants a
// seekable file; the bytes are buffered in memory here, which is the same thing
// without a temp file to leak. The extraction is `extractall` with no `filter=`,
// i.e. fully trusted — see [extractTar] for what that reproduces and what it
// does not.
func (s *machineService) retrieveFiles(ctx context.Context, obj machineObject, src, dst string) error {
	machineName := obj.GetLabels()[labelName]
	machineNamespace := obj.GetNamespace()

	stream, err := s.execStream(ctx, machineNamespace, machineName,
		[]string{"tar", "cf", "-", src}, execOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	var archive bytes.Buffer
	for {
		stdout, _, err := stream.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		archive.Write(stdout)
	}

	return extractTar(bytes.NewReader(archive.Bytes()), dst)
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// getByFilters is `get_machines_api_objects_by_filters`
// (`KubernetesMachine.py:983`).
//
// With no labHash it enumerates every Kathará namespace and lists each one,
// concatenating in namespace order (k8s-backend.md O13); with one it queries
// that namespace directly. "" is exactly as absent as Python's None.
//
// It lists PODS, not Deployments, and it does so with `timeout_seconds=9999`.
// The pods are what everything downstream reads — the exec target, the network
// annotation, the inventory — while the Deployment is only ever created and
// deleted.
func (s *machineService) getByFilters(ctx context.Context, labHash, machineName string) ([]*corev1.Pod, error) {
	namespaces, err := s.targetNamespaces(ctx, labHash)
	if err != nil {
		return nil, err
	}

	var pods []*corev1.Pod
	for _, namespace := range namespaces {
		list, err := s.clientset.CoreV1().Pods(namespace).List(ctx, listOptions(ObjectSelector(machineName)))
		if err != nil {
			return nil, translateAPI(err)
		}
		for i := range list.Items {
			pods = append(pods, &list.Items[i])
		}
	}
	return pods, nil
}

// targetNamespaces is the twin of [linkService.targetNamespaces]
// (`KubernetesMachine.py:998-999`).
func (s *machineService) targetNamespaces(ctx context.Context, labHash string) ([]string, error) {
	if labHash != "" {
		return []string{labHash}, nil
	}
	all, err := s.namespace.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for _, namespace := range all {
		names = append(names, namespace.Name)
	}
	return names, nil
}
