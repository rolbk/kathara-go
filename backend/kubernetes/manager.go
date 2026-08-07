// This file is `KubernetesManager.py`: the [kathara.Manager] surface, the
// lab-identifier resolution every method starts with, and the constructor's
// side effects.

package kubernetes

import (
	"context"
	"io"
	"log/slog"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/streaming/pkg/httpstream"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// BackendName is the `manager_type` value that selects this backend. It is
// compared exactly and case-sensitively ([kathara.Backend.Name]).
const BackendName = "kubernetes"

// FormattedName is `get_formatted_manager_name` (`KubernetesManager.py:951`).
const FormattedName = "Kubernetes (Megalos)"

// Backend is the registry row `cmd/kathara` registers from
// `backends_all.go`.
//
// There is no `init()` that registers it, and that is what makes the `nok8s`
// build work: a binary that does not name this package does not link
// `client-go` (PACKAGE_GRAPH.md §1.2, PORT_SPEC §0.2 #8).
func Backend() kathara.Backend {
	return kathara.Backend{
		Name:          BackendName,
		FormattedName: FormattedName,
		New:           New,
	}
}

// Manager is `KubernetesManager` (`KubernetesManager.py:27`).
//
// Its four Python slots — `k8s_secret`, `k8s_namespace`, `k8s_machine`,
// `k8s_link` — are here, plus the clients they share and the two things
// PORT_SPEC §0.2 #10 took away from the constructor and made parameters: the
// settings and the event dispatcher.
type Manager struct {
	clientset  kubernetes.Interface
	dynamic    dynamic.Interface
	settings   *settings.Settings
	dispatcher *event.Dispatcher
	defaults   model.Defaults

	namespace *namespaceService
	secret    *secretService
	machine   *machineService
	link      *linkService
	configMap *configMapService
}

var _ kathara.Manager = (*Manager)(nil)

// New is `KubernetesManager.__init__` (`KubernetesManager.py:32`).
//
// The order of its side effects is the contract, and it is a short one:
// `KubernetesConfig.load_kube_config()` first, then the four services. Unlike
// the Docker backend there is no ping and no version read — Megalos does not
// touch the cluster until an operation does, so an unreachable API server is
// reported by the first request and not here. What IS reported here is an
// unreadable configuration ([kerrors.ErrKubeConfigUnreadable]).
//
// `KubernetesLink.__init__` calls `get_cluster_user()` at this moment, which is
// why [ClusterConfig] carries the seed: the VNI derivation is fixed for the
// life of the Manager.
func New(_ context.Context, cfg kathara.Config) (kathara.Manager, error) {
	cluster, err := LoadKubeConfig(cfg.Settings)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(cluster.Rest)
	if err != nil {
		return nil, kerrors.NewKubernetesAPI(err)
	}
	dynamicClient, err := dynamic.NewForConfig(cluster.Rest)
	if err != nil {
		return nil, kerrors.NewKubernetesAPI(err)
	}

	executor := &restExecutorFactory{config: cluster.Rest, client: clientset.CoreV1().RESTClient()}

	return newManager(cfg, clientset, dynamicClient, executor, cluster.User), nil
}

// newManager wires the services. It is separate from [New] so that the tests
// can build a Manager over a fake clientset, a fake dynamic client and a fake
// executor without a cluster and without a kubeconfig.
func newManager(cfg kathara.Config, clientset kubernetes.Interface, dynamicClient dynamic.Interface, executor executorFactory, seed string) *Manager {
	m := &Manager{
		clientset:  clientset,
		dynamic:    dynamicClient,
		settings:   cfg.Settings,
		dispatcher: cfg.Dispatcher,
		defaults:   cfg.Defaults,
	}

	m.namespace = &namespaceService{clientset: clientset}
	m.secret = &secretService{clientset: clientset, dockerConfigJSON: cfg.Settings.DockerConfigJSON}
	m.configMap = &configMapService{clientset: clientset}
	m.machine = &machineService{
		clientset:      clientset,
		namespace:      m.namespace,
		configMap:      m.configMap,
		executor:       executor,
		dispatcher:     cfg.Dispatcher,
		settings:       cfg.Settings,
		portName:       randomPortName,
		startupTimeout: maxTimeError,
	}
	m.link = &linkService{
		dynamic:    dynamicClient,
		namespace:  m.namespace,
		dispatcher: cfg.Dispatcher,
		netPrefix:  cfg.Settings.NetPrefix,
		seed:       seed,
	}

	return m
}

// restExecutorFactory is the real transport behind
// `stream(self.core_client.connect_get_namespaced_pod_exec, …)`.
//
// Python's `stream()` upgrades to a WebSocket. client-go's default is SPDY, so
// the WebSocket executor is primary here and SPDY is the fallback for an API
// server too old to negotiate the v5 protocol — which is what `kubectl` does
// and what keeps the transport preference Python's.
type restExecutorFactory struct {
	config *rest.Config
	client rest.Interface
}

// NewExecutor builds the executor for one `POST /pods/{name}/exec`.
func (f *restExecutorFactory) NewExecutor(req execRequest) (remotecommand.Executor, error) {
	url := f.client.Post().
		Resource("pods").
		Name(req.Pod).
		Namespace(req.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Command: req.Command,
			Stdin:   req.Stdin,
			// `stdout=True` is hard-coded at every Python call site.
			Stdout: true,
			Stderr: req.Stderr,
			TTY:    req.TTY,
		}, scheme.ParameterCodec).
		URL()

	websocketExecutor, err := remotecommand.NewWebSocketExecutor(f.config, "GET", url.String())
	if err != nil {
		return nil, err
	}
	spdyExecutor, err := remotecommand.NewSPDYExecutor(f.config, "POST", url)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(websocketExecutor, spdyExecutor, httpstream.IsUpgradeFailure)
}

