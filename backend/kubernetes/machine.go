// This file is `KubernetesMachine.py`: devices as Deployments, and the pods
// those Deployments produce.
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

	shutdownTimeout time.Duration

	// portName is `str(uuid.uuid4()).replace('-', '')[0:15]`
	// (`KubernetesMachine.py:419`), the name of a published container port.
	portName func() string
}

// randomPortName is this implementation-name generator: fifteen lower-case hex characters,
// which is what `uuid4().hex[:15]` produces.
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
		deployErr = runChunked(opCtx, machines, machineItemName, s.deployMachine)
	} else {

		for _, machine := range machines {
			if deployErr = s.deployMachine(opCtx, machine); deployErr != nil {
				break
			}
		}
	}

	if deployErr != nil {
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
func (s *machineService) deployMachine(ctx context.Context, machine *model.Machine) error {
	return s.Create(ctx, machine)
}

// waitMachinesStartup is `_wait_machines_startup`
// (`KubernetesMachine.py:209`): count pods into Ready until every watched
// device has reported, or has restarted too many times to be worth waiting for.
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
			// context cancellation of the startup timeout.
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
func realName(machine *model.Machine) string {
	if value, ok := machine.Meta.Extras.Get(realNameMeta); ok {
		return value.String()
	}
	return ""
}

// buildDefinition is `_build_definition` (`KubernetesMachine.py:372`): the
// Deployment, built from a device and the ConfigMap holding its files.
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
			// It also produces strings Kubernetes has no suffix for.
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
	// configured default.
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
func (s *machineService) Undeploy(ctx context.Context, labHash string, selected, excluded kathara.NameSet) error {
	if selected != nil && excluded != nil {
		return kerrors.ErrSelectedOrExcludedMachines
	}

	pods, err := s.getByFilters(ctx, labHash, "")
	if err != nil {
		return err
	}

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

	if undeployErr := runChunked(ctx, pods, podItemName, s.undeployMachine); undeployErr != nil {

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
func (s *machineService) Wipe(ctx context.Context) error {
	pods, err := s.getByFilters(ctx, "", "")
	if err != nil {
		return err
	}
	return runChunked(ctx, pods, podItemName, s.undeployMachine)
}

// undeployMachine is `_undeploy_machine` → `_delete_machine`
// (`KubernetesMachine.py:656,668`): run the shutdown script inside the pod,
// then delete the ConfigMap and the Deployment.
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

	return translateAPI(
		s.clientset.AppsV1().Deployments(machineNamespace).Delete(ctx, deploymentName, metav1.DeleteOptions{}))
}

// ---------------------------------------------------------------------------
// Interactive
// ---------------------------------------------------------------------------

// Connect is `connect` (`KubernetesMachine.py:696`): open an interactive shell
// on a running device.
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
