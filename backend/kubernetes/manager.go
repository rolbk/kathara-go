// This file is `KubernetesManager.py`: the [kathara.Manager] surface, the
// lab-identifier resolution every method starts with, and the constructor's
// side effects.

package kubernetes

import (
	"context"
	"errors"
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
func Backend() kathara.Backend {
	return kathara.Backend{
		Name:          BackendName,
		FormattedName: FormattedName,
		New:           New,
	}
}

// Manager is `KubernetesManager` (`KubernetesManager.py:27`).
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
func resolveRequired(ref kathara.LabRef) (string, error) {
	if err := ref.RequireSingle(); err != nil {
		return "", err
	}
	return strings.ToLower(resolveHash(ref)), nil
}

// resolveAtMostOne is the same with `check_single_not_none_var`
// (`KubernetesManager.py:591,658,768,859`): none at all is legal and means
// "every scenario in the cluster".
func resolveAtMostOne(ref kathara.LabRef) (string, error) {
	if err := ref.AtMostOne(); err != nil {
		return "", err
	}
	return strings.ToLower(resolveHash(ref)), nil
}

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

func lowerLabHash(lab *model.Lab) {
	lab.Hash = strings.ToLower(lab.Hash)
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// DeployMachine is `deploy_machine` (`KubernetesManager.py:40`).
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
func translateForbidden(err error) error {
	if batch := kerrors.Joined(err); len(batch) > 0 {
		translated := make([]error, len(batch))
		for i, e := range batch {
			translated[i] = translateForbidden(e)
		}
		return errors.Join(translated...)
	}

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