// GetFormattedManagerName is `get_formatted_manager_name`.
func (m *Manager) GetFormattedManagerName() string { return FormattedName }

// GetReleaseVersion is `get_release_version` (`KubernetesManager.py:942`):
// `client.VersionApi().get_code().git_version`, the CLUSTER's version and not
// Kathará's.
func (m *Manager) GetReleaseVersion(context.Context) (string, error) {
	version, err := m.clientset.Discovery().ServerVersion()
	if err != nil {
		return "", translateAPI(err)
	}
	return version.GitVersion, nil
}

// CheckImage is `check_image` (`KubernetesManager.py:930`): nothing at all. The
// cluster resolves the image when it schedules the pod, and a bad name is an
// ImagePullBackOff the watcher reports as a restart rather than an error here.
func (m *Manager) CheckImage(context.Context, string) error { return nil }

// ---------------------------------------------------------------------------
// Lab-identifier resolution
// ---------------------------------------------------------------------------

// resolveRequired is the three lines that open eleven methods
// (`KubernetesManager.py:281-290` and its ten twins):
//
//	check_required_single_not_none_var(lab_hash=…, lab_name=…, lab=…)
//	if lab: lab_hash = lab.hash
//	elif lab_name: lab_hash = generate_urlsafe_hash(lab_name)
//	lab_hash = lab_hash.lower()
//
// The third line is Megalos'. A Kubernetes namespace name must be an RFC 1123
// label and `generate_urlsafe_hash` produces mixed-case base64, so every
// identifier is folded — which means the Docker and Kubernetes backends compute
// DIFFERENT effective ids for the same scenario, and that the fold is applied
// AFTER the hash, never before (k8s-backend.md G1). Lowercasing the name first
// would give a different hash entirely.
func resolveRequired(ref kathara.LabRef) (string, error) {
	if err := ref.RequireSingle(); err != nil {
		return "", err
	}
	return strings.ToLower(resolveHash(ref)), nil
}

// resolveAtMostOne is the same with `check_single_not_none_var`
// (`KubernetesManager.py:591,658,768,859`): none at all is legal and means
// "every scenario in the cluster".
//
// The fold is guarded — `lab_hash.lower() if lab_hash else None` — so an absent
// identifier stays absent rather than becoming the empty string; here both
// spell "" and the guard is only cosmetic.
func resolveAtMostOne(ref kathara.LabRef) (string, error) {
	if err := ref.AtMostOne(); err != nil {
		return "", err
	}
	return strings.ToLower(resolveHash(ref)), nil
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

// lowerLabHash is `machine.lab.hash = machine.lab.hash.lower()`
// (`KubernetesManager.py:58,80,119,195,239`), which MUTATES the shared scenario
// object rather than a local (k8s-backend.md G1).
//
// The mutation is observable and load-bearing: everything downstream — the
// namespace name, the network annotation's `namespace` field, the ConfigMap
// name — reads `lab.hash` again, and a caller that holds the same [model.Lab]
// sees the folded value afterwards. Copying to a local instead would leave the
// device's own `machine.lab.hash` mixed-case and produce a Deployment in one
// namespace referring to networks in another.
func lowerLabHash(lab *model.Lab) {
	lab.Hash = strings.ToLower(lab.Hash)
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// DeployMachine is `deploy_machine` (`KubernetesManager.py:40`).
//
// Namespace, then Secret, then the device's collision domains, then the device
// — the same order [Manager.DeployLab] uses, and the links-before-machines half
// of it is a hard requirement rather than a preference: the pod annotation reads
// `interface.link.api_object` (k8s-backend.md G2/O9).
func (m *Manager) DeployMachine(ctx context.Context, machine *model.Machine) error {
	if machine.Lab == nil {
		return kerrors.NewLabNotFoundMachine(machine.Name)
	}

	if err := machine.Check(); err != nil {
		return err
	}

	lowerLabHash(machine.Lab)

	if _, err := m.namespace.Create(ctx, machine.Lab); err != nil {
		return err
	}
	if _, err := m.secret.Create(ctx, machine.Lab); err != nil {
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
// the set comprehension `deploy_machine` builds (`KubernetesManager.py:62`).
//
// A tombstone — the slot `remove_interface` leaves behind — has no `.link`, and
// Python's comprehension has no guard, so it crashes with
// `AttributeError: 'NoneType' object has no attribute 'link'`. Reproduced: the
// set decides which collision domains are deployed, and silently dropping a
// slot would deploy a different scenario.
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

// DeployLink is `deploy_link` (`KubernetesManager.py:65`).
//
// No Secret here: a collision domain pulls no image, so the private-registry
// credential is not created (`KubernetesManager.py:82-83`). A scenario deployed
// one collision domain at a time therefore has no Secret until its first
// device.
func (m *Manager) DeployLink(ctx context.Context, link *model.Link) error {
	if link.Lab == nil {
		return kerrors.NewLabNotFoundCollisionDomain(link.Name)
	}

	lowerLabHash(link.Lab)

	if _, err := m.namespace.Create(ctx, link.Lab); err != nil {
		return err
	}
	return m.link.DeployLinks(ctx, link.Lab, kathara.NewNameSet(link.Name), nil)
}

// DeployLab is `deploy_lab` (`KubernetesManager.py:85`).
//
// The order below is the order errors surface in, and it is observable:
//
//  1. `lab.check_integrity()` — before the filters are even looked at;
//  2. selected-and-excluded;
//  3. selected names not in the scenario, then excluded names not in it;
//  4. fold the hash, narrow the collision domains;
//  5. namespace and Secret, OUTSIDE the try;
//  6. links, then machines, INSIDE it.
//
// Step 5 being outside the `try` is why a namespace stuck Terminating is
// reported by the first pod creation and not by the namespace creation: that one
// swallows its own 409, and the 403 the pod creation gets is what becomes
// [kerrors.ErrLabTerminating].
//
// # The link narrowing
//
// `selected_machines` narrows to the collision domains those devices touch.
// `excluded_machines` computes the domains of the REMAINING devices and
// subtracts them from the excluded devices' domains, so a domain shared with a
// device that is still being deployed survives.
//
// Errors: [kerrors.ErrSelectOrExcludeDevices], [kerrors.ErrMachineNotFound]
// naming the set (sorted here, where Python's set repr is hash-ordered —
// k8s-backend.md G13), [kerrors.ErrLabTerminating].
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

	lowerLabHash(lab)

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

	if _, err := m.namespace.Create(ctx, lab); err != nil {
		return err
	}
	if _, err := m.secret.Create(ctx, lab); err != nil {
		return err
	}

	if err := m.link.DeployLinks(ctx, lab, selectedLinks, excludedLinks); err != nil {
		return translateForbidden(err)
	}
	return translateForbidden(m.machine.DeployMachines(ctx, lab, selected, excluded))
}

// translateForbidden is the `except ApiException` of `deploy_lab`
// (`KubernetesManager.py:141-145`): a 403 means the namespace is still
// terminating, everything else is re-raised into the taxonomy's passthrough
// code.
//
// A non-API error passes through untouched — Python's `except` does not catch
// those, so a [kerrors.ErrMachineAlreadyExists] from the machine layer arrives
// unchanged.
func translateForbidden(err error) error {
	switch {
	case err == nil:
		return nil
	case !isAPIException(err):
		return err
	case isForbidden(err):
		return kerrors.ErrLabTerminating
	}
	return translateAPI(err)
}

// missingMachines is `selected_machines - set(lab.machines.keys())`, the
// difference the MachineNotFound message names.
//
// Python interpolates a `set`, whose repr order is hash-randomised
// (k8s-backend.md G13); `kerrors.NewMachineNotFoundSet` sorts
// (ERROR_CODES.md §0.2).
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

// ConnectMachineToLink is `connect_machine_to_link`
// (`KubernetesManager.py:147`): permanently unsupported on this backend, not
// deferred. A pod's network attachments are fixed at creation, so there is
// nothing to hot-plug.
func (m *Manager) ConnectMachineToLink(context.Context, *model.Machine, *model.Link, string) error {
	return kerrors.ErrUpdateRunningDevice
}

// DisconnectMachineFromLink is `disconnect_machine_from_link`
// (`KubernetesManager.py:163`), unsupported for the same reason and with the
// same message.
func (m *Manager) DisconnectMachineFromLink(context.Context, *model.Machine, *model.Link, bool) error {
	return kerrors.ErrUpdateRunningDevice
}

// ---------------------------------------------------------------------------
// Undeploy
// ---------------------------------------------------------------------------

// UndeployMachine is `undeploy_machine` (`KubernetesManager.py:179`).
//
// The collision-domain garbage collection is the whole of it: a domain is
// deleted when the device that is going away used it and NO still-running pod
// of the scenario references it in its `k8s.v1.cni.cncf.io/networks` annotation.
// The survivor set is read off the cluster, not off the model, so a domain kept
// alive by a device this process does not know about is kept.
//
// The namespace goes when the listing found no other device — the count is
// taken BEFORE the undeploy, so "no other device" means "this was the last one".
// `keep_links` suppresses both the domain deletion and the namespace deletion,
// which is why a `keep_links` teardown of the last device leaves an empty
// namespace behind.
func (m *Manager) UndeployMachine(ctx context.Context, machine *model.Machine, keepLinks bool) error {
	if machine.Lab == nil {
		return kerrors.NewLabNotFoundMachine(machine.Name)
	}

	lowerLabHash(machine.Lab)

	pods, err := m.machine.getByFilters(ctx, machine.Lab.Hash, "")
	if err != nil {
		return err
	}
	running := make([]*corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if !strings.Contains(string(pod.Status.Phase), "Terminating") && pod.Labels[labelName] != machine.Name {
			running = append(running, pod)
		}
	}

	var networksToDelete kathara.NameSet
	if !keepLinks {
		runningNetworks, err := attachedNetworkNames(running)
		if err != nil {
			return err
		}

		networksToDelete = kathara.NewNameSet()
		for _, iface := range machine.Interfaces() {
			if iface.IsTombstone() {
				return newPyAttributeError("link")
			}
			name := NetworkName(m.settings.NetPrefix, iface.Link.Name)
			if !runningNetworks.Has(name) {
				networksToDelete[name] = struct{}{}
			}
		}
	}

	if err := m.machine.Undeploy(ctx, machine.Lab.Hash, kathara.NewNameSet(machine.Name), nil); err != nil {
		return err
	}
	if !keepLinks {
		if err := m.link.Undeploy(ctx, machine.Lab.Hash, networksToDelete); err != nil {
			return err
		}
	}

	if len(running) == 0 && !keepLinks {
		slog.Debug("Waiting for namespace deletion...")
		return m.namespace.Undeploy(ctx, machine.Lab.Hash)
	}
	return nil
}

// attachedNetworkNames is the
// `running_networks.update([net['name'] for net in network_annotation])` loop
// that three of the undeploy paths share
// (`KubernetesManager.py:207-209,249-252,306-314`).
//
// The annotation is indexed unguarded, so a pod without one is a KeyError that
// fails the teardown — reproduced, because treating such a pod as "attached to
// nothing" would delete collision domains that are still carrying traffic.
func attachedNetworkNames(pods []*corev1.Pod) (kathara.NameSet, error) {
	names := kathara.NewNameSet()
	for _, pod := range pods {
		attachments, err := podNetworkAttachments(pod)
		if err != nil {
			return nil, err
		}
		for _, attachment := range attachments {
			names[attachment.Name] = struct{}{}
		}
	}
	return names, nil
}

// UndeployLink is `undeploy_link` (`KubernetesManager.py:224`): a silent no-op
// when any still-running pod references the collision domain.
//
// "Silent" is exact: no error, no warning, no event. `lclean` on a shared
// domain therefore looks like it worked.
func (m *Manager) UndeployLink(ctx context.Context, link *model.Link) error {
	if link.Lab == nil {
		return kerrors.NewLabNotFoundCollisionDomain(link.Name)
	}

	lowerLabHash(link.Lab)

	networkName := NetworkName(m.settings.NetPrefix, link.Name)

	pods, err := m.machine.getByFilters(ctx, link.Lab.Hash, "")
	if err != nil {
		return err
	}
	running := make([]*corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if !strings.Contains(string(pod.Status.Phase), "Terminating") {
			running = append(running, pod)
		}
	}

	for _, pod := range running {
		attachments, err := podNetworkAttachments(pod)
		if err != nil {
			return err
		}
		for _, attachment := range attachments {
			if attachment.Name == networkName {
				return nil
			}
		}
	}

	return m.link.Undeploy(ctx, link.Lab.Hash, kathara.NewNameSet(networkName))
}

// UndeployLab is `undeploy_lab` (`KubernetesManager.py:257`).
//
// Three passes, in this order:
//
//  1. when either machine filter is given, list every collision domain and
//     every running pod of the scenario ONCE, and compute the domains that no
//     SURVIVING pod references — a survivor being a pod that is not selected, or
//     that is excluded;
//  2. when `selected_links` is given, intersect with the domains whose NAD
//     label matches those collision-domain names — and note the asymmetry:
//     `selected_links` names collision domains, while what comes out is
//     Kubernetes network names;
//  3. undeploy machines, undeploy links, and delete the namespace only on a
//     full teardown.
//
// The namespace test (`KubernetesManager.py:346-351`) is four conditions and
// each arm is reachable: no filter at all; a selection covering every running
// device; an exclusion that intersects nothing that is running; and in every
// case no `selected_links`. Any use of `selected_links` keeps the namespace,
// even one that deletes every collision domain.
//
// The manager-level both-filters guard is TRUTHINESS, and the machine layer's
// is `is not None`, so two non-nil EMPTY sets pass here and trip there
// (k8s-backend.md G3).
func (m *Manager) UndeployLab(ctx context.Context, ref kathara.LabRef, opts kathara.UndeployLabOptions) error {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return err
	}

	selected, excluded, selectedLinks := opts.SelectedMachines, opts.ExcludedMachines, opts.SelectedLinks
	if len(selected) > 0 && len(excluded) > 0 {
		return kerrors.ErrSelectOrExcludeDevices
	}

	var networks []*Network
	var networksToDelete kathara.NameSet
	runningMachines := kathara.NewNameSet()

	if len(selected) > 0 || len(excluded) > 0 {
		networks, err = m.link.getByFilters(ctx, labHash, "")
		if err != nil {
			return err
		}
		allNetworks := kathara.NewNameSet()
		for _, network := range networks {
			allNetworks[NetworkNameOf(network)] = struct{}{}
		}

		pods, err := m.machine.getByFilters(ctx, labHash, "")
		if err != nil {
			return err
		}
		running := make([]*corev1.Pod, 0, len(pods))
		for _, pod := range pods {
			if !strings.Contains(string(pod.Status.Phase), "Terminating") {
				running = append(running, pod)
			}
		}

		survivors := kathara.NewNameSet()
		for _, pod := range running {
			attachments, err := podNetworkAttachments(pod)
			if err != nil {
				return err
			}
			// "To be running, you are not selected to undeploy, or you are
			// excluded from undeploy." Both tests are `is not None` on the
			// filter, so an empty selection makes every pod a survivor.
			name := pod.Labels[labelName]
			if (selected != nil && !selected.Has(name)) || (excluded != nil && excluded.Has(name)) {
				for _, attachment := range attachments {
					survivors[attachment.Name] = struct{}{}
				}
			}
		}

		networksToDelete = kathara.NewNameSet()
		for name := range allNetworks {
			if !survivors.Has(name) {
				networksToDelete[name] = struct{}{}
			}
		}

		for _, pod := range running {
			runningMachines[pod.Labels[labelName]] = struct{}{}
		}
	}

	if selectedLinks != nil {
		if networks == nil {
			networks, err = m.link.getByFilters(ctx, labHash, "")
			if err != nil {
				return err
			}
		}
		selectedNetworks := kathara.NewNameSet()
		for _, network := range networks {
			if selectedLinks.Has(NetworkLinkName(network)) {
				selectedNetworks[NetworkNameOf(network)] = struct{}{}
			}
		}

		// `networks_to_delete & selected_networks_to_delete if networks_to_delete
		// else selected_networks_to_delete` — a TRUTHINESS test, so an empty
		// machine-derived set is replaced wholesale rather than intersected.
		if len(networksToDelete) > 0 {
			intersection := kathara.NewNameSet()
			for name := range networksToDelete {
				if selectedNetworks.Has(name) {
					intersection[name] = struct{}{}
				}
			}
			networksToDelete = intersection
		} else {
			networksToDelete = selectedNetworks
		}
	}

	if err := m.machine.Undeploy(ctx, labHash, selected, excluded); err != nil {
		return err
	}
	if err := m.link.Undeploy(ctx, labHash, networksToDelete); err != nil {
		return err
	}

	undeployNamespace := (len(selected) == 0 && len(excluded) == 0) ||
		(len(selected) > 0 && !anyOutside(runningMachines, selected)) ||
		(len(excluded) > 0 && !anyInside(runningMachines, excluded))
	undeployNamespace = undeployNamespace && len(selectedLinks) == 0

	if undeployNamespace {
		slog.Debug("Waiting for namespace deletion...")
		return m.namespace.Undeploy(ctx, labHash)
	}
	return nil
}

// anyOutside is `bool(running_machines - selected_machines)`: whether any
// running device was left unselected.
func anyOutside(running, selected kathara.NameSet) bool {
	for name := range running {
		if !selected.Has(name) {
			return true
		}
	}
	return false
}

// anyInside is `bool(running_machines & excluded_machines)`: whether any
// running device was excluded from the teardown.
func anyInside(running, excluded kathara.NameSet) bool {
	for name := range running {
		if excluded.Has(name) {
			return true
		}
	}
	return false
}

// Wipe is `wipe` (`KubernetesManager.py:356`): delete every Kathará namespace
// and let the API server cascade.
//
// It touches neither pods nor networks — `KubernetesMachine.wipe` and
// `KubernetesLink.wipe` exist and are unreachable from here (k8s-backend.md
// G26) — and `all_users` is meaningless on a cluster where namespaces carry no
// user, so it only warns.
func (m *Manager) Wipe(ctx context.Context, allUsers bool) error {
	if allUsers {
		slog.Warn("User-specific options have no effect on Megalos.")
	}
	return m.namespace.Wipe(ctx)
}

// ---------------------------------------------------------------------------
// Interactive
// ---------------------------------------------------------------------------

// ConnectTTY is `connect_tty` (`KubernetesManager.py:371`).
//
// `wait` is accepted and IGNORED, with no warning — unlike [Manager.Exec],
// which warns. That asymmetry is Python's (k8s-backend.md G15).
func (m *Manager) ConnectTTY(ctx context.Context, machineName string, ref kathara.LabRef, opts kathara.ConnectTTYOptions) (kathara.TTYSession, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}
	return m.machine.Connect(ctx, labHash, machineName, opts)
}

// ConnectTTYObj is `connect_tty_obj` (`KubernetesManager.py:411`), which uses
// the "Device" spelling of the LabNotFound message where `deploy_machine` uses
// "Machine".
func (m *Manager) ConnectTTYObj(ctx context.Context, machine *model.Machine, opts kathara.ConnectTTYOptions) (kathara.TTYSession, error) {
	if machine.Lab == nil {
		return nil, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	return m.ConnectTTY(ctx, machine.Name, kathara.LabRef{Lab: machine.Lab}, opts)
}

// Exec is `exec(..., stream=False)` (`KubernetesManager.py:435`).
//
// `stderr=True, tty=False` are forced here, which is what makes the two output
// sides separable.
func (m *Manager) Exec(ctx context.Context, machineName string, command kathara.Command, ref kathara.LabRef, wait kathara.WaitPolicy) ([]byte, []byte, int, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, nil, 0, err
	}
	warnWaitIgnored(wait)

	words, err := commandWords(command)
	if err != nil {
		return nil, nil, 0, err
	}

	result, err := m.machine.exec(ctx, labHash, machineName, words, execOptions{Stderr: true})
	if err != nil {
		return nil, nil, 0, err
	}
	return result.Stdout, result.Stderr, result.ExitCode, nil
}

// ExecObj is `exec_obj(..., stream=False)` (`KubernetesManager.py:478`).
//
// The warning fires TWICE for a waiting caller: once here and once in the
// `exec` this delegates to (k8s-backend.md G15). Preserved.
func (m *Manager) ExecObj(ctx context.Context, machine *model.Machine, command kathara.Command, wait kathara.WaitPolicy) ([]byte, []byte, int, error) {
	if machine.Lab == nil {
		return nil, nil, 0, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	warnWaitIgnored(wait)
	return m.Exec(ctx, machine.Name, command, kathara.LabRef{Lab: machine.Lab}, wait)
}

// ExecStream is `exec(..., stream=True)`.
func (m *Manager) ExecStream(ctx context.Context, machineName string, command kathara.Command, ref kathara.LabRef, wait kathara.WaitPolicy) (kathara.ExecStream, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}
	warnWaitIgnored(wait)

	words, err := commandWords(command)
	if err != nil {
		return nil, err
	}

	return m.machine.execStream(ctx, labHash, machineName, words, execOptions{Stderr: true})
}

// ExecStreamObj is `exec_obj(..., stream=True)`, with the same doubled warning.
func (m *Manager) ExecStreamObj(ctx context.Context, machine *model.Machine, command kathara.Command, wait kathara.WaitPolicy) (kathara.ExecStream, error) {
	if machine.Lab == nil {
		return nil, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	warnWaitIgnored(wait)
	return m.ExecStream(ctx, machine.Name, command, kathara.LabRef{Lab: machine.Lab}, wait)
}

// warnWaitIgnored is `if wait: logging.warning("Wait option has no effect on
// Megalos.")` (`KubernetesManager.py:473-474,504-505`).
//
// The test is Python's truthiness of the `wait` union: `False` is silent, `True`
// warns, and a `(retries, interval)` TUPLE is always truthy — even `(0, 0.0)`,
// since a non-empty tuple is truthy whatever it holds. [kathara.WaitPolicy]
// spells all three as `Enabled`.
func warnWaitIgnored(wait kathara.WaitPolicy) {
	if wait.Enabled {
		slog.Warn("Wait option has no effect on Megalos.")
	}
}

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

// CopyFiles is `copy_files` (`KubernetesManager.py:509`): pack the pairs into a
// tar and extract it at the pod's root.
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

	obj, ok := machineObjectOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("metadata")
	}
	return m.machine.copyFiles(ctx, obj, "/", tarData)
}

// copyEntryContent is `pack_file_for_tar`'s value branch (`utils.py:430-435`):
// a host path gets the `convert_win_2_linux` pass, a reader is shipped
// verbatim. Content wins when both are set ([kathara.CopyEntry]).
func copyEntryContent(entry kathara.CopyEntry) ([]byte, error) {
	if entry.Content != nil {
		return io.ReadAll(entry.Content)
	}
	return util.ConvertWin2Linux(entry.HostPath)
}

// RetrieveFiles is `retrieve_files` (`KubernetesManager.py:524`).
func (m *Manager) RetrieveFiles(ctx context.Context, machine *model.Machine, src, dst string) error {
	obj, ok := machineObjectOf(machine.APIObject)
	if !ok {
		return newPyAttributeError("metadata")
	}
	return m.machine.retrieveFiles(ctx, obj, src, dst)
}

// ---------------------------------------------------------------------------
// API objects
// ---------------------------------------------------------------------------

// GetMachineAPIObject is `get_machine_api_object`
// (`KubernetesManager.py:537`): the POD, not the Deployment. `pods.pop()` takes
// the LAST match (k8s-backend.md O10).
func (m *Manager) GetMachineAPIObject(ctx context.Context, machineName string, ref kathara.LabRef, allUsers bool) (any, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}
	warnAllUsersIgnored(allUsers)

	pods, err := m.machine.getByFilters(ctx, labHash, machineName)
	if err != nil {
		return nil, err
	}
	if len(pods) > 0 {
		return pods[len(pods)-1], nil
	}
	return nil, kerrors.NewMachineNotFoundUnquoted(machineName)
}

// GetMachinesAPIObjects is `get_machines_api_objects`
// (`KubernetesManager.py:575`): at-most-one ref, and none at all means every
// scenario in the cluster.
func (m *Manager) GetMachinesAPIObjects(ctx context.Context, ref kathara.LabRef, allUsers bool) ([]any, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}
	warnAllUsersIgnored(allUsers)

	pods, err := m.machine.getByFilters(ctx, labHash, "")
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(pods))
	for _, pod := range pods {
		out = append(out, pod)
	}
	return out, nil
}

// GetLinkAPIObject is `get_link_api_object` (`KubernetesManager.py:604`).
func (m *Manager) GetLinkAPIObject(ctx context.Context, linkName string, ref kathara.LabRef, allUsers bool) (any, error) {
	labHash, err := resolveRequired(ref)
	if err != nil {
		return nil, err
	}
	warnAllUsersIgnored(allUsers)

	networks, err := m.link.getByFilters(ctx, labHash, linkName)
	if err != nil {
		return nil, err
	}
	if len(networks) > 0 {
		return networks[len(networks)-1], nil
	}
	return nil, kerrors.NewLinkNotFoundUnquoted(linkName)
}

// GetLinksAPIObjects is `get_links_api_objects` (`KubernetesManager.py:642`).
func (m *Manager) GetLinksAPIObjects(ctx context.Context, ref kathara.LabRef, allUsers bool) ([]any, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}
	warnAllUsersIgnored(allUsers)

	networks, err := m.link.getByFilters(ctx, labHash, "")
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(networks))
	for _, network := range networks {
		out = append(out, network)
	}
	return out, nil
}

// warnAllUsersIgnored is `if all_users: logging.warning("User-specific options
// have no effect on Megalos.")`, which appears at six sites
// (`KubernetesManager.py:367,567,600,634,667,776,868`).
func warnAllUsersIgnored(allUsers bool) {
	if allUsers {
		slog.Warn("User-specific options have no effect on Megalos.")
	}
}

// ---------------------------------------------------------------------------
// Reconstruction
// ---------------------------------------------------------------------------

// GetLabFromAPI is `get_lab_from_api` (`KubernetesManager.py:671`): rebuild a
// [model.Lab] from the pods and NADs that are actually running.
//
// Its guard is `if not lab_hash and not lab_name` — a truthiness test, unlike
// the `is not None` count the rest of the interface uses — so an empty string
// genuinely is absent here, and when BOTH are given `lab_name` wins
// (NILABILITY.tsv:65). A scenario addressed by hash comes back named
// "reconstructed_lab" with the hash forced onto it, because the name that
// produced the hash is not recoverable.
//
// # What cannot be rebuilt
//
// `privileged` and `bridged` have no Megalos representation, and `sysctls`,
// `exec`, `ipv6` and `num_terms` are not readable back off a pod — the sysctls
// live inside the postStart script's text and nothing parses it. So the
// reconstruction carries `image`, `shell`, `mem`, `cpu`, `envs`, `ports` and the
// interfaces, and no more.
//
// # Two Python details that are preserved because they are observable
//
// The CPU meta is written under the key `cpu`, while `Machine.get_cpu` reads
// `cpus` — so the value round-trips into a meta nothing consults. And it is
// written as a FLOAT (`int(limit.replace('m',”)) / 1000`), where every other
// meta is a string; both are kept ([model.Meta.Extras] holds it).
//
// The interface NUMBERS come from the annotation's array POSITION, not from its
// `interface: netN` field: `add_interface` is called without a number, so it
// takes `len(interfaces)`. A scenario with a hole in its numbering therefore
// comes back renumbered (k8s-backend.md O2).
//
// Errors: [kerrors.ErrLabHashOrName] when both identifiers are empty.
func (m *Manager) GetLabFromAPI(ctx context.Context, labHash, labName string) (*model.Lab, error) {
	if labHash == "" && labName == "" {
		return nil, kerrors.ErrLabHashOrName
	}

	var lab *model.Lab
	if labName != "" {
		lab = model.NewLab(labName, m.defaults)
	} else {
		lab = model.NewLab("reconstructed_lab", m.defaults)
		lab.Hash = labHash
	}

	pods, err := m.GetMachinesAPIObjects(ctx, kathara.LabRef{Hash: lab.Hash}, false)
	if err != nil {
		return nil, err
	}
	networkObjects, err := m.GetLinksAPIObjects(ctx, kathara.LabRef{Hash: lab.Hash}, false)
	if err != nil {
		return nil, err
	}

	networks := make(map[string]*Network, len(networkObjects))
	for _, obj := range networkObjects {
		network, _ := obj.(*Network)
		networks[NetworkNameOf(network)] = network
	}

	for _, obj := range pods {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		if err := m.reconstructDevice(lab, pod, networks); err != nil {
			return nil, err
		}
	}

	return lab, nil
}

// reconstructDevice is the body of `get_lab_from_api`'s pod loop
// (`KubernetesManager.py:698-734`).
func (m *Manager) reconstructDevice(lab *model.Lab, pod *corev1.Pod, networks map[string]*Network) error {
	device, err := lab.GetOrNewMachine(pod.Labels[labelName], nil)
	if err != nil {
		return err
	}
	device.APIObject = pod

	if len(pod.Spec.Containers) == 0 {
		// `pod.spec.containers[0]` on an empty list.
		return &model.PyRuntimeError{Class: "IndexError", Msg: "list index out of range"}
	}
	container := pod.Spec.Containers[0]

	if _, _, err := device.AddMeta("image", container.Image); err != nil {
		return err
	}
	// `get_env_var_value_from_pod` answers None when the variable is absent,
	// and `add_meta` stores that None. The model cannot hold a stored None
	// (model.Kind), so an absent variable becomes "" — which every reader
	// treats the same way, since both are falsy.
	if _, _, err := device.AddMeta("shell", EnvVarValueFromPod(pod, megalosShellEnv)); err != nil {
		return err
	}

	if memory, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
		// `limits['memory'].upper()` on the string the API server canonicalised,
		// which for a limit this backend wrote is already upper-case.
		if _, _, err := device.AddMeta("mem", strings.ToUpper(memory.String())); err != nil {
			return err
		}
	}
	if cpu, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
		// `int(limits['cpu'].replace('m', '')) / 1000` — true division, so the
		// result is a float. A limit written WITHOUT the `m` suffix (set out of
		// band) parses as its whole number and divides to a thousandth, which is
		// Python's answer too (k8s-backend.md G22).
		millis, err := util.PyInt(strings.ReplaceAll(cpu.String(), "m", ""))
		if err != nil {
			failure := util.PyIntFailure(err, strings.ReplaceAll(cpu.String(), "m", ""))
			return kerrors.WrapValue(failure, failure.Error())
		}
		device.Meta.Extras.Set("cpu", model.Float(float64(millis)/1000))
	}

	// `device.meta["envs"][env.name] = env.value` — a direct dict write that
	// bypasses `add_meta` and therefore its parsing.
	for _, env := range container.Env {
		if env.Name != megalosShellEnv {
			device.Meta.Envs.Set(env.Name, env.Value)
		}
	}

	// `if container.ports:` — a truthiness test, so a container with no ports
	// leaves the meta untouched.
	for _, port := range container.Ports {
		device.Meta.Ports.Set(
			model.PortKey{HostPort: int(port.HostPort), Protocol: strings.ToLower(string(port.Protocol))},
			int(port.ContainerPort),
		)
	}

	attachments, err := podNetworkAttachments(pod)
	if err != nil {
		return err
	}
	for _, attachment := range attachments {
		network, ok := networks[attachment.Name]
		if !ok {
			// `lab_networks[network_conf['name']]` on a name the listing did not
			// return — a collision domain deleted between the two queries.
			return newPyKeyError(attachment.Name)
		}
		link := lab.GetOrNewLink(NetworkLinkName(network))
		link.APIObject = network

		if _, err := device.AddInterface(link, model.AddInterfaceOptions{MAC: attachment.MAC}); err != nil {
			return err
		}
	}

	return nil
}

// UpdateLabFromAPI is `update_lab_from_api` (`KubernetesManager.py:738`):
// permanently unsupported, not deferred.
func (m *Manager) UpdateLabFromAPI(context.Context, *model.Lab) error {
	return kerrors.ErrUpdateRunningLab
}

// ---------------------------------------------------------------------------
// Stats
// ---------------------------------------------------------------------------

// GetMachinesStats is `get_machines_stats` (`KubernetesManager.py:749`).
//
// The ref check runs NOW: this Python method holds no `yield`, so it validates
// and then returns the generator its machine layer built. Its singular sibling
// does hold one and behaves differently on purpose
// ([kathara.Manager.GetMachineStats]).
func (m *Manager) GetMachinesStats(ctx context.Context, ref kathara.LabRef, machineName string, allUsers bool) (kathara.MachinesStatsStream, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}
	warnAllUsersIgnored(allUsers)

	return &machinesStatsStream{machine: m.machine, labHash: labHash, machineName: machineName}, nil
}

// GetMachineStats is `get_machine_stats` (`KubernetesManager.py:781`), a Python
// generator function: nothing in its body runs until the first `next()`, so the
// ref check is carried into the stream rather than performed here.
//
// `all_users` is accepted and DROPPED, with no warning — the body forwards
// `lab_hash` and `machine_name` to the plural getter and does not pass
// `all_users` on (`KubernetesManager.py:811`), so the warning every other
// method emits never fires on this one. Its collision-domain twin does forward
// it and does warn ([Manager.GetLinkStats]).
func (m *Manager) GetMachineStats(_ context.Context, machineName string, ref kathara.LabRef, _ bool) kathara.MachineStatsStream {
	check := ref.RequireSingle()
	labHash := strings.ToLower(resolveHash(ref))

	return &machineStatsStream{
		check: check,
		inner: &machinesStatsStream{machine: m.machine, labHash: labHash, machineName: machineName},
	}
}

// GetMachineStatsObj is `get_machine_stats_obj` (`KubernetesManager.py:819`),
// whose body holds no `yield` — hence the eager LabNotFound, in the "Device"
// spelling.
func (m *Manager) GetMachineStatsObj(ctx context.Context, machine *model.Machine, allUsers bool) (kathara.MachineStatsStream, error) {
	if machine.Lab == nil {
		return nil, kerrors.NewLabNotFoundDevice(machine.Name)
	}
	return m.GetMachineStats(ctx, machine.Name, kathara.LabRef{Lab: machine.Lab}, allUsers), nil
}

// GetLinksStats is `get_links_stats` (`KubernetesManager.py:840`).
func (m *Manager) GetLinksStats(ctx context.Context, ref kathara.LabRef, linkName string, allUsers bool) (kathara.LinksStatsStream, error) {
	labHash, err := resolveAtMostOne(ref)
	if err != nil {
		return nil, err
	}
	warnAllUsersIgnored(allUsers)

	return &linksStatsStream{link: m.link, labHash: labHash, linkName: linkName}, nil
}

// GetLinkStats is `get_link_stats` (`KubernetesManager.py:872`), lazy for the
// same reason [Manager.GetMachineStats] is.
//
// Unlike [Manager.GetMachineStats] it DOES forward `all_users` to the plural
// getter (`KubernetesManager.py:902`), which warns — and since the forwarding
// happens inside the generator, the warning arrives at the first `Next` rather
// than at the call.
func (m *Manager) GetLinkStats(_ context.Context, linkName string, ref kathara.LabRef, allUsers bool) kathara.LinkStatsStream {
	check := ref.RequireSingle()
	labHash := strings.ToLower(resolveHash(ref))

	return &linkStatsStream{
		check:        check,
		warnAllUsers: allUsers,
		inner:        &linksStatsStream{link: m.link, labHash: labHash, linkName: linkName},
	}
}

// GetLinkStatsObj is `get_link_stats_obj` (`KubernetesManager.py:910`), which
// uses the "Link" spelling of the LabNotFound message.
func (m *Manager) GetLinkStatsObj(ctx context.Context, link *model.Link, allUsers bool) (kathara.LinkStatsStream, error) {
	if link.Lab == nil {
		return nil, kerrors.NewLabNotFoundLink(link.Name)
	}
	return m.GetLinkStats(ctx, link.Name, kathara.LabRef{Lab: link.Lab}, allUsers), nil
}
